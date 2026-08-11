package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestSyncIdentityCommandsAreDistinctAndExplicit(t *testing.T) {
	cmd := newSyncCmd()
	if found, _, err := cmd.Find([]string{"conflicts"}); err != nil || found.Name() != "conflicts" {
		t.Fatalf("conflicts command missing: %v", err)
	}
	adopt, _, err := cmd.Find([]string{"adopt-id"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	adopt.SetOut(&out)
	adopt.SetErr(&out)
	if err := adopt.RunE(adopt, []string{"conflict-1"}); err == nil || !strings.Contains(err.Error(), "--survivor") {
		t.Fatalf("adopt-id inferred ids: %v", err)
	}
}
