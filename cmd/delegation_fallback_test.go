package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sandboxHome points every grove path at a throwaway home so resolution sees an
// empty toolchain dir and an empty state dir.
func sandboxHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GROVE_HOME", "")
	t.Setenv("GROVE_BIN", "")
	return home
}

// TestResolveToolNeverFallsBackToPath pins the hub model's delegation boundary:
// grove is the only name the install puts in the user's namespace, so a
// same-named binary found on PATH belongs to somebody else. An uninstalled tool
// must refuse rather than silently run it.
func TestResolveToolNeverFallsBackToPath(t *testing.T) {
	sandboxHome(t)

	binDir := t.TempDir()
	fake := filepath.Join(binDir, "git-viewer")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	res, ok, err := resolveTool("git-viewer")
	if err != nil {
		t.Fatalf("resolveTool returned an error: %v", err)
	}
	if ok {
		t.Fatalf("resolveTool resolved an uninstalled tool to %q; PATH fallback must be gone", res.Path)
	}
}

// TestDelegateToUninstalledRegisteredToolNamesTheInstall checks the wording the
// removed fallback made necessary: the user is told the tool is not installed
// and how to install it.
func TestDelegateToUninstalledRegisteredToolNamesTheInstall(t *testing.T) {
	sandboxHome(t)
	t.Setenv("PATH", t.TempDir())

	err := delegateToTool("git-viewer", nil)
	if err == nil {
		t.Fatal("delegating to an uninstalled tool succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "not installed") || !strings.Contains(err.Error(), "grove install git-viewer") {
		t.Fatalf("error = %q, want it to name the install command", err.Error())
	}
}

// TestDelegateToUnknownNameStillRefuses guards the other half of the boundary:
// an unregistered name is not a tool at all, whatever is on PATH.
func TestDelegateToUnknownNameStillRefuses(t *testing.T) {
	sandboxHome(t)

	binDir := t.TempDir()
	fake := filepath.Join(binDir, "definitely-not-a-grove-tool")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	err := delegateToTool("definitely-not-a-grove-tool", nil)
	if err == nil {
		t.Fatal("delegating to an unregistered name succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("error = %q, want an unknown-tool refusal", err.Error())
	}
}
