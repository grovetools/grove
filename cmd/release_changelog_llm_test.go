package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTotalFileBytes covers the changelog context-budget measurement: the sum of
// on-disk sizes that gates whether callChangelogViaSharedPrefix uploads the
// whole-repo cx context or drops it for a diff-only request.
func TestTotalFileBytes(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, make([]byte, 300), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, make([]byte, 700), 0o600); err != nil {
		t.Fatal(err)
	}

	// Missing files are skipped, not fatal.
	got := totalFileBytes([]string{a, b, filepath.Join(dir, "missing.txt")})
	if got != 1000 {
		t.Fatalf("totalFileBytes = %d, want 1000", got)
	}

	// The budget boundary: 1000 bytes is under the 500 KB budget (whole-repo
	// context kept); a file over the budget flips to the diff-only path.
	if got > changelogCtxBudgetBytes {
		t.Fatalf("small fileset (%d) should be within budget %d", got, changelogCtxBudgetBytes)
	}
	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(big, make([]byte, changelogCtxBudgetBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if over := totalFileBytes([]string{big}); over <= changelogCtxBudgetBytes {
		t.Fatalf("oversized fileset (%d) should exceed budget %d", over, changelogCtxBudgetBytes)
	}
}

func TestFixChangelogHeader(t *testing.T) {
	tests := []struct {
		name      string
		changelog string
		version   string
		date      string
		want      string
	}{
		{
			name:      "fixes wrong version and date",
			changelog: "## v0.4.0 (2024-07-26)\n\nSome changelog content.\n",
			version:   "v0.4.1",
			date:      "2026-01-14",
			want:      "## v0.4.1 (2026-01-14)\n\nSome changelog content.\n",
		},
		{
			name:      "fixes version with different format",
			changelog: "## v1.0.0-beta (2025-01-01)\n\n### Features\n- New feature\n",
			version:   "v1.0.0",
			date:      "2026-01-14",
			want:      "## v1.0.0 (2026-01-14)\n\n### Features\n- New feature\n",
		},
		{
			name:      "handles missing header",
			changelog: "Some content without header\n",
			version:   "v0.4.1",
			date:      "2026-01-14",
			want:      "## v0.4.1 (2026-01-14)\n\nSome content without header\n",
		},
		{
			name:      "handles empty changelog",
			changelog: "",
			version:   "v0.4.1",
			date:      "2026-01-14",
			want:      "",
		},
		{
			name:      "preserves content after header",
			changelog: "## v0.3.0 (2025-09-26)\n\nThis release introduces a major overhaul.\n\n### Features\n\n- Add feature (abc1234)\n\n### File Changes\n\n```\n file.go | 10 +\n```\n",
			version:   "v0.4.1",
			date:      "2026-01-14",
			want:      "## v0.4.1 (2026-01-14)\n\nThis release introduces a major overhaul.\n\n### Features\n\n- Add feature (abc1234)\n\n### File Changes\n\n```\n file.go | 10 +\n```\n",
		},
		{
			name:      "handles header only",
			changelog: "## v0.1.0 (2024-01-01)",
			version:   "v0.2.0",
			date:      "2026-01-14",
			want:      "## v0.2.0 (2026-01-14)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fixChangelogHeader(tt.changelog, tt.version, tt.date)
			if got != tt.want {
				t.Errorf("fixChangelogHeader() =\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}
