package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureGroveLinkCreatesAndIsIdempotent covers the install/onboard path:
// the hub link is created at the STABLE toolchain path, and running again
// changes nothing.
func TestEnsureGroveLinkCreatesAndIsIdempotent(t *testing.T) {
	dir, _, groveBin := exposeSandbox(t)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	link, created, err := ensureGroveLink()
	if err != nil {
		t.Fatalf("ensureGroveLink: %v", err)
	}
	if !created {
		t.Error("first ensureGroveLink reported no change")
	}
	if link != filepath.Join(dir, "grove") {
		t.Errorf("link = %q, want %q", link, filepath.Join(dir, "grove"))
	}
	dest, ok := linkDestination(link)
	if !ok || dest != groveBin {
		t.Fatalf("link target = %q (symlink=%v), want %q", dest, ok, groveBin)
	}

	_, created, err = ensureGroveLink()
	if err != nil {
		t.Fatalf("second ensureGroveLink: %v", err)
	}
	if created {
		t.Error("second ensureGroveLink relinked an already-correct link")
	}
}

// TestEnsureGroveLinkRefusesForeignFile pins the refusal-first rule:
// ~/.local/bin is the user's directory, so a real file named grove is reported,
// never replaced.
func TestEnsureGroveLinkRefusesForeignFile(t *testing.T) {
	dir, _, _ := exposeSandbox(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "grove")
	if err := os.WriteFile(foreign, []byte("someone else's grove\n"), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}

	_, created, err := ensureGroveLink()
	if err == nil {
		t.Fatal("ensureGroveLink clobbered a foreign file, want a refusal")
	}
	if created {
		t.Error("ensureGroveLink reported a change it refused to make")
	}
	data, readErr := os.ReadFile(foreign)
	if readErr != nil || !strings.Contains(string(data), "someone else's grove") {
		t.Errorf("foreign file was modified: %q (%v)", string(data), readErr)
	}
}

// TestGroveReachableViaLinkOffPath checks the reachability probe used by every
// PATH nag: a hub link counts even when its directory is not on THIS shell's
// PATH (a fresh install whose shell has not been restarted is set up right).
func TestGroveReachableViaLinkOffPath(t *testing.T) {
	dir, _, _ := exposeSandbox(t)
	t.Setenv("PATH", t.TempDir())

	if _, ok := groveReachable(); ok {
		t.Fatal("groveReachable said yes with no link and no grove on PATH")
	}
	if _, _, err := ensureGroveLink(); err != nil {
		t.Fatal(err)
	}
	path, ok := groveReachable()
	if !ok {
		t.Fatal("groveReachable said no with the hub link in place")
	}
	if path != filepath.Join(dir, "grove") {
		t.Errorf("groveReachable = %q, want the hub link", path)
	}
}
