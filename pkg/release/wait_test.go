package release

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitConfig(t *testing.T) {
	config := DefaultWaitConfig()

	if config.MaxRetries != 20 {
		t.Errorf("Expected MaxRetries to be 20, got %d", config.MaxRetries)
	}

	if config.InitialBackoff != 15*time.Second {
		t.Errorf("Expected InitialBackoff to be 15s, got %v", config.InitialBackoff)
	}

	if config.MaxBackoff != 60*time.Second {
		t.Errorf("Expected MaxBackoff to be 60s, got %v", config.MaxBackoff)
	}

	if config.Timeout != 5*time.Minute {
		t.Errorf("Expected Timeout to be 5m, got %v", config.Timeout)
	}
}

func TestWaitForModuleAvailabilityTimeout(t *testing.T) {
	ctx := context.Background()
	config := WaitConfig{
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     20 * time.Millisecond,
		Timeout:        100 * time.Millisecond,
	}

	// This should timeout since the module doesn't exist (empty repoDir → go
	// list -m fallback, which cannot resolve fake.module/test).
	err := WaitForModuleAvailabilityWithConfig(ctx, "fake.module/test", "v1.0.0", "", config)
	if err == nil {
		t.Error("Expected error for non-existent module, got nil")
	}
}

// TestGitTagVisibleOnRemote validates the ls-remote tag-visibility gate against
// a scratch bare "origin": the tag is invisible until it is actually pushed.
func TestGitTagVisibleOnRemote(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "origin.git")
	work := filepath.Join(tmp, "work")

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run(tmp, "init", "--bare", bare)
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	run(tmp, "init", work)
	run(work, "remote", "add", "origin", "file://"+bare)
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(work, "add", ".")
	run(work, "commit", "-m", "init")
	run(work, "branch", "-M", "main")
	run(work, "push", "origin", "main")

	// Before the tag is pushed, the gate must report it invisible.
	if err := gitTagVisibleOnRemote(ctx, work, "v1.0.0"); err == nil {
		t.Fatal("expected tag v1.0.0 to be invisible before push, got nil")
	}

	run(work, "tag", "-a", "v1.0.0", "-m", "release v1.0.0")
	run(work, "push", "origin", "v1.0.0")

	// After the push, the gate must pass.
	if err := gitTagVisibleOnRemote(ctx, work, "v1.0.0"); err != nil {
		t.Fatalf("expected tag v1.0.0 visible after push, got %v", err)
	}

	// A prefix of the real tag must NOT false-positive (ref is fully qualified).
	if err := gitTagVisibleOnRemote(ctx, work, "v1.0"); err == nil {
		t.Fatal("expected non-existent tag v1.0 to be invisible, got nil")
	}
}
