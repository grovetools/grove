package doctorchecks

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/doctor"
	"github.com/grovetools/core/pkg/notespace"
)

const (
	dupID      = "01ABCDEFGHJKMNPQRSTVWXYZ02"
	dupSubject = "local:01ABCDEFGHJKMNPQRSTVWXYZ03"
)

// duplicateMachine builds a sandboxed machine recording one notebook whose
// notespaces/ holds two roots carrying one stamp id — the D8 state — and
// returns the config dir plus both roots.
func duplicateMachine(t *testing.T, machineTOML string) (configDir, keeper, loser string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	configDir = filepath.Join(home, "config", "grove")
	nb := filepath.Join(home, "notebook")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "notebooks.toml"), []byte("default=\"nb\"\n[notebooks.nb]\nroot=\""+nb+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "roots.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := notespace.InstallNotebook(nb, notespace.NotebookStamp{ID: "01ABCDEFGHJKMNPQRSTVWXYZ01", Name: "nb"}); err != nil {
		t.Fatal(err)
	}
	stamp := notespace.NotespaceStamp{ID: dupID, Name: "one", Subject: dupSubject, Kind: "notes"}
	for _, name := range []string{"one", "two"} {
		root := filepath.Join(nb, "notespaces", name)
		stamp.Name = name
		if _, err := notespace.InstallNotespace(root, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(configDir, "machine.toml"), []byte(machineTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()
	t.Cleanup(config.ResetLoadCache)
	return configDir, filepath.Join(nb, "notespaces", "one"), filepath.Join(nb, "notespaces", "two")
}

func primariesOnly() string {
	return "[primaries]\n\"" + dupSubject + "\"=\"" + dupID + "\"\n"
}

// The whole point of the flow: the operator designates a root, that root gets
// a new identity, the other keeps the id and its history, and doctor goes green.
func TestRemintDesignatedDuplicateSeparatesTheTwoRootsAndClearsTheCheck(t *testing.T) {
	_, keeper, loser := duplicateMachine(t, primariesOnly())

	before := (&notespaceIdentityCheck{}).Run(context.Background(), doctor.RunOptions{})
	if before.Status != doctor.StatusFail {
		t.Fatalf("the fixture is not the duplicate state: %+v", before)
	}

	var out bytes.Buffer
	result, err := RemintDesignatedDuplicate(loser, &out)
	if err != nil {
		t.Fatal(err)
	}
	if result.OldID != dupID || result.NewID == dupID {
		t.Fatalf("result = %+v, want a new id in place of %s", result, dupID)
	}
	keeperStamp, err := notespace.LoadNotespace(keeper)
	if err != nil || keeperStamp.ID != dupID {
		t.Fatalf("the undesignated copy lost its id: %+v, %v", keeperStamp, err)
	}
	loserStamp, err := notespace.LoadNotespace(loser)
	if err != nil || loserStamp.ID != result.NewID {
		t.Fatalf("the designated copy = %+v, %v", loserStamp, err)
	}
	// Mutable metadata is carried over: the copy is still notes about the
	// same subject, under its own directory name.
	if loserStamp.Subject != dupSubject || loserStamp.Name != "two" {
		t.Fatalf("re-mint altered mutable metadata: %+v", loserStamp)
	}

	config.ResetLoadCache()
	after := (&notespaceIdentityCheck{}).Run(context.Background(), doctor.RunOptions{})
	if after.Status == doctor.StatusFail && strings.Contains(after.Error, "physical roots") {
		t.Fatalf("the duplicate survived the repair: %+v", after)
	}
	evidence := out.String()
	for _, want := range []string{"duplicate", "keeps id", "re-minting", "new id", keeper, loser} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("evidence is missing %q:\n%s", want, evidence)
		}
	}
}

