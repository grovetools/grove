package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/grovetools/grove/pkg/release"
)

// TestVerifyContextFileset exercises the freeze-verify guard that gates any API
// spend: an empty fileset and a near-empty one must be rejected, and a
// real-sized fileset accepted.
func TestVerifyContextFileset(t *testing.T) {
	t.Run("empty fileset rejected", func(t *testing.T) {
		_, err := verifyContextFileset("/tmp/repo", nil)
		if err == nil {
			t.Fatal("expected empty fileset to be rejected")
		}
		if !strings.Contains(err.Error(), "empty prefix") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("near-empty fileset rejected", func(t *testing.T) {
		dir := t.TempDir()
		f := filepath.Join(dir, "ctx.txt")
		if err := os.WriteFile(f, []byte("tiny"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := verifyContextFileset(dir, []string{f})
		if err == nil {
			t.Fatal("expected near-empty fileset to be rejected")
		}
		if !strings.Contains(err.Error(), "near-empty prefix") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("real-sized fileset accepted", func(t *testing.T) {
		dir := t.TempDir()
		f := filepath.Join(dir, "ctx.txt")
		if err := os.WriteFile(f, []byte(strings.Repeat("x", genMinContextBytes+10)), 0o600); err != nil {
			t.Fatal(err)
		}
		total, err := verifyContextFileset(dir, []string{f})
		if err != nil {
			t.Fatalf("expected acceptance, got %v", err)
		}
		if total < genMinContextBytes {
			t.Fatalf("expected total >= %d, got %d", genMinContextBytes, total)
		}
	})
}

// TestRunGenPool exercises the worker pool + single-writer collector: every
// repo is processed exactly once, each repo's log block contains only its own
// lines (no interleaving), callbacks never run concurrently, and sorting the
// completion-ordered results restores a deterministic order. Run with -race.
func TestRunGenPool(t *testing.T) {
	repos := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}

	work := func(_ context.Context, repo string, out io.Writer) genWorkerResult {
		// Write several lines with yields in between; under concurrency these
		// would interleave if the buffers were shared.
		for i := 0; i < 5; i++ {
			fmt.Fprintf(out, "%s line %d\n", repo, i)
			time.Sleep(time.Millisecond)
		}
		return genWorkerResult{
			repo: repo,
			plan: release.RepoReleasePlan{NextVersion: "v1.0.0-" + repo},
			res:  genRepoResult{Repo: repo, Status: "staged"},
		}
	}

	// The collector contract: callbacks run on one goroutine only. inCallback
	// would trip the race detector (and this check) on any overlap.
	inCallback := false
	enter := func() {
		if inCallback {
			t.Error("collector callbacks ran concurrently")
		}
		inCallback = true
	}
	leave := func() { inCallback = false }

	starts := map[string]int{}
	onStart := func(repo string) { enter(); starts[repo]++; leave() }
	finishes := map[string]int{}
	onResult := func(r genWorkerResult) { enter(); finishes[r.repo]++; leave() }

	results := runGenPool(context.Background(), repos, 3, work, onStart, onResult)

	if len(results) != len(repos) {
		t.Fatalf("expected %d results, got %d", len(repos), len(results))
	}
	for _, repo := range repos {
		if starts[repo] != 1 || finishes[repo] != 1 {
			t.Errorf("repo %s: starts=%d finishes=%d, want 1/1", repo, starts[repo], finishes[repo])
		}
	}
	for _, r := range results {
		for _, line := range strings.Split(strings.TrimSpace(string(r.log)), "\n") {
			if !strings.HasPrefix(line, r.repo+" line ") {
				t.Errorf("repo %s log block contains foreign line %q", r.repo, line)
			}
		}
	}

	// Deterministic summary order after the sort runReleaseGen applies.
	orderIdx := make(map[string]int, len(repos))
	for i, r := range repos {
		orderIdx[r] = i
	}
	sort.Slice(results, func(i, j int) bool { return orderIdx[results[i].repo] < orderIdx[results[j].repo] })
	for i, r := range results {
		if r.repo != repos[i] {
			t.Fatalf("sorted results out of order at %d: got %s want %s", i, r.repo, repos[i])
		}
	}
}

// TestRunGenPoolCancellation verifies workers stop picking up new repos once
// the context is canceled but the run still drains cleanly.
func TestRunGenPoolCancellation(t *testing.T) {
	repos := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	ctx, cancel := context.WithCancel(context.Background())

	work := func(_ context.Context, repo string, _ io.Writer) genWorkerResult {
		cancel() // first repos in flight cancel the rest
		time.Sleep(5 * time.Millisecond)
		return genWorkerResult{repo: repo, res: genRepoResult{Repo: repo}}
	}

	results := runGenPool(ctx, repos, 2, work, nil, nil)
	if len(results) == 0 {
		t.Fatal("in-flight repos should still report results")
	}
	if len(results) == len(repos) {
		t.Fatalf("expected cancellation to skip some repos, got all %d", len(results))
	}
}
