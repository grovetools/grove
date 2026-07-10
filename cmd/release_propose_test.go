package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBuildProposeArgs verifies the `docgen propose` argv follows gen's flag
// conventions: --output-dir and --usage-json always present; --model and
// --cache-ttl passed through only when set (an unset ttl lets docgen apply its
// 1h propose default); --dry-run only when requested.
func TestBuildProposeArgs(t *testing.T) {
	joined := func(a []string) string { return strings.Join(a, " ") }

	t.Run("minimal: only required args, no model/ttl/dry-run", func(t *testing.T) {
		args := buildProposeArgs(proposeArgsInput{outputDir: "/out", usagePath: "/tmp/u.json"})
		got := joined(args)
		if !strings.Contains(got, "propose --output-dir /out --usage-json /tmp/u.json") {
			t.Fatalf("unexpected base args: %q", got)
		}
		for _, unwanted := range []string{"--model", "--cache-ttl", "--dry-run", "--fresh", "--followup"} {
			if strings.Contains(got, unwanted) {
				t.Errorf("%s must be omitted when unset: %q", unwanted, got)
			}
		}
	})

	t.Run("full: model + ttl + dry-run passed through", func(t *testing.T) {
		args := buildProposeArgs(proposeArgsInput{
			outputDir: "/out", usagePath: "/tmp/u.json",
			model: "claude-haiku-4-5", cacheTTL: "1h", dryRun: true,
		})
		got := joined(args)
		for _, want := range []string{
			"--model claude-haiku-4-5",
			"--cache-ttl 1h",
			"--dry-run",
			"--output-dir /out",
			"--usage-json /tmp/u.json",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("args missing %q: %q", want, got)
			}
		}
	})

	t.Run("fresh adds --fresh", func(t *testing.T) {
		args := buildProposeArgs(proposeArgsInput{outputDir: "/out", usagePath: "/u", fresh: true})
		if !strings.Contains(joined(args), "--fresh") {
			t.Errorf("--fresh missing: %v", args)
		}
	})

	t.Run("followup adds --followup + --transcript", func(t *testing.T) {
		args := buildProposeArgs(proposeArgsInput{
			outputDir: "/out", usagePath: "/u",
			followup: "merge the CLI pages", transcriptPath: "/prev/transcript.json",
		})
		got := joined(args)
		if !strings.Contains(got, "--followup merge the CLI pages") {
			t.Errorf("--followup missing: %q", got)
		}
		if !strings.Contains(got, "--transcript /prev/transcript.json") {
			t.Errorf("--transcript missing: %q", got)
		}
	})
}

// TestProposeRunDirName verifies collision-free naming: the bare timestamp when
// free, then -2, -3, … as candidates already exist.
func TestProposeRunDirName(t *testing.T) {
	now := time.Date(2026, 7, 10, 15, 30, 0, 0, time.UTC)
	base := "20260710-153000"

	if got := proposeRunDirName(now, func(string) bool { return false }); got != base {
		t.Errorf("free slot: got %q, want %q", got, base)
	}

	taken := map[string]bool{base: true, base + "-2": true}
	if got := proposeRunDirName(now, func(c string) bool { return taken[c] }); got != base+"-3" {
		t.Errorf("collision: got %q, want %q", got, base+"-3")
	}
}

// TestNewProposeRunDirCollision verifies two runs "in the same second" land in
// distinct dirs on disk.
func TestNewProposeRunDirCollision(t *testing.T) {
	proposalDir := t.TempDir()
	now := time.Date(2026, 7, 10, 15, 30, 0, 0, time.UTC)

	leaf1, dir1, err := newProposeRunDir(proposalDir, now)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	leaf2, dir2, err := newProposeRunDir(proposalDir, now)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if leaf1 == leaf2 || dir1 == dir2 {
		t.Fatalf("same-second runs collided: %q vs %q", dir1, dir2)
	}
	if leaf2 != leaf1+"-2" {
		t.Errorf("second run leaf = %q, want %q", leaf2, leaf1+"-2")
	}
	for _, d := range []string{dir1, dir2} {
		if fi, serr := os.Stat(d); serr != nil || !fi.IsDir() {
			t.Errorf("run dir %q not created: %v", d, serr)
		}
	}
}

// TestRepointAndResolveProposeLatest round-trips the latest symlink: repoint,
// then resolve back to the absolute run dir, and confirm repointing replaces an
// existing link.
func TestRepointAndResolveProposeLatest(t *testing.T) {
	proposalDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proposalDir, "runs", "20260710-153000"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(proposalDir, "runs", "20260710-160000"), 0o755); err != nil {
		t.Fatal(err)
	}

	// No prior run: resolve errors.
	if _, err := resolveProposeLatestDir(proposalDir); err == nil {
		t.Error("expected an error when no latest symlink exists")
	}

	if err := repointProposeLatest(proposalDir, "20260710-153000"); err != nil {
		t.Fatalf("first repoint: %v", err)
	}
	got, err := resolveProposeLatestDir(proposalDir)
	if err != nil {
		t.Fatalf("resolve after first repoint: %v", err)
	}
	if want := filepath.Join(proposalDir, "runs", "20260710-153000"); got != want {
		t.Errorf("resolved %q, want %q", got, want)
	}

	// Repoint replaces the existing link (no error, points at the new run).
	if err := repointProposeLatest(proposalDir, "20260710-160000"); err != nil {
		t.Fatalf("second repoint: %v", err)
	}
	got, err = resolveProposeLatestDir(proposalDir)
	if err != nil {
		t.Fatalf("resolve after second repoint: %v", err)
	}
	if want := filepath.Join(proposalDir, "runs", "20260710-160000"); got != want {
		t.Errorf("after repoint resolved %q, want %q", got, want)
	}
	// The symlink target must be RELATIVE (staging tree stays relocatable).
	target, _ := os.Readlink(filepath.Join(proposalDir, "latest"))
	if filepath.IsAbs(target) {
		t.Errorf("latest symlink target should be relative, got %q", target)
	}
}
