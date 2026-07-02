package configui

import (
	"testing"

	"github.com/grovetools/core/config"
)

func TestBuildAuditNodes(t *testing.T) {
	findings := []config.AuditFinding{
		{Key: "tui.theme", Class: config.AuditKnownCore, Layer: config.SourceGlobal, File: "/g/grove.toml"},
		{Key: "old_stuff", Class: config.AuditOrphan, Layer: config.SourceGlobal, File: "/g/grove.toml"},
		{Key: "tui.bogus", Class: config.AuditUnknownNested, Layer: config.SourceGlobal, File: "/g/grove.toml"},
		{Key: "proj_only", Class: config.AuditOrphan, Layer: config.SourceProject, File: "/p/grove.toml"},
	}

	layered := &config.LayeredConfig{}

	t.Run("filters to layer and class", func(t *testing.T) {
		roots := BuildAuditNodes(findings, layered, config.SourceGlobal)
		if len(roots) != 1 {
			t.Fatalf("expected 1 header root, got %d", len(roots))
		}

		header := roots[0]
		if !header.IsAuditSection() {
			t.Error("expected header to report IsAuditSection")
		}
		if header.DisplayKey() != AuditSectionTitle {
			t.Errorf("expected header key %q, got %q", AuditSectionTitle, header.DisplayKey())
		}
		if header.Collapsed {
			t.Error("expected audit section to start expanded")
		}

		if len(header.Children) != 2 {
			t.Fatalf("expected 2 audit rows (orphan + unknown-nested), got %d", len(header.Children))
		}
		for _, child := range header.Children {
			if child.Audit == nil {
				t.Fatalf("expected audit row %q to carry its finding", child.DisplayKey())
			}
			if child.Parent != header {
				t.Errorf("expected row %q to be parented to the header", child.DisplayKey())
			}
		}
		if header.Children[0].DisplayKey() != "old_stuff" || header.Children[0].Audit.Class != config.AuditOrphan {
			t.Errorf("unexpected first row: %q (%s)", header.Children[0].DisplayKey(), header.Children[0].Audit.Class)
		}
		if header.Children[1].DisplayKey() != "tui.bogus" || header.Children[1].Audit.Class != config.AuditUnknownNested {
			t.Errorf("unexpected second row: %q (%s)", header.Children[1].DisplayKey(), header.Children[1].Audit.Class)
		}
	})

	t.Run("no findings yields no section", func(t *testing.T) {
		if roots := BuildAuditNodes(findings, layered, config.SourceEcosystem); roots != nil {
			t.Errorf("expected nil roots for layer without findings, got %v", roots)
		}
	})

	t.Run("orphan value resolved from layer extensions", func(t *testing.T) {
		withValues := &config.LayeredConfig{
			Global: &config.Config{
				Extensions: map[string]interface{}{"old_stuff": "still-here"},
			},
		}
		roots := BuildAuditNodes(findings, withValues, config.SourceGlobal)
		if len(roots) != 1 {
			t.Fatalf("expected 1 header root, got %d", len(roots))
		}
		if got := roots[0].Children[0].Value; got != "still-here" {
			t.Errorf("expected orphan value from Extensions, got %v", got)
		}
	})
}

func TestAuditBadge(t *testing.T) {
	if AuditBadge(config.AuditOrphan) != "⚠ ORPHAN" {
		t.Errorf("unexpected orphan badge: %q", AuditBadge(config.AuditOrphan))
	}
	if AuditBadge(config.AuditUnknownNested) != "⚠ UNREAD" {
		t.Errorf("unexpected unknown-nested badge: %q", AuditBadge(config.AuditUnknownNested))
	}
	if AuditBadge(config.AuditKnownCore) != "" {
		t.Errorf("expected empty badge for known-core, got %q", AuditBadge(config.AuditKnownCore))
	}
}
