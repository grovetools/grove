package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/machine"
	"github.com/grovetools/core/pkg/registry"
)

// sandboxRegistry builds a hermetic machine with a registry subscription and a
// materialized replica, and returns the registry workspace root.
//
// GROVE_HOME redirects config AND state, so nothing here can reach the
// developer's real ~/.config/grove or ~/.local/state/grove — including the rev
// cache that ReadMachines writes.
func sandboxRegistry(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	config.ResetLoadCache()
	t.Cleanup(config.ResetLoadCache)

	configDir := filepath.Join(home, "config", "grove")
	notebookRoot := filepath.Join(home, "notebooks", "nb")
	root := filepath.Join(notebookRoot, "notespaces", "registry")
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(configDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("grove.toml", "name = \"fixture\"\n\n[notebooks.definitions.nb]\nroot_dir = \""+notebookRoot+"\"\n[notebooks.rules]\ndefault = \"nb\"\n")
	write("sync.toml", "server = \"http://127.0.0.1:1\"\n\n[[workspaces]]\nname = \"registry\"\nrole = \"registry\"\npull = true\n")
	return root
}

func writeMachineNote(t *testing.T, root string, n *registry.Note) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(registry.NotePath(n.MachineID)))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, n.Render(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runMachinesCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newMachinesCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	return out.String(), err
}

func TestMachinesListsPeersWithStalenessAndMissingBadges(t *testing.T) {
	root := sandboxRegistry(t)
	writeMachineNote(t, root, &registry.Note{
		MachineID: "01PEERAAAAAAAAAAAAAAAAAAAA",
		Name:      "solm4",
		Rev:       4,
		LastSeen:  "2020-01-01", // deliberately ancient
		OriginID:  "peer-origin",
		Ecosystems: []registry.NoteEcosystem{
			{Name: "grovetools", Path: "/code/grovetools", State: registry.StateDeclaredMissing, Enabled: true},
			{Name: "nb", Path: "/code/nb", State: registry.StatePresent, Enabled: true},
		},
	})

	out, err := runMachinesCmd(t)
	if err != nil {
		t.Fatalf("grove machines: %v (%s)", err, out)
	}
	// "name (short id)" — never the bare name, which collides across hosts
	// restored from one dotfiles repo.
	if !strings.Contains(out, machine.Describe("solm4", "01PEERAAAAAAAAAAAAAAAAAAAA")) {
		t.Errorf("row is not labelled name (short id):\n%s", out)
	}
	for _, want := range []string{
		"days ago (2020-01-01)",
		"1 present, 1 declared but missing",
		"grove ecosystem materialize grovetools",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// The own note is noise in a fleet view (you already know about yourself) and
// is hidden unless asked for.
func TestMachinesHidesSelfUnlessAsked(t *testing.T) {
	root := sandboxRegistry(t)
	if out, err := runMachineCmd(t, "init", "--name", "here"); err != nil {
		t.Fatalf("machine init: %v (%s)", err, out)
	}
	self, _ := machine.Load()
	writeMachineNote(t, root, &registry.Note{
		MachineID: self.ID, Name: "here", Rev: 1, LastSeen: registry.Today(timeNowUTC()),
	})

	out, err := runMachinesCmd(t)
	if err != nil {
		t.Fatalf("grove machines: %v (%s)", err, out)
	}
	if strings.Contains(out, "(this machine)") {
		t.Errorf("own note shown without --all:\n%s", out)
	}
	if !strings.Contains(out, "--all") {
		t.Errorf("output does not mention how to see the own note:\n%s", out)
	}

	out, err = runMachinesCmd(t, "--all")
	if err != nil {
		t.Fatalf("grove machines --all: %v (%s)", err, out)
	}
	if !strings.Contains(out, "(this machine)") {
		t.Errorf("--all did not show the own note:\n%s", out)
	}
}

// A listing must not mint state as a side effect of being run.
func TestMachinesDoesNotMintAnIdentity(t *testing.T) {
	sandboxRegistry(t)
	if out, err := runMachinesCmd(t); err != nil {
		t.Fatalf("grove machines: %v (%s)", err, out)
	}
	if _, err := os.Stat(machine.IdentityPath()); err == nil {
		t.Error("grove machines minted a machine identity")
	}
}

func TestMachinesExplainsAnUnconfiguredRegistry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	config.ResetLoadCache()
	t.Cleanup(config.ResetLoadCache)

	out, err := runMachinesCmd(t)
	if err != nil {
		t.Fatalf("grove machines: %v (%s)", err, out)
	}
	if !strings.Contains(out, "grove join") {
		t.Errorf("no registry should point at join:\n%s", out)
	}
}

// Read-side validation surfaces a note whose document disagrees with its path.
func TestMachinesFlagsTamperedNotes(t *testing.T) {
	root := sandboxRegistry(t)
	n := &registry.Note{MachineID: "01LIAR", Name: "impostor", Rev: 1, LastSeen: "2026-08-02"}
	p := filepath.Join(root, registry.MachinesDir, "01VICTIMAAAAAAAAAAAAAAAAAA.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, n.Render(), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runMachinesCmd(t)
	if err != nil {
		t.Fatalf("grove machines: %v (%s)", err, out)
	}
	if !strings.Contains(out, "tampered") {
		t.Errorf("tampered note not flagged:\n%s", out)
	}
	if !strings.Contains(out, "advisory") {
		t.Errorf("the interim trust model is not stated:\n%s", out)
	}
}

func TestMachineRetireDeletesAPeersNote(t *testing.T) {
	root := sandboxRegistry(t)
	const peer = "01PEERAAAAAAAAAAAAAAAAAAAA"
	writeMachineNote(t, root, &registry.Note{
		MachineID: peer, Name: "gone", Rev: 2, LastSeen: "2020-01-01",
	})
	notePath := filepath.Join(root, filepath.FromSlash(registry.NotePath(peer)))

	out, err := runMachineCmd(t, "retire", peer, "--yes")
	if err != nil {
		t.Fatalf("machine retire: %v (%s)", err, out)
	}
	if _, err := os.Stat(notePath); !os.IsNotExist(err) {
		t.Errorf("note not deleted: %v", err)
	}
	if !strings.Contains(out, machine.Describe("gone", peer)) {
		t.Errorf("retire did not name the machine it removed:\n%s", out)
	}

	// Retiring a machine with no note is an error, not a silent success.
	if _, err := runMachineCmd(t, "retire", peer, "--yes"); err == nil {
		t.Error("retiring an absent note succeeded")
	}
}

// Retiring your own note is a no-op the local daemon undoes on its next pass,
// so it is refused rather than performed.
func TestMachineRetireRefusesSelf(t *testing.T) {
	sandboxRegistry(t)
	if out, err := runMachineCmd(t, "init", "--name", "here"); err != nil {
		t.Fatalf("machine init: %v (%s)", err, out)
	}
	self, _ := machine.Load()

	out, err := runMachineCmd(t, "retire", self.ID, "--yes")
	if err == nil {
		t.Fatalf("retiring self succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), "THIS machine") {
		t.Errorf("unhelpful refusal: %v", err)
	}
}

// timeNowUTC keeps the "today" fixture readable at the call site.
func timeNowUTC() time.Time { return time.Now().UTC() }
