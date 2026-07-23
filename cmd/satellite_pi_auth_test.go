package cmd

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestParsePiCodexStatusAllowsMetadataOnly(t *testing.T) {
	usable := true
	got, err := parsePiCodexStatus(`{"provider":"openai-codex","present":true,"type":"oauth","usable":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "openai-codex" || !got.Present || got.Usable == nil || *got.Usable != usable {
		t.Fatalf("status = %+v", got)
	}
}

func TestParsePiCodexStatusStructurallyRedactsUnknownValues(t *testing.T) {
	canary := "GROVE_SECRET_CANARY_DO_NOT_LOG"
	_, err := parsePiCodexStatus(`{"provider":"openai-codex","present":true,"access":"` + canary + `"}`)
	if err == nil {
		t.Fatal("secret-bearing field was accepted")
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatal("secret canary escaped in error")
	}
	if _, err := parsePiCodexStatus("{}\n{}\n"); err == nil {
		t.Fatal("multiple records accepted")
	}
}

func TestPiCodexRemoteCommandEnforcesGuestLocalSafety(t *testing.T) {
	script := piCodexRemoteCommand("status", true)
	for _, want := range []string{"[ ! -L \"$auth\" ]", "stat -c '%u'", "stat -c '%a'", "= 600", "chmod 700", "--auth-path \"$auth\" --usable"} {
		if !strings.Contains(script, want) {
			t.Errorf("remote auth command missing %q", want)
		}
	}
	for _, forbidden := range []string{"environment.d", "CLAUDE_CODE_OAUTH_TOKEN", "CODEX_HOME", "OPENAI_API_KEY"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("remote auth command contains forbidden representation %q", forbidden)
		}
	}
	check := exec.Command("bash", "-n")
	check.Stdin = strings.NewReader(script)
	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("auth script syntax: %v: %s", err, out)
	}
}

func TestPiCodexAuthMessagingSeparatesSnapshotAndRevocationPolicy(t *testing.T) {
	for _, want := range []string{"never create an image, snapshot, or clone", "cannot enforce direct Tart", "no supported Pi provider revoke callback", "revoke separately"} {
		if !strings.Contains(authenticatedGuestSnapshotPolicy+"\n"+upstreamRevocationGuidance, want) {
			t.Errorf("auth hardening guidance missing %q", want)
		}
	}
}

func TestSatelliteAuthCommandShape(t *testing.T) {
	root := newSatelliteAuthCmd()
	if root.Use != "auth" {
		t.Fatalf("root use = %q", root.Use)
	}
	pi, _, err := root.Find([]string{"pi-codex", "status"})
	if err != nil {
		t.Fatal(err)
	}
	if pi.Name() != "status" {
		t.Fatalf("resolved command = %q", pi.Name())
	}
	var out bytes.Buffer
	pi.SetOut(&out)
}

func TestSanitizeRemoteAuthErrorDropsProviderBodies(t *testing.T) {
	canary := "GROVE_SECRET_CANARY_DO_NOT_LOG"
	got := sanitizeRemoteAuthError(fakeAuthError(canary))
	if strings.Contains(got.Error(), canary) {
		t.Fatal("provider body escaped redaction")
	}
}

type fakeAuthError string

func (e fakeAuthError) Error() string { return string(e) }
