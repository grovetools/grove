package doctorchecks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/grovetools/core/pkg/doctor"
)

// pathSandbox gives a test its own toolchain dir (with a fake grove binary) and
// its own "global" bin dir, so nothing touches the real ~/.local/bin.
func pathSandbox(t *testing.T) (globalDir, managed string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GROVE_HOME", filepath.Join(home, "grove"))

	binDir := filepath.Join(home, "toolchain", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GROVE_BIN", binDir)
	managed = filepath.Join(binDir, "grove")
	if err := os.WriteFile(managed, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}

	globalDir = filepath.Join(home, ".local", "bin")
	t.Setenv(exposeDirEnv, globalDir)
	// An empty PATH so only the hub link can make grove reachable.
	t.Setenv("PATH", t.TempDir())
	return globalDir, managed
}

func TestGroveOnPathCheck(t *testing.T) {
	globalDir, managed := pathSandbox(t)
	check := &groveOnPathCheck{}

	res := check.Run(context.Background(), doctor.RunOptions{})
	if res.Status != doctor.StatusFail {
		t.Fatalf("no grove anywhere: status = %s, want fail", res.Status)
	}
	if res.Resolution == "" {
		t.Error("failing check offered no fix")
	}

	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(managed, filepath.Join(globalDir, "grove")); err != nil {
		t.Fatal(err)
	}
	if res := check.Run(context.Background(), doctor.RunOptions{}); res.Status != doctor.StatusOK {
		t.Fatalf("hub link present: status = %s (%s), want ok", res.Status, res.Message)
	}

	// A broken link is not reachability.
	link := filepath.Join(globalDir, "grove")
	_ = os.Remove(link)
	if err := os.Symlink(filepath.Join(globalDir, "gone"), link); err != nil {
		t.Fatal(err)
	}
	if res := check.Run(context.Background(), doctor.RunOptions{}); res.Status != doctor.StatusFail {
		t.Fatalf("broken link: status = %s, want fail", res.Status)
	}
}

func TestGroveGlobalSymlinkCheck(t *testing.T) {
	globalDir, managed := pathSandbox(t)
	check := &groveGlobalSymlinkCheck{}
	link := filepath.Join(globalDir, "grove")

	if res := check.Run(context.Background(), doctor.RunOptions{}); res.Status != doctor.StatusWarn {
		t.Fatalf("missing link: status = %s, want warn", res.Status)
	}

	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(managed, link); err != nil {
		t.Fatal(err)
	}
	if res := check.Run(context.Background(), doctor.RunOptions{}); res.Status != doctor.StatusOK {
		t.Fatalf("current link: status = %s (%s), want ok", res.Status, res.Message)
	}

	// Stale: points at some other grove, so updates never reach the user.
	stale := filepath.Join(t.TempDir(), "grove")
	if err := os.WriteFile(stale, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	_ = os.Remove(link)
	if err := os.Symlink(stale, link); err != nil {
		t.Fatal(err)
	}
	if res := check.Run(context.Background(), doctor.RunOptions{}); res.Status != doctor.StatusWarn {
		t.Fatalf("stale link: status = %s (%s), want warn", res.Status, res.Message)
	}

	// A regular file is the user's own install: warn, never touch.
	_ = os.Remove(link)
	if err := os.WriteFile(link, []byte("their own grove\n"), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	res := check.Run(context.Background(), doctor.RunOptions{})
	if res.Status != doctor.StatusWarn {
		t.Fatalf("regular file: status = %s (%s), want warn", res.Status, res.Message)
	}
}
