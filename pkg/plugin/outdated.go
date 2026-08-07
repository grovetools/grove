package plugin

import (
	"context"
	"fmt"
	"strings"
)

// The read-only half of `update`.
//
// `update` can answer "has this moved?" only by moving it: Fetch rewrites the
// managed checkout and Checkout moves its working tree, so asking the question
// costs the answer. That is the wrong price for something a panel wants to ask
// on a keypress, and a checkout mutated behind a running plugin is a real
// hazard rather than a theoretical one.
//
// The honest primitive is `git ls-remote` — one round trip, nothing local
// touched at all — compared against the commit already pinned. Everything here
// is therefore safe to run at any time, which is the property that makes it
// usable from a TUI.

// UpdateState is what the check concluded about one plugin.
type UpdateState string

const (
	// StateCurrent means the pinned ref still names the pinned commit.
	StateCurrent UpdateState = "current"
	// StateOutdated means the ref names something else now. Nothing has moved:
	// `grove plugin update <name>` moves a pin, and it asks first.
	StateOutdated UpdateState = "outdated"
	// StateUnreachable means the remote could not be asked — offline, private,
	// renamed, deleted. Reported rather than raised: a check that cannot reach
	// one remote still has something true to say about every other plugin.
	StateUnreachable UpdateState = "unreachable"
	// StateDev means the question does not apply. A development install is
	// built from a working tree, so there is no ref to re-resolve and no pin
	// for a remote to be ahead of.
	StateDev UpdateState = "dev"
)

// OutdatedReport is one row of the check.
type OutdatedReport struct {
	Name string
	Pin  *Pin
	// State is the conclusion. Every plugin gets one, including the ones the
	// check could not answer for.
	State UpdateState
	// Latest is the commit the pinned ref names on the remote right now. Empty
	// for a dev entry and for an unreachable one — an empty string here means
	// "not known", never "nothing".
	Latest string
	// Reason carries git's own words when State is StateUnreachable, because
	// "unreachable" alone cannot tell a typo'd URL from a dropped wifi.
	Reason string
}

// Outdated checks the named plugins, or every installed one when no names are
// given.
//
// It returns an error only for something the caller got wrong — an unreadable
// lockfile, a plugin that is not installed. Everything the network can do to a
// check lands in a report instead, so a partial answer is still an answer.
func Outdated(ctx context.Context, names []string) ([]OutdatedReport, error) {
	lock, err := LoadLock()
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		names = lock.Names()
	}
	reports := make([]OutdatedReport, 0, len(names))
	for _, name := range names {
		pin := lock.Get(name)
		if pin == nil {
			return nil, fmt.Errorf("%q is not installed", name)
		}
		reports = append(reports, checkPin(ctx, name, pin))
	}
	return reports, nil
}

func checkPin(ctx context.Context, name string, pin *Pin) OutdatedReport {
	report := OutdatedReport{Name: name, Pin: pin, State: StateDev}
	if pin.Dev {
		return report
	}

	latest, err := LsRemote(ctx, pin.URL, pin.Ref)
	if err != nil {
		report.State = StateUnreachable
		report.Reason = firstLine(err.Error())
		return report
	}
	if latest == "" {
		// The remote answered and has no such ref. The ordinary reason is a pin
		// made against a bare commit: a commit is not a ref, so it never appears
		// in ls-remote — and it is also the one pin that can never go out of
		// date, which is worth saying rather than reporting as a failure.
		if isCommitRef(pin.Ref, pin.Commit) {
			report.State = StateCurrent
			report.Latest = pin.Commit
			return report
		}
		report.State = StateUnreachable
		report.Reason = fmt.Sprintf("%s no longer names anything on %s", refOrDefault(pin.Ref), pin.URL)
		return report
	}

	report.Latest = latest
	report.State = StateCurrent
	if latest != pin.Commit {
		report.State = StateOutdated
	}
	return report
}

// isCommitRef reports whether a pin's ref is the pinned commit itself, spelled
// in full or abbreviated. Only then is "the remote does not have this ref"
// expected rather than alarming.
func isCommitRef(ref, commit string) bool {
	if ref == "" || !strings.HasPrefix(commit, ref) {
		return false
	}
	for _, r := range ref {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// refOrDefault names the ref the way the pin means it, so a message about an
// empty ref does not read as a message about an empty string.
func refOrDefault(ref string) string {
	if ref == "" {
		return "the default branch"
	}
	return ref
}

// firstLine keeps a git failure to one row of a table. The rest of what git
// says is usually the same thing again with a URL in it.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
