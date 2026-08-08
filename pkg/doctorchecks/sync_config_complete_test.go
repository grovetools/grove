package doctorchecks

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/doctor"
)

func runSyncConfigCheck(t *testing.T) doctor.CheckResult {
	t.Helper()
	config.ResetLoadCache()
	t.Cleanup(config.ResetLoadCache)
	return (&syncConfigCompleteCheck{}).Run(context.Background(), doctor.RunOptions{})
}

// No sync.toml is not a problem — it is the "sync is off" state, which is what
// most machines are in.
func TestSyncConfigComplete_NoConfigIsOK(t *testing.T) {
	setupScratchConfig(t)
	res := runSyncConfigCheck(t)
	if res.Status != doctor.StatusOK {
		t.Fatalf("status = %s (%s), want ok", res.Status, res.Message)
	}
}

// THE CHECK'S REASON FOR EXISTING: workspaces declared with no server. The
// daemon has nowhere to connect, so every counter stays at zero and every
// status surface stays plausible. Nothing else in the ecosystem says a word.
func TestSyncConfigComplete_WorkspacesWithoutAServerFail(t *testing.T) {
	groveDir := setupScratchConfig(t)
	write(t, filepath.Join(groveDir, "sync.toml"),
		"[[workspaces]]\nname = \"registry\"\nrole = \"registry\"\npull = true\n")

	res := runSyncConfigCheck(t)
	if res.Status != doctor.StatusFail {
		t.Fatalf("status = %s (%s), want fail", res.Status, res.Message)
	}
	if !strings.Contains(res.Error, "no `server`") {
		t.Errorf("the failure does not name the missing key: %q", res.Error)
	}
	if !strings.Contains(res.Resolution, "grove join --repair") {
		t.Errorf("the resolution does not name the fix: %q", res.Resolution)
	}
}

// A token_command that runs and prints nothing is the job-52 keychain bug:
// the item exists, the command succeeds, the value is "". The machine
// 401-loops and looks idle.
func TestSyncConfigComplete_TokenCommandThatResolvesEmptyFails(t *testing.T) {
	groveDir := setupScratchConfig(t)
	write(t, filepath.Join(groveDir, "sync.toml"),
		"server = \"https://sync.example.com\"\ntoken_command = \"printf ''\"\n\n"+
			"[[workspaces]]\nname = \"registry\"\nrole = \"registry\"\npull = true\n")

	res := runSyncConfigCheck(t)
	if res.Status != doctor.StatusFail {
		t.Fatalf("status = %s (%s), want fail", res.Status, res.Message)
	}
	if !strings.Contains(res.Error, "produces no token") {
		t.Errorf("the failure does not describe the empty resolution: %q", res.Error)
	}
}

// A failing token_command must be reported without quoting the command — a
// command like `echo hunter2` carries the secret in its own text, and so does
// its output.
func TestSyncConfigComplete_FailureCarriesNeitherCommandNorOutput(t *testing.T) {
	groveDir := setupScratchConfig(t)
	write(t, filepath.Join(groveDir, "sync.toml"),
		"server = \"https://sync.example.com\"\ntoken_command = \"echo hunter2 && exit 3\"\n\n"+
			"[[workspaces]]\nname = \"registry\"\nrole = \"registry\"\npull = true\n")

	res := runSyncConfigCheck(t)
	if res.Status != doctor.StatusFail {
		t.Fatalf("status = %s (%s), want fail", res.Status, res.Message)
	}
	whole := res.Message + " " + res.Error + " " + res.Resolution
	if strings.Contains(whole, "hunter2") {
		t.Fatalf("the check leaked the secret: %q", whole)
	}
	if !strings.Contains(res.Error, "exit status 3") {
		t.Errorf("the failure does not say what happened: %q", res.Error)
	}
}

// A complete config passes, and says which three facts it verified.
func TestSyncConfigComplete_CompleteConfigPasses(t *testing.T) {
	groveDir := setupScratchConfig(t)
	write(t, filepath.Join(groveDir, "sync.toml"),
		"server = \"https://sync.example.com\"\ntoken_command = \"printf tok\"\n\n"+
			"[[workspaces]]\nname = \"registry\"\nrole = \"registry\"\npull = true\n")

	res := runSyncConfigCheck(t)
	if res.Status != doctor.StatusOK {
		t.Fatalf("status = %s (%s / %s), want ok", res.Status, res.Message, res.Error)
	}
}

// A server with no subscriptions is a STAGED config, not a broken one: nothing
// is silently failing to replicate, because nothing is subscribed.
func TestSyncConfigComplete_NoWorkspacesIsOK(t *testing.T) {
	groveDir := setupScratchConfig(t)
	write(t, filepath.Join(groveDir, "sync.toml"), "server = \"https://sync.example.com\"\n")

	res := runSyncConfigCheck(t)
	if res.Status != doctor.StatusOK {
		t.Fatalf("status = %s (%s), want ok", res.Status, res.Message)
	}
}
