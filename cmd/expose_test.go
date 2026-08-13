package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exposeSandbox gives a test its own expose dir, toolchain bin dir (with a fake
// grove binary in it) and state dir, so nothing touches the real ~/.local/bin.
func exposeSandbox(t *testing.T) (exposeDirPath, binDir, groveBin string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GROVE_HOME", filepath.Join(home, "grove"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	binDir = filepath.Join(home, "toolchain", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GROVE_BIN", binDir)

	groveBin = filepath.Join(binDir, "grove")
	if err := os.WriteFile(groveBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}

	exposeDirPath = filepath.Join(home, ".local", "bin")
	t.Setenv(exposeDirEnv, exposeDirPath)
	t.Setenv("PATH", exposeDirPath+string(os.PathListSeparator)+binDir)
	return exposeDirPath, binDir, groveBin
}

func TestExposeCreatesSymlinkAtStableTarget(t *testing.T) {
	dir, _, groveBin := exposeSandbox(t)

	if err := runExpose("cx", "", false); err != nil {
		t.Fatalf("expose cx: %v", err)
	}

	link := filepath.Join(dir, "cx")
	dest, ok := linkDestination(link)
	if !ok {
		t.Fatalf("%s is not a symlink", link)
	}
	// The stable BinDir path, not this test binary: exposures must survive
	// upgrades, and only the reconciled path does.
	if dest != groveBin {
		t.Errorf("link target = %q, want %q", dest, groveBin)
	}
	if _, isGrove := pointsAtGrove(link); !isGrove {
		t.Error("pointsAtGrove said no about a link grove just made")
	}
	if got := loadExposures()["cx"]; got != "cx" {
		t.Errorf("ledger recorded %q for cx, want %q", got, "cx")
	}
}