// A primary names an id, not a path. The id survives at the copy that was not
// designated, so the pointer is still true — rewriting it would be a guess.
func TestRemintLeavesAPrimaryThatStillResolves(t *testing.T) {
	configDir, _, loser := duplicateMachine(t, primariesOnly())

	var out bytes.Buffer
	result, err := RemintDesignatedDuplicate(loser, &out)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(configDir, "machine.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), dupID) || strings.Contains(string(data), result.NewID) {
		t.Fatalf("[primaries] was rewritten to follow the re-minted copy:\n%s", data)
	}
	if len(result.Left) == 0 || !strings.Contains(strings.Join(result.Left, " "), "[primaries]") {
		t.Fatalf("the untouched primary was not reported as evidence: %+v", result.Left)
	}
}

// The registry binding names a NOTEBOOK as well as an id, so it can be checked
// against the re-minted root — and when it followed that root, it is repaired.
func TestRemintRewritesTheRegistryBindingThatFollowedTheRoot(t *testing.T) {
	configDir, _, loser := duplicateMachine(t, primariesOnly()+
		"[sync.registry]\nnotebook = \"nb\"\nnotespace_id = \""+dupID+"\"\n")

	var out bytes.Buffer
	result, err := RemintDesignatedDuplicate(loser, &out)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(configDir, "machine.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "notespace_id = \""+result.NewID+"\"") {
		t.Fatalf("[sync.registry] was not repaired:\n%s", data)
	}
	if len(result.Rewritten) != 1 || !strings.Contains(result.Rewritten[0], "[sync.registry]") {
		t.Fatalf("Rewritten = %+v, want the registry binding", result.Rewritten)
	}
	// And the machine config still parses, with the primary untouched.
	config.ResetLoadCache()
	cfg, err := config.LoadMachineConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sync.Registry.NotespaceID != result.NewID || cfg.Primaries[dupSubject] != dupID {
		t.Fatalf("bindings after repair = %+v / %+v", cfg.Sync.Registry, cfg.Primaries)
	}
}

// Nothing is inferred: a root whose id no other root claims is not a duplicate,
// and re-minting it would only discard history.
func TestRemintRefusesARootWithNoDuplicate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	dir := filepath.Join(home, "config", "grove")
	nb := filepath.Join(home, "notebook")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notebooks.toml"), []byte("default=\"nb\"\n[notebooks.nb]\nroot=\""+nb+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "roots.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(nb, "notespaces", "one")
	if _, err := notespace.InstallNotespace(root, notespace.NotespaceStamp{ID: dupID, Name: "one", Subject: dupSubject, Kind: "notes"}); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()
	t.Cleanup(config.ResetLoadCache)

	var out bytes.Buffer
	if _, err := RemintDesignatedDuplicate(root, &out); err == nil {
		t.Fatal("a unique id was re-minted")
	}
	stamp, err := notespace.LoadNotespace(root)
	if err != nil || stamp.ID != dupID {
		t.Fatalf("the refused root was rewritten anyway: %+v, %v", stamp, err)
	}
}

func TestRemintRefusesRootsOutsideRecordedTopology(t *testing.T) {
	_, _, loser := duplicateMachine(t, primariesOnly())
	var out bytes.Buffer

	if _, err := RemintDesignatedDuplicate("", &out); err == nil {
		t.Fatal("an empty designation was accepted")
	}
	if _, err := RemintDesignatedDuplicate(filepath.Join(t.TempDir(), "nowhere"), &out); err == nil {
		t.Fatal("a nonexistent root was accepted")
	}
	unrecorded := t.TempDir()
	if _, err := notespace.InstallNotespace(unrecorded, notespace.NotespaceStamp{ID: dupID, Name: "stray", Subject: dupSubject, Kind: "notes"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RemintDesignatedDuplicate(unrecorded, &out); err == nil {
		t.Fatal("a root beneath no recorded notebook was accepted")
	}
	// The designated-but-refused runs left the real duplicate untouched.
	stamp, err := notespace.LoadNotespace(loser)
	if err != nil || stamp.ID != dupID {
		t.Fatalf("a refused run rewrote the duplicate: %+v, %v", stamp, err)
	}
}
