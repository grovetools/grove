package cmd

import (
	"testing"

	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/syncproto"
)

// Registration intent, per notespace (P4 W4.1 x P3 W3.1).
//
// The regression these pin is the one the notebook lab's probe 10 found: every
// grove-side registration used to send `reconcile`, which the server refuses
// for a notespace whose subject claim already names a different one. Since a
// subject may legally hold siblings, that made `grove notebook share` fail
// outright on any notebook holding one — and fail the WHOLE share, so the
// notebook's membership was never written — and made `grove notespace move`
// unable to move a sibling into a shared notebook at all.

func stampFor(id, subject string) notespace.NotespaceStamp {
	return notespace.NotespaceStamp{ID: id, Name: "core", Subject: subject, Kind: "repo"}
}

func TestRegistrationIntentReconcilesTheRecordedPrimary(t *testing.T) {
	primaries := map[string]string{siblingSubject: fixtureNotespace1}
	if got := registrationIntentFor(stampFor(fixtureNotespace1, siblingSubject), primaries); got != syncproto.RegistrationIntentReconcile {
		t.Errorf("the recorded primary registered as %q, want %q", got, syncproto.RegistrationIntentReconcile)
	}
}

func TestRegistrationIntentTreatsAnythingElseAsASibling(t *testing.T) {
	primaries := map[string]string{siblingSubject: fixtureNotespace1}
	if got := registrationIntentFor(stampFor(fixtureNotespace2, siblingSubject), primaries); got != syncproto.RegistrationIntentCreateSibling {
		t.Errorf("a second notespace for the subject registered as %q, want %q", got, syncproto.RegistrationIntentCreateSibling)
	}
}

// A machine that records no primary for the subject has no basis to call
// anything a sibling, and reconcile is the pre-P4 answer a pre-P4 machine must
// keep getting.
func TestRegistrationIntentWithoutARecordedPrimaryReconciles(t *testing.T) {
	for name, primaries := range map[string]map[string]string{
		"nothing recorded":     nil,
		"another subject only": {"example.com/org/other": fixtureNotespace3},
		"empty entry":          {siblingSubject: ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := registrationIntentFor(stampFor(fixtureNotespace1, siblingSubject), primaries); got != syncproto.RegistrationIntentReconcile {
				t.Errorf("intent = %q, want %q", got, syncproto.RegistrationIntentReconcile)
			}
		})
	}
}

// Both intents the decision can produce are ones the wire contract accepts —
// the failure mode this whole file exists for was a request the SERVER refused,
// so the request has to be valid before the server's answer means anything.
func TestRegistrationIntentProducesValidRequests(t *testing.T) {
	primaries := map[string]string{siblingSubject: fixtureNotespace1}
	for _, id := range []string{fixtureNotespace1, fixtureNotespace2} {
		stamp := stampFor(id, siblingSubject)
		req := syncproto.RegisterRequest{
			RequestIdentity: syncproto.RequestIdentity{
				ProtocolVersion: syncproto.ProtocolVersionNotespaceID,
				IdempotencyKey:  "test",
				DeviceID:        "device-1",
			},
			Intent:              registrationIntentFor(stamp, primaries),
			Subject:             stamp.Subject,
			NotespaceName:       syncproto.NotespaceName(stamp.Name),
			Kind:                stamp.Kind,
			ProposedNotespaceID: syncproto.NotespaceID(stamp.ID),
		}
		if wire := req.Validate(); wire != nil {
			t.Errorf("register request for %s is invalid: %s", id, wire.Message)
		}
	}
}
