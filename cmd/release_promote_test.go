package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBundle creates a proposal bundle dir with the given config YAML and a
// prompts/ dir containing the named files (each with placeholder content).
func writeBundle(t *testing.T, configYAML string, prompts ...string) string {
	t.Helper()
	dir := t.TempDir()
	if configYAML != "" {
		if err := os.WriteFile(filepath.Join(dir, proposedConfigName), []byte(configYAML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if len(prompts) > 0 {
		pd := filepath.Join(dir, "prompts")
		if err := os.MkdirAll(pd, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, p := range prompts {
			if err := os.WriteFile(filepath.Join(pd, p), []byte("# "+p+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

const goodBundleConfig = `enabled: true
title: notify
sections:
    - name: overview
      title: Overview
      type: prose
      prompt: 01-overview.md
      output: 01-overview.md
    - name: cli-reference
      title: CLI Reference
      type: capture
      binary: notify
      output: 02-cli-reference.md
`

// TestValidatePromoteBundle covers the up-front validation gate: a good bundle
// parses; missing output, a missing prose prompt file, and an unparseable config
// each hard-fail (and the error lists every problem).
func TestValidatePromoteBundle(t *testing.T) {
	t.Run("valid bundle passes", func(t *testing.T) {
		dir := writeBundle(t, goodBundleConfig, "01-overview.md")
		b, err := validatePromoteBundle(dir)
		if err != nil {
			t.Fatalf("expected valid bundle, got %v", err)
		}
		if len(b.Sections) != 2 {
			t.Fatalf("expected 2 sections, got %d", len(b.Sections))
		}
		if !strings.Contains(string(b.ConfigBytes), "title: notify") {
			t.Errorf("raw config bytes not preserved: %q", string(b.ConfigBytes))
		}
	})

	t.Run("missing config file", func(t *testing.T) {
		dir := writeBundle(t, "") // no config written
		_, err := validatePromoteBundle(dir)
		if err == nil || !strings.Contains(err.Error(), "missing") {
			t.Fatalf("expected missing-config error, got %v", err)
		}
	})

	t.Run("section missing output", func(t *testing.T) {
		cfg := `sections:
    - name: overview
      type: prose
      prompt: 01-overview.md
`
		dir := writeBundle(t, cfg, "01-overview.md")
		_, err := validatePromoteBundle(dir)
		if err == nil || !strings.Contains(err.Error(), "no output") {
			t.Fatalf("expected missing-output error, got %v", err)
		}
	})

	t.Run("prose prompt file missing from bundle", func(t *testing.T) {
		// Config references 01-overview.md but the bundle prompts/ dir omits it.
		dir := writeBundle(t, goodBundleConfig) // no prompt files
		_, err := validatePromoteBundle(dir)
		if err == nil || !strings.Contains(err.Error(), "missing from the bundle") {
			t.Fatalf("expected missing-prompt-file error, got %v", err)
		}
	})

	t.Run("prose section without prompt field", func(t *testing.T) {
		cfg := `sections:
    - name: overview
      type: prose
      output: 01-overview.md
`
		dir := writeBundle(t, cfg)
		_, err := validatePromoteBundle(dir)
		if err == nil || !strings.Contains(err.Error(), "no prompt") {
			t.Fatalf("expected missing-prompt error, got %v", err)
		}
	})

	t.Run("capture section missing binary", func(t *testing.T) {
		cfg := `sections:
    - name: cli-reference
      type: capture
      output: 02-cli.md
`
		dir := writeBundle(t, cfg)
		_, err := validatePromoteBundle(dir)
		if err == nil || !strings.Contains(err.Error(), "no binary") {
			t.Fatalf("expected missing-binary error, got %v", err)
		}
	})

	t.Run("capture section with command instead of binary hints the rename", func(t *testing.T) {
		// Live-observed --fresh defect: 'command:' where docgen wants 'binary:'.
		cfg := `sections:
    - name: cli-reference
      type: capture
      command: notify
      output: 02-cli.md
`
		dir := writeBundle(t, cfg)
		_, err := validatePromoteBundle(dir)
		if err == nil || !strings.Contains(err.Error(), "docgen expects binary:") {
			t.Fatalf("expected command-vs-binary hint, got %v", err)
		}
	})

	t.Run("no sections", func(t *testing.T) {
		dir := writeBundle(t, "enabled: true\n")
		_, err := validatePromoteBundle(dir)
		if err == nil || !strings.Contains(err.Error(), "no sections") {
			t.Fatalf("expected no-sections error, got %v", err)
		}
	})

	t.Run("unparseable config", func(t *testing.T) {
		dir := writeBundle(t, "sections: : : not yaml\n  - [")
		_, err := validatePromoteBundle(dir)
		if err == nil || !strings.Contains(err.Error(), "parse") {
			t.Fatalf("expected parse error, got %v", err)
		}
	})

	t.Run("lists every problem", func(t *testing.T) {
		cfg := `sections:
    - name: a
      type: prose
    - name: b
      type: prose
`
		dir := writeBundle(t, cfg)
		_, err := validatePromoteBundle(dir)
		if err == nil {
			t.Fatal("expected an error")
		}
		// Both sections lack output AND lack a prompt file: 4 problems total.
		if got := strings.Count(err.Error(), "- "); got < 4 {
			t.Errorf("expected all problems listed (>=4), got %d in:\n%s", got, err.Error())
		}
	})
}

// TestPlanAndExecutePromoteApply covers the apply plan + execution: config +
// prompts are written into the notebook, and a stale notebook prompt not
// referenced by the new config is pruned.
func TestPlanAndExecutePromoteApply(t *testing.T) {
	bundleDir := writeBundle(t, goodBundleConfig, "01-overview.md")
	bundle, err := validatePromoteBundle(bundleDir)
	if err != nil {
		t.Fatalf("bundle should validate: %v", err)
	}

	docgenDir := t.TempDir()
	// Seed a stale prompt (not referenced by the new config) and a config to be
	// overwritten.
	promptsDir := filepath.Join(docgenDir, "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "99-stale.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docgenDir, notebookConfigName), []byte("old config"), 0o644); err != nil {
		t.Fatal(err)
	}

	actions, err := planPromoteApply(bundle, docgenDir)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(actions.PromptWrites) != 1 || filepath.Base(actions.PromptWrites[0].Dst) != "01-overview.md" {
		t.Fatalf("expected 1 prompt write of 01-overview.md, got %+v", actions.PromptWrites)
	}
	if len(actions.PromptDeletes) != 1 || filepath.Base(actions.PromptDeletes[0]) != "99-stale.md" {
		t.Fatalf("expected 99-stale.md pruned, got %+v", actions.PromptDeletes)
	}

	written, deleted, err := executePromoteActions(actions)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if written != 2 { // config + 1 prompt
		t.Errorf("written = %d, want 2", written)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	// Config replaced with the bundle bytes.
	got, _ := os.ReadFile(filepath.Join(docgenDir, notebookConfigName))
	if !strings.Contains(string(got), "title: notify") {
		t.Errorf("config not replaced with bundle bytes: %q", string(got))
	}
	// Referenced prompt present, stale prompt gone.
	if _, err := os.Stat(filepath.Join(promptsDir, "01-overview.md")); err != nil {
		t.Errorf("referenced prompt not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(promptsDir, "99-stale.md")); !os.IsNotExist(err) {
		t.Errorf("stale prompt should have been pruned, stat err = %v", err)
	}
}

// TestPromoteDryRunLeavesTargetUntouched confirms computing the plan writes
// nothing to the notebook (the --dry-run guarantee).
func TestPromoteDryRunLeavesTargetUntouched(t *testing.T) {
	bundleDir := writeBundle(t, goodBundleConfig, "01-overview.md")
	bundle, err := validatePromoteBundle(bundleDir)
	if err != nil {
		t.Fatal(err)
	}

	docgenDir := t.TempDir()
	promptsDir := filepath.Join(docgenDir, "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	staleData := []byte("keep me")
	if err := os.WriteFile(filepath.Join(promptsDir, "99-stale.md"), staleData, 0o644); err != nil {
		t.Fatal(err)
	}

	// Planning is the entire dry-run path — it must not write.
	if _, err := planPromoteApply(bundle, docgenDir); err != nil {
		t.Fatalf("plan: %v", err)
	}

	if _, err := os.Stat(filepath.Join(docgenDir, notebookConfigName)); !os.IsNotExist(err) {
		t.Errorf("dry-run should not have written the config, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(promptsDir, "01-overview.md")); !os.IsNotExist(err) {
		t.Errorf("dry-run should not have written prompts, stat err = %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(promptsDir, "99-stale.md"))
	if string(got) != string(staleData) {
		t.Errorf("dry-run should not have pruned/altered the stale prompt, got %q", string(got))
	}
}

// TestResolvePromoteBundleDir covers run selection: an explicit --run resolves
// to runs/<leaf>, a missing run errors clearly, and no --run falls back to the
// latest symlink.
func TestResolvePromoteBundleDir(t *testing.T) {
	proposalDir := t.TempDir()
	runLeaf := "20260710-135333"
	runDir := filepath.Join(proposalDir, "runs", runLeaf)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("explicit run resolves", func(t *testing.T) {
		dir, leaf, err := resolvePromoteBundleDir(proposalDir, runLeaf)
		if err != nil {
			t.Fatal(err)
		}
		if dir != runDir || leaf != runLeaf {
			t.Errorf("got (%q,%q), want (%q,%q)", dir, leaf, runDir, runLeaf)
		}
	})

	t.Run("missing run errors", func(t *testing.T) {
		_, _, err := resolvePromoteBundleDir(proposalDir, "nope")
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected not-found error, got %v", err)
		}
	})

	t.Run("no run falls back to latest", func(t *testing.T) {
		if err := repointProposeLatest(proposalDir, runLeaf); err != nil {
			t.Fatal(err)
		}
		dir, leaf, err := resolvePromoteBundleDir(proposalDir, "")
		if err != nil {
			t.Fatal(err)
		}
		if dir != runDir || leaf != runLeaf {
			t.Errorf("latest: got (%q,%q), want (%q,%q)", dir, leaf, runDir, runLeaf)
		}
	})
}
