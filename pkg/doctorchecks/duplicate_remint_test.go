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

// duplicateAcrossNotebooks builds the same duplicate, but with the two roots in
// two different recorded notebooks — the shape where a notebook name DOES
// disambiguate which copy a binding followed.
func duplicateAcrossNotebooks(t *testing.T, machineTOML string) (configDir, keeper, loser string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	configDir = filepath.Join(home, "config", "grove")
	nb, nb2 := filepath.Join(home, "notebook"), filepath.Join(home, "notebook2")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	notebooks := "default=\"nb\"\n[notebooks.nb]\nroot=\"" + nb + "\"\n[notebooks.nb2]\nroot=\"" + nb2 + "\"\n"
	if err := os.WriteFile(filepath.Join(configDir, "notebooks.toml"), []byte(notebooks), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "roots.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for i, root := range []string{nb, nb2} {
		if _, err := notespace.InstallNotebook(root, notespace.NotebookStamp{ID: "01ABCDEFGHJKMNPQRSTVWXYZ0" + string(rune('1'+i)), Name: filepath.Base(root)}); err != nil {
			t.Fatal(err)
		}
	}
	keeper = filepath.Join(nb, "notespaces", "one")
	loser = filepath.Join(nb2, "notespaces", "two")
	for name, root := range map[string]string{"one": keeper, "two": loser} {
		if _, err := notespace.InstallNotespace(root, notespace.NotespaceStamp{ID: dupID, Name: name, Subject: dupSubject, Kind: "notes"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(configDir, "machine.toml"), []byte(machineTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()
	t.Cleanup(config.ResetLoadCache)
	return configDir, keeper, loser
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

// F2 of the W3.6 review: when BOTH copies live in one notebook — the `cp -R`
// inside notespaces/ that is the D8 case, and the only duplicate shape the
// daemon detects — the notebook comparison is satisfied by both roots, so it
// says nothing about which copy the binding meant. The binding is left, and the
// ambiguity is named. Rewriting it would point the machine at an id the server
// has never seen, at the copy the operator just designated as the loser.
func TestRemintLeavesTheRegistryBindingWhenBothCopiesShareANotebook(t *testing.T) {
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
	if strings.Contains(string(data), result.NewID) {
		t.Fatalf("[sync.registry] was re-pointed at the designated LOSER by a notebook-name guess:\n%s", data)
	}
	if len(result.Rewritten) != 0 {
		t.Fatalf("Rewritten = %+v, want nothing rewritten on an ambiguous binding", result.Rewritten)
	}
	joined := strings.Join(result.Left, " ")
	if !strings.Contains(joined, "[sync.registry]") || !strings.Contains(joined, "both copies are in notebook") {
		t.Fatalf("the ambiguity was not named in the evidence: %+v", result.Left)
	}
	// And the binding still resolves: the id survives at the keeper.
	config.ResetLoadCache()
	cfg, err := config.LoadMachineConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sync.Registry.NotespaceID != dupID {
		t.Fatalf("[sync.registry] = %+v, want the surviving id %s", cfg.Sync.Registry, dupID)
	}
}

// The other half of the same rule: a notebook whose ONLY claimant of the id is
// the re-minted root does say which copy the binding meant, so it is repaired.
func TestRemintRewritesTheRegistryBindingOfTheSoleClaimantNotebook(t *testing.T) {
	configDir, _, loser := duplicateAcrossNotebooks(t, primariesOnly()+
		"[sync.registry]\nnotebook = \"nb2\"\nnotespace_id = \""+dupID+"\"\n")

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

// F9 of the same review: the machine.toml writer validates the WHOLE table, so
// an unrelated unreachable binding fails the repair — and discovering that
// after the mint leaves the stamp rewritten and the bindings not. The run is
// refused before a byte of the stamp is written.
func TestRemintPreflightsBindingValidationBeforeRewritingTheStamp(t *testing.T) {
	_, _, loser := duplicateAcrossNotebooks(t, primariesOnly()+
		"\"local:01ABCDEFGHJKMNPQRSTVWXYZ07\"=\"01ABCDEFGHJKMNPQRSTVWXYZ09\"\n"+
		"[sync.registry]\nnotebook = \"nb2\"\nnotespace_id = \""+dupID+"\"\n")

	var out bytes.Buffer
	if _, err := RemintDesignatedDuplicate(loser, &out); err == nil {
		t.Fatal("a re-mint whose binding repair could not succeed was allowed to rewrite the stamp")
	} else if !strings.Contains(err.Error(), "01ABCDEFGHJKMNPQRSTVWXYZ09") {
		t.Fatalf("the refusal does not name the binding that blocks it: %v", err)
	}
	stamp, err := notespace.LoadNotespace(loser)
	if err != nil || stamp.ID != dupID {
		t.Fatalf("the refused run left a re-minted stamp behind: %+v, %v", stamp, err)
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
