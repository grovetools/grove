package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPathFallbackResolvesRegisteredToolFromPath pins the pilot's delegation
// finding: with grove on PATH but git-viewer not grove-installed, the flow →
// git-viewer handoff routes through `grove git-viewer` and must resolve the
// binary from PATH rather than failing "unknown tool: git-viewer".
func TestPathFallbackResolvesRegisteredToolFromPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	binDir := t.TempDir()
	fake := filepath.Join(binDir, "git-viewer")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	if got := pathFallbackForRegisteredTool("git-viewer"); got != fake {
		t.Fatalf("registered tool fallback = %q, want %q", got, fake)
	}
}

// TestPathFallbackRefusesUnregisteredNames guards the delegation boundary: a
// name outside the tool registry never falls back to PATH, even when a
// same-named executable exists there — delegation must not become a shell
// passthrough.
func TestPathFallbackRefusesUnregisteredNames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	binDir := t.TempDir()
	fake := filepath.Join(binDir, "definitely-not-a-grove-tool")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	if got := pathFallbackForRegisteredTool("definitely-not-a-grove-tool"); got != "" {
		t.Fatalf("unregistered name resolved to %q, want refusal", got)
	}
}
