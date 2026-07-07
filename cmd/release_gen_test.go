package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