// The alias case: `grove expose daemon` must produce `groved`, the binary's
// real name, not the repo name the user typed.
func TestExposeUsesRegistryAlias(t *testing.T) {
	dir, _, _ := exposeSandbox(t)

	if err := runExpose("daemon", "", false); err != nil {
		t.Fatalf("expose daemon: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "groved")); err != nil {
		t.Fatalf("expected a link named groved: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "daemon")); err == nil {
		t.Error("exposed the repo name instead of the binary alias")
	}
}

func TestExposeRefusesUnknownTool(t *testing.T) {
	exposeSandbox(t)
	err := runExpose("definitely-not-a-grove-tool", "", false)
	if err == nil {
		t.Fatal("expected a refusal for an unregistered tool")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("error = %q, want it to say the tool is unknown", err)
	}
}

func TestExposeIsIdempotent(t *testing.T) {
	dir, _, _ := exposeSandbox(t)

	if err := runExpose("cx", "", false); err != nil {
		t.Fatalf("first expose: %v", err)
	}
	if err := runExpose("cx", "", false); err != nil {
		t.Fatalf("second expose: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "cx")); err != nil {
		t.Fatalf("link vanished: %v", err)
	}
}

// A foreign file in ~/.local/bin is never clobbered — not even with --force.
// Only foreign PATH entries ELSEWHERE are force-able.
func TestExposeRefusesToClobberForeignFile(t *testing.T) {
	dir, _, _ := exposeSandbox(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "cx")
	if err := os.WriteFile(foreign, []byte("not grove"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}

	for _, force := range []bool{false, true} {
		err := runExpose("cx", "", force)
		if err == nil {
			t.Fatalf("force=%v: expected a refusal to clobber a foreign file", force)
		}
		if !strings.Contains(err.Error(), "grove did not create it") {
			t.Errorf("force=%v: error = %q", force, err)
		}
	}
	data, err := os.ReadFile(foreign)
	if err != nil || string(data) != "not grove" {
		t.Errorf("foreign file was modified: %v %q", err, data)
	}
}

func TestExposeCollisionRefusalAndForce(t *testing.T) {
	dir, _, _ := exposeSandbox(t)

	// A foreign `nb` somewhere else on PATH — the classic clash.
	otherDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(otherDir, "nb"), []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+otherDir)

	err := runExpose("nb", "", false)
	if err == nil {
		t.Fatal("expected a collision refusal")
	}
	if !strings.Contains(err.Error(), filepath.Join(otherDir, "nb")) {
		t.Errorf("error %q does not name the conflicting path", err)
	}
	if !strings.Contains(err.Error(), "--as") {
		t.Errorf("error %q does not suggest --as", err)
	}
	if _, statErr := os.Lstat(filepath.Join(dir, "nb")); statErr == nil {
		t.Error("refused exposure still created a link")
	}

	// --as sidesteps it.
	if err := runExpose("nb", "gnb", false); err != nil {
		t.Fatalf("expose nb --as gnb: %v", err)
	}
	if got := loadExposures()["gnb"]; got != "nb" {
		t.Errorf("ledger recorded %q for gnb, want nb", got)
	}

	// --force takes the name anyway.
	if err := runExpose("nb", "", true); err != nil {
		t.Fatalf("expose nb --force: %v", err)
	}
	if _, isGrove := pointsAtGrove(filepath.Join(dir, "nb")); !isGrove {
		t.Error("--force did not create a grove exposure")
	}
}

func TestHideRemovesOnlyGroveLinks(t *testing.T) {
	dir, _, _ := exposeSandbox(t)

	if err := runExpose("cx", "", false); err != nil {
		t.Fatalf("expose: %v", err)
	}
	if err := runHide("cx"); err != nil {
		t.Fatalf("hide cx: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "cx")); err == nil {
		t.Fatal("hide left the link behind")
	}
	if _, ok := loadExposures()["cx"]; ok {
		t.Error("hide left a ledger entry behind")
	}

	// A real binary: refused.
	real := filepath.Join(dir, "tend")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	if err := runHide("tend"); err == nil {
		t.Error("hide removed a real binary")
	}
	if _, err := os.Lstat(real); err != nil {
		t.Errorf("hide deleted a file it should have refused: %v", err)
	}

	// A symlink to something that is not grove: refused, and named.
	elsewhere := filepath.Join(t.TempDir(), "flow")
	if err := os.WriteFile(elsewhere, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	foreignLink := filepath.Join(dir, "flow")
	if err := os.Symlink(elsewhere, foreignLink); err != nil {
		t.Fatal(err)
	}
	err := runHide("flow")
	if err == nil {
		t.Fatal("hide removed a foreign symlink")
	}
	if !strings.Contains(err.Error(), elsewhere) {
		t.Errorf("error %q does not name the real target", err)
	}
	if _, statErr := os.Lstat(foreignLink); statErr != nil {
		t.Error("hide deleted a foreign symlink it should have refused")
	}
}

func TestHideOnMissingName(t *testing.T) {
	exposeSandbox(t)
	if err := runHide("cx"); err == nil {
		t.Fatal("expected an error hiding a name that was never exposed")
	}
}

func TestProbePATHCollision(t *testing.T) {
	sep := string(os.PathListSeparator)

	exposeDirPath := t.TempDir()
	binDir := t.TempDir()
	foreignDir := t.TempDir()
	emptyDir := t.TempDir()

	write := func(dir, name string, mode os.FileMode) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// grove's own copies must never read as collisions.
	write(exposeDirPath, "cx", 0o755)
	write(binDir, "cx", 0o755)
	foreignNB := write(foreignDir, "nb", 0o755)
	write(foreignDir, "notexec", 0o644)
	if err := os.Mkdir(filepath.Join(foreignDir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}

	pathEnv := strings.Join([]string{exposeDirPath, binDir, foreignDir, emptyDir}, sep)
	skip := []string{exposeDirPath, binDir}

	tests := []struct {
		name, probe, want string
	}{
		{"grove's own dirs are not collisions", "cx", ""},
		{"a foreign executable collides", "nb", foreignNB},
		{"a non-executable file is not a collision", "notexec", ""},
		{"a directory is not a collision", "adir", ""},
		{"an unused name is free", "tend", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := probePATHCollision(tt.probe, pathEnv, skip); got != tt.want {
				t.Errorf("probePATHCollision(%q) = %q, want %q", tt.probe, got, tt.want)
			}
		})
	}

	// With nothing skipped, grove's own dir is reported — that is what makes
	// the skip list load-bearing rather than decorative.
	if got := probePATHCollision("cx", pathEnv, nil); got != filepath.Join(exposeDirPath, "cx") {
		t.Errorf("unskipped probe = %q, want the expose dir copy", got)
	}
}

func TestValidExposeName(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", "/abs"} {
		if err := validExposeName(bad); err == nil {
			t.Errorf("validExposeName(%q) accepted an invalid name", bad)
		}
	}
	if err := validExposeName("gnb"); err != nil {
		t.Errorf("validExposeName(gnb) = %v, want nil", err)
	}
}

func TestExposeListSkipsForeignEntries(t *testing.T) {
	dir, _, _ := exposeSandbox(t)
	if err := runExpose("cx", "", false); err != nil {
		t.Fatal(err)
	}
	if err := runExpose("nb", "gnb", false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "someone-elses-tool"), []byte("x"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}

	exposures := loadExposures()
	if got := exposedToolFor("cx", exposures); got != "cx" {
		t.Errorf("exposedToolFor(cx) = %q, want cx", got)
	}
	if got := exposedToolFor("gnb", exposures); got != "nb" {
		t.Errorf("exposedToolFor(gnb) = %q, want nb", got)
	}
	// A name grove links but cannot map dispatches as plain grove.
	if got := exposedToolFor("mystery", exposures); got != "grove" {
		t.Errorf("exposedToolFor(mystery) = %q, want grove", got)
	}

	if err := runExposeList(); err != nil {
		t.Fatalf("runExposeList: %v", err)
	}
}
