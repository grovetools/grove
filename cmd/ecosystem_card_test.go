package cmd

import (
	"strings"
	"testing"

	"github.com/grovetools/core/config"
)

func TestDeriveEcosystemCardIsIdentityOnlyAndStable(t *testing.T) {
	first := deriveEcosystemCard(t.TempDir(), nil)
	if first.ID == "" {
		t.Fatal("identity was not minted")
	}
	second := deriveEcosystemCard(t.TempDir(), &first)
	if second.ID != first.ID {
		t.Fatalf("identity changed: %q -> %q", first.ID, second.ID)
	}
	if err := second.Validate(); err != nil {
		t.Fatalf("identity card invalid: %v", err)
	}
}

func TestRenderEcosystemCardSummaryDoesNotTeachRemovedFields(t *testing.T) {
	got := renderEcosystemCardSummary(config.EcosystemCard{ID: "01ABCDEFGHJKMNPQRSTVWXYZ01"})
	if !strings.Contains(got, "id:") {
		t.Fatalf("summary=%q", got)
	}
	for _, retired := range []string{"layout", "remotes", "notebooks"} {
		if strings.Contains(got, retired) {
			t.Fatalf("summary retained %q: %s", retired, got)
		}
	}
}
