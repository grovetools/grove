package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/registry"
)

// gitFixture runs a git command in dir, failing the test on error. Materialize
// is a git verb; testing it against a real local repository is the only way the
// clone, the submodule pass and the convergence check mean anything.
func gitFixture(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=grove-test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=grove-test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

// sourceEcosystem builds a real, local git repository that IS an ecosystem: a
// grove manifest with a non-empty workspaces list plus an [ecosystem] card
// declaring the superrepo layout and pointing its origin remote at itself.
func sourceEcosystem(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, "source", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, dir, "init", "-b", "main")

	manifest := filepath.Join(dir, "grove.toml")
	if err := os.WriteFile(manifest, []byte("name = \""+name+"\"\nworkspaces = [\"*\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.WriteEcosystemCard(manifest, config.EcosystemCard{ID: "01ECOSYSTEMAAAAAAAAAAAAAAA"}); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, dir, "add", ".")
	gitFixture(t, dir, "commit", "-m", "ecosystem")
	return dir
}

func runMaterialize(t *testing.T, in string, opts materializeOptions) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runEcosystemMaterialize(context.Background(), strings.NewReader(in), &out, opts)
	return out.String(), err
}

// TestMaterializeFromURLClonesAndConverges walks the --url path and then
// re-runs it: the second run must converge, not fail and not re-clone. That
// idempotence is what makes materialize a standing verb rather than a
// bootstrap script.
func TestMaterializeFromURLClonesAndConverges(t *testing.T) {
	home, configDir, _ := sandboxAdoption(t)
	source := sourceEcosystem(t, home, "grovetools")
	dest := filepath.Join(home, "code", "grovetools")

	opts := materializeOptions{
		name:           "grovetools",
		url:            source,
		path:           dest,
		noNotebookSync: true,
		assumeYes:      true,
	}
	out, err := runMaterialize(t, "", opts)
	if err != nil {
		t.Fatalf("materialize: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "grove.toml")); statErr != nil {
		t.Fatalf("nothing was cloned into %s: %v\n%s", dest, statErr, out)
	}
	if !strings.Contains(out, "is materialized at") {
		t.Errorf("materialize did not report success:\n%s", out)
	}

	table, err := coderoot.LoadFrom(filepath.Join(configDir, coderoot.RootsFileName), filepath.Join(configDir, coderoot.NotebooksFileName))
	if err != nil {
		t.Fatalf("subscription not written: %v", err)
	}
	if got := table.Roots["grovetools"].Path; got != dest {
		t.Errorf("subscription path = %q, want %q", got, dest)
	}

	// Second run: converge.
	out2, err := runMaterialize(t, "", opts)
	if err != nil {
		t.Fatalf("re-materialize: %v\n%s", err, out2)
	}
	if !strings.Contains(out2, "already declared") {
		t.Errorf("re-run rewrote the subscription:\n%s", out2)
	}
	if !strings.Contains(out2, "already holds a git checkout") {
		t.Errorf("re-run did not recognise the existing checkout:\n%s", out2)
	}
}

// TestMaterializeRepairsFromTheLocalCard: an ecosystem already on disk needs
// no registry and no --url — its own manifest is the authority, which is what
// makes a half-finished materialization repairable.
func TestMaterializeRepairsFromTheLocalCard(t *testing.T) {
	home, _, _ := sandboxAdoption(t)
	source := sourceEcosystem(t, home, "grovetools")
	dest := filepath.Join(home, "code", "grovetools")

	// Simulate an interrupted run: the clone happened, nothing else did.
	gitFixture(t, home, "clone", source, dest)

	out, err := runMaterialize(t, "", materializeOptions{
		name:           "grovetools",
		path:           dest,
		noNotebookSync: true,
	})
	if err != nil {
		t.Fatalf("repair: %v\n%s", err, out)
	}
	if !strings.Contains(out, filepath.Join(dest, "grove.toml")) {
		t.Errorf("repair did not read the local card:\n%s", out)
	}
}

// TestMaterializeRefusesRegistryCardWithoutATerminal is the contract's hard
// guard: consuming a registry card means trusting a document any token can
// write, so an unattended run must refuse rather than clone.
func TestMaterializeRefusesRegistryCardWithoutATerminal(t *testing.T) {
	home, configDir, notebookRoot := sandboxAdoption(t)
	seedRegistryOffer(t, configDir, notebookRoot, "grovetools", filepath.Join(home, "source", "grovetools"))

	out, err := runMaterialize(t, "", materializeOptions{
		name:           "grovetools",
		path:           filepath.Join(home, "code", "grovetools"),
		noNotebookSync: true,
		// assumeYes deliberately absent, and `go test` has no terminal.
	})
	if err == nil {
		t.Fatalf("materialize consumed a registry card unattended:\n%s", out)
	}
	if !strings.Contains(err.Error(), "not confirmed") {
		t.Errorf("refusal did not name the missing confirmation: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "code", "grovetools")); !os.IsNotExist(statErr) {
		t.Errorf("something was created despite the refusal (%v)", statErr)
	}
	// The plan it refused to run must still have been SHOWN — a gate the user
	// cannot read is not a gate.
	if !strings.Contains(out, "Clone from:") || !strings.Contains(out, "integrity-ADVISORY") {
		t.Errorf("the confirmation gate did not show what would be cloned:\n%s", out)
	}
}

// TestMaterializeFromRegistryWithConsent: with --yes the same card is used, and
// the clone comes from the remote the gate displayed.
func TestMaterializeFromRegistryWithConsent(t *testing.T) {
	home, configDir, notebookRoot := sandboxAdoption(t)
	source := sourceEcosystem(t, home, "grovetools")
	seedRegistryOffer(t, configDir, notebookRoot, "grovetools", source)
	dest := filepath.Join(home, "code", "grovetools")

	out, err := runMaterialize(t, "", materializeOptions{
		name:           "grovetools",
		path:           dest,
		noNotebookSync: true,
		assumeYes:      true,
	})
	if err != nil {
		t.Fatalf("materialize: %v\n%s", err, out)
	}
	if !strings.Contains(out, "the machine registry") {
		t.Errorf("card was not attributed to the registry:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "grove.toml")); statErr != nil {
		t.Fatalf("nothing was cloned: %v\n%s", statErr, out)
	}
}

// TestMaterializeWithoutACardExplainsItself: no --url, nothing on disk, no
// registry — the error must name all three ways out rather than "not found".
func TestMaterializeWithoutACardExplainsItself(t *testing.T) {
	home, _, _ := sandboxAdoption(t)
	_, err := runMaterialize(t, "", materializeOptions{
		name:           "grovetools",
		path:           filepath.Join(home, "code", "grovetools"),
		noNotebookSync: true,
	})
	if err == nil {
		t.Fatal("materialize succeeded with no card available")
	}
	for _, want := range []string{"grove join", "--url"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// TestMaterializeWritesPeerNotebookSubscription: the notebook entry must be
// role=peer and pull-enabled. Pulling is legal here precisely because the role
// says this machine is mirroring its own notebook, not receiving a disposable
// VM's writes — the distinction the whole role model exists to draw.
func TestMaterializeWritesPeerNotebookSubscription(t *testing.T) {
	home, configDir, _ := sandboxAdoption(t)
	source := sourceEcosystem(t, home, "grovetools")
	if err := os.WriteFile(filepath.Join(configDir, syncConfigFileName),
		[]byte("server = \"http://127.0.0.1:1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runMaterialize(t, "", materializeOptions{
		name:      "grovetools",
		url:       source,
		path:      filepath.Join(home, "code", "grovetools"),
		assumeYes: true,
		waitFor:   0, // no daemon to wait for in a unit test
	})
	if err != nil {
		t.Fatalf("materialize: %v\n%s", err, out)
	}
	syncCfg, err := config.LoadSyncConfigFrom(filepath.Join(configDir, syncConfigFileName))
	if err != nil || syncCfg == nil {
		t.Fatalf("sync config unusable: %v (%v)", syncCfg, err)
	}
	var found *config.SyncWorkspace
	for i := range syncCfg.Workspaces {
		if syncCfg.Workspaces[i].Name == "grovetools" {
			found = &syncCfg.Workspaces[i]
		}
	}
	if found == nil {
		t.Fatalf("no notebook subscription was written: %+v", syncCfg.Workspaces)
	}
	if found.Role != config.SyncRolePeer || !found.Pull {
		t.Errorf("notebook entry is role=%q pull=%t, want role=peer pull=true", found.Role, found.Pull)
	}
}

// seedRegistryOffer writes a peer machine's presence note carrying an
// ecosystem card, plus the registry subscription that makes it locatable.
func seedRegistryOffer(t *testing.T, configDir, notebookRoot, eco, remoteURL string) {
	t.Helper()
	root := filepath.Join(notebookRoot, "notespaces", "registry")
	if err := os.MkdirAll(filepath.Join(root, "machines"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, syncConfigFileName),
		[]byte("server = \"http://127.0.0.1:1\"\n\n[[workspaces]]\nname = \"registry\"\nrole = \"registry\"\npull = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()

	note := &registry.Note{
		MachineID: "01PEERAAAAAAAAAAAAAAAAAAAA",
		Name:      "solm4",
		Rev:       1,
		LastSeen:  registry.Today(timeNowUTC()),
		Ecosystems: []registry.NoteEcosystem{{
			Name:    eco,
			Path:    "/Users/peer/code/" + eco,
			Enabled: true,
			State:   registry.StatePresent,
			Card:    &registry.NoteCard{ID: "01ECOSYSTEMAAAAAAAAAAAAAAA", Name: eco, Members: []registry.NoteMemberOrigin{{Path: ".", Origin: remoteURL}}},
		}},
	}
	p := filepath.Join(root, filepath.FromSlash(registry.NotePath(note.MachineID)))
	if err := os.WriteFile(p, note.Render(), 0o644); err != nil {
		t.Fatal(err)
	}
}
