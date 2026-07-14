package internal

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/pkg/workspace"
)

// runWorktreePath executes the plumbing verb with args and returns its raw
// stdout (the machine-readable contract: one path, newline, nothing else).
func runWorktreePath(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newWorktreePathCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// TestWorktreePathCmd pins the plumbing verb's contract: an existing worktree
// resolves in place (legacy base first, matching core FindWorktreePath); a
// nonexistent one gets the XDG new-worktree path; output is exactly one
// absolute path on stdout; invalid inputs error with nothing on stdout.
func TestWorktreePathCmd(t *testing.T) {
	root := t.TempDir()

	// Existing legacy worktree resolves to where it already is.
	legacy := filepath.Join(root, ".grove-worktrees", "plan-x")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runWorktreePath(t, "--git-root", root, "--name", "plan-x")
	if err != nil {
		t.Fatalf("existing worktree: %v", err)
	}
	if out != legacy+"\n" {
		t.Errorf("existing worktree: stdout = %q, want %q", out, legacy+"\n")
	}

	// Absent worktree: the XDG new-worktree location, computed by the same
	// core function the rest of the ecosystem uses.
	out, err = runWorktreePath(t, "--git-root", root, "--name", "plan-y")
	if err != nil {
		t.Fatalf("new worktree: %v", err)
	}
	want := workspace.ResolveNewWorktreePath(root, "plan-y", true) + "\n"
	if out != want {
		t.Errorf("new worktree: stdout = %q, want %q", out, want)
	}
	line := strings.TrimSuffix(out, "\n")
	if strings.Contains(line, "\n") || !filepath.IsAbs(line) {
		t.Errorf("output must be a single absolute path, got %q", out)
	}

	// Invalid inputs: nonzero with nothing on stdout.
	for _, args := range [][]string{
		{"--git-root", "relative/root", "--name", "plan-x"},
		{"--git-root", root, "--name", ""},
		{"--name", "plan-x"},
		{"--git-root", root},
	} {
		out, err := runWorktreePath(t, args...)
		if err == nil {
			t.Errorf("args %v: want error", args)
		}
		if out != "" {
			t.Errorf("args %v: stdout must stay empty on error, got %q", args, out)
		}
	}
}
