package cmd

import (
	"strings"
	"testing"
)

// TestBuildProposeArgs verifies the `docgen propose` argv follows gen's flag
// conventions: --output-dir and --usage-json always present; --model and
// --cache-ttl passed through only when set (an unset ttl lets docgen apply its
// 1h propose default); --dry-run only when requested.
func TestBuildProposeArgs(t *testing.T) {
	joined := func(a []string) string { return strings.Join(a, " ") }

	t.Run("minimal: only required args, no model/ttl/dry-run", func(t *testing.T) {
		args := buildProposeArgs("/out", "/tmp/u.json", "", "", false)
		got := joined(args)
		if !strings.Contains(got, "propose --output-dir /out --usage-json /tmp/u.json") {
			t.Fatalf("unexpected base args: %q", got)
		}
		if strings.Contains(got, "--model") {
			t.Errorf("--model must be omitted when unset: %q", got)
		}
		if strings.Contains(got, "--cache-ttl") {
			t.Errorf("--cache-ttl must be omitted when unset (docgen defaults propose to 1h): %q", got)
		}
		if strings.Contains(got, "--dry-run") {
			t.Errorf("--dry-run must be omitted unless requested: %q", got)
		}
	})

	t.Run("full: model + ttl + dry-run passed through", func(t *testing.T) {
		args := buildProposeArgs("/out", "/tmp/u.json", "claude-haiku-4-5", "1h", true)
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
}
