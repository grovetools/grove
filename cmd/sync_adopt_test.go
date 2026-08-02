package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/models"
)

// seedAdoptableWorkspace makes a notebook workspace exist on disk and points
// sync.toml at a server, which is the precondition adopt checks before it
// writes anything.
func seedAdoptableWorkspace(t *testing.T, configDir, notebookRoot, name string) string {
	t.Helper()
	root := filepath.Join(notebookRoot, "workspaces", name)
	if err := os.MkdirAll(filepath.Join(root, "notes", "inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, syncConfigFileName),
		[]byte("server = \"http://127.0.0.1:1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()
	return root
}

func runAdopt(t *testing.T, opts syncAdoptOptions) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runSyncAdopt(context.Background(), &out, opts)
	return out.String(), err
}

// TestSyncAdoptWritesTheSubscription is the difference between the real verb
// and the stub it replaced: the stub walked the tree, hashed every file into a
// discard, and printed a fabricated count. This one writes a subscription and
// reports what the daemon says.
func TestSyncAdoptWritesTheSubscription(t *testing.T) {
	_, configDir, notebookRoot := sandboxAdoption(t)
	root := seedAdoptableWorkspace(t, configDir, notebookRoot, "grovetools")

	out, err := runAdopt(t, syncAdoptOptions{
		workspace: "grovetools",
		role:      config.SyncRolePeer,
		pull:      true,
		waitFor:   0, // no daemon in a unit test
	})
	if err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}
	if !strings.Contains(out, root) {
		t.Errorf("adopt did not report the resolved workspace root %s:\n%s", root, out)
	}

	syncCfg, err := config.LoadSyncConfigFrom(filepath.Join(configDir, syncConfigFileName))
	if err != nil || syncCfg == nil {
		t.Fatalf("sync config unusable: %v (%v)", syncCfg, err)
	}
	if len(syncCfg.Workspaces) != 1 {
		t.Fatalf("want one subscription, got %+v", syncCfg.Workspaces)
	}
	ws := syncCfg.Workspaces[0]
	if ws.Name != "grovetools" || ws.Role != config.SyncRolePeer || !ws.Pull {
		t.Errorf("subscription = %+v, want grovetools/peer/pull", ws)
	}
}

// TestSyncAdoptIsIdempotent: an existing entry is the user's, and is reported
// rather than rewritten.
func TestSyncAdoptIsIdempotent(t *testing.T) {
	_, configDir, notebookRoot := sandboxAdoption(t)
	seedAdoptableWorkspace(t, configDir, notebookRoot, "grovetools")
	opts := syncAdoptOptions{workspace: "grovetools", role: config.SyncRolePeer, pull: true, waitFor: 0}

	if out, err := runAdopt(t, opts); err != nil {
		t.Fatalf("first adopt: %v\n%s", err, out)
	}
	before, err := os.ReadFile(filepath.Join(configDir, syncConfigFileName))
	if err != nil {
		t.Fatal(err)
	}

	out, err := runAdopt(t, opts)
	if err != nil {
		t.Fatalf("second adopt: %v\n%s", err, out)
	}
	after, _ := os.ReadFile(filepath.Join(configDir, syncConfigFileName))
	if string(after) != string(before) {
		t.Errorf("second adopt rewrote sync.toml:\n%s", after)
	}
	if !strings.Contains(out, "already subscribed") {
		t.Errorf("second adopt did not report the entry as present:\n%s", out)
	}
}

// TestSyncAdoptRefusesPullUnderAPushOnlyRole: the role model's hard invariant,
// enforced at the single rendering choke point. A satellite entry that pulls
// would let a disposable VM overwrite local notebooks.
func TestSyncAdoptRefusesPullUnderAPushOnlyRole(t *testing.T) {
	_, configDir, notebookRoot := sandboxAdoption(t)
	seedAdoptableWorkspace(t, configDir, notebookRoot, "grovetools")

	_, err := runAdopt(t, syncAdoptOptions{
		workspace: "grovetools",
		role:      config.SyncRoleSatellite,
		pull:      true,
		waitFor:   0,
	})
	if err == nil || !strings.Contains(err.Error(), "PUSH-ONLY") {
		t.Fatalf("a pull-enabled satellite entry was accepted: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(configDir, syncConfigFileName)); strings.Contains(string(data), "grovetools") {
		t.Errorf("the refused entry was written anyway:\n%s", data)
	}
}

// TestSyncAdoptRejectsAnUnresolvableWorkspace: a typo must fail here, not
// become a subscription that silently syncs nothing.
func TestSyncAdoptRejectsAnUnresolvableWorkspace(t *testing.T) {
	_, configDir, notebookRoot := sandboxAdoption(t)
	seedAdoptableWorkspace(t, configDir, notebookRoot, "grovetools")

	_, err := runAdopt(t, syncAdoptOptions{workspace: "grovetoolz", role: config.SyncRolePeer, pull: true, waitFor: 0})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("a nonexistent workspace was adopted: %v", err)
	}
}

// TestSyncAdoptRequiresAServer: adopting into nowhere is a configuration
// mistake with an obvious fix, and the error says it.
func TestSyncAdoptRequiresAServer(t *testing.T) {
	_, _, _ = sandboxAdoption(t)
	_, err := runAdopt(t, syncAdoptOptions{workspace: "grovetools", waitFor: 0})
	if err == nil || !strings.Contains(err.Error(), "grove join") {
		t.Fatalf("adopt did not require a configured server: %v", err)
	}
}

// TestSyncAdoptWithoutADaemonSaysSo: the subscription is written either way —
// what fails is the reporting, and the message must say which.
func TestSyncAdoptWithoutADaemonSaysSo(t *testing.T) {
	_, configDir, notebookRoot := sandboxAdoption(t)
	seedAdoptableWorkspace(t, configDir, notebookRoot, "grovetools")

	_, err := runAdopt(t, syncAdoptOptions{
		workspace: "grovetools",
		role:      config.SyncRolePeer,
		pull:      true,
		waitFor:   50 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "no running daemon") {
		t.Fatalf("adopt did not report the missing daemon: %v", err)
	}
	syncCfg, _ := config.LoadSyncConfigFrom(filepath.Join(configDir, syncConfigFileName))
	if syncCfg == nil || len(syncCfg.Workspaces) != 1 {
		t.Errorf("the subscription was not written despite the daemon being absent: %+v", syncCfg)
	}
}

// TestAdoptionResultReportsRealCounters: every number printed comes from the
// daemon's own status payload. This is the property the stub violated by
// fabricating its summary from a local walk.
func TestAdoptionResultReportsRealCounters(t *testing.T) {
	var out bytes.Buffer
	reportAdoptionResult(&out, "grovetools", &models.SyncStatus{
		Documents:         42,
		DocumentsDiverged: 2,
		OutboxPending:     7,
		OutboxParked:      1,
		Workspaces: []models.SyncWorkspaceStatus{{
			Name:      "grovetools",
			Cursor:    19,
			Hydration: &models.SyncHydrationProgress{Workspace: "grovetools", Scanned: 100, Enqueued: 42, Quarantined: 3},
		}},
	})
	text := out.String()
	for _, want := range []string{"42", "7", "(parked 1)", "19", "100 scanned", "42 enqueued", "3 quarantined"} {
		if !strings.Contains(text, want) {
			t.Errorf("report is missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "Still draining") {
		t.Errorf("a pending outbox was reported as converged:\n%s", text)
	}
}

// TestAdoptionResultReportsConvergence: an empty outbox is the "done" signal.
func TestAdoptionResultReportsConvergence(t *testing.T) {
	var out bytes.Buffer
	reportAdoptionResult(&out, "grovetools", &models.SyncStatus{
		Documents:  42,
		Workspaces: []models.SyncWorkspaceStatus{{Name: "grovetools", Cursor: 19}},
	})
	if !strings.Contains(out.String(), "adopted and converged") {
		t.Errorf("a drained workspace was not reported as converged:\n%s", out.String())
	}
}
