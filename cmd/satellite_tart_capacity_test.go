package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCalculateFullTartCapacityPlanUsesSeparateBudgets(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "payload"), make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := calculateFullTartCapacityPlan(root)
	if err != nil {
		t.Fatal(err)
	}
	host, err := plan.Host.RequiredBytes()
	if err != nil {
		t.Fatal(err)
	}
	guest, err := plan.Guest.RequiredBytes()
	if err != nil {
		t.Fatal(err)
	}
	if host == guest {
		t.Fatalf("host and guest capacity observations collapsed into one budget: %d", host)
	}
	if plan.Host.PayloadBytes < 4096 || plan.Guest.PayloadBytes < 4096 {
		t.Fatalf("calculated payload omitted local bytes: host=%d guest=%d", plan.Host.PayloadBytes, plan.Guest.PayloadBytes)
	}
}
