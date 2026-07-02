package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"

	"github.com/grovetools/grove/pkg/configui"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
)

// writeProjectLayer writes a project grove.toml with an orphan key and
// returns a LayeredConfig wired to it.
func writeProjectLayer(t *testing.T) (*config.LayeredConfig, string) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "grove.toml")
	content := "workspaces = [\"a\"]\n\n[stale_section]\nold_key = \"unused\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write project config: %v", err)
	}

	layered := &config.LayeredConfig{
		Project: &config.Config{
			Extensions: map[string]interface{}{
				"stale_section": map[string]interface{}{"old_key": "unused"},
			},
		},
		FilePaths: map[config.ConfigSource]string{
			config.SourceProject: path,
		},
	}
	return layered, path
}

// findAuditNode returns the first visible audit row on the page, or nil.
func findAuditNode(p *LayerPage) *configui.ConfigNode {
	for _, node := range p.nodes {
		if node.Audit != nil {
			return node
		}
	}
	return nil
}

func TestLayerPageShowsAuditRows(t *testing.T) {
	layered, _ := writeProjectLayer(t)
	filters := &FilterState{
		ShowPreview:    true,
		ViewMode:       configui.ViewAll,
		MaturityFilter: configui.MaturityStable,
		SortMode:       configui.SortConfiguredFirst,
	}

	p := NewLayerPage("Project", config.SourceProject, layered, filters, 80, 24)

	node := findAuditNode(p)
	if node == nil {
		t.Fatal("expected an audit row for the orphan key on the project page")
	}
	if node.Audit.Key != "stale_section" {
		t.Errorf("expected orphan key %q, got %q", "stale_section", node.Audit.Key)
	}
	if node.Audit.Class != config.AuditOrphan {
		t.Errorf("expected orphan class, got %s", node.Audit.Class)
	}
	if node.Audit.Layer != config.SourceProject {
		t.Errorf("expected project layer, got %s", node.Audit.Layer)
	}

	// The audit rows survive the configured-only view filter.
	filters.ViewMode = configui.ViewConfigured
	p.Refresh(layered)
	if findAuditNode(p) == nil {
		t.Error("expected audit rows to remain visible in configured-only view")
	}
}

func TestDeleteFlowRemovesKeyFromLayerFile(t *testing.T) {
	layered, path := writeProjectLayer(t)

	svc := setup.NewService(false)
	m := New(layered, setup.NewYAMLHandler(svc), setup.NewTOMLHandler(svc), grovekeymap.NewConfigKeyMap(nil))
	m.workspacePath = filepath.Dir(path)

	// Project page is index 3; grab its audit row directly.
	node := findAuditNode(m.layerPages[3])
	if node == nil {
		t.Fatal("expected an audit row for the orphan key")
	}

	// D emits deleteNodeMsg from the page; feed it to the model.
	updated, _ := m.Update(deleteNodeMsg{node: node})
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update must return the value type Model, got %T", updated)
	}
	if m2.state != viewConfirmDelete {
		t.Fatalf("expected viewConfirmDelete state, got %d", m2.state)
	}
	if m2.deleteFile != path {
		t.Errorf("expected delete target %q, got %q", path, m2.deleteFile)
	}

	// Confirm with Enter.
	updated, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m3, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update must return the value type Model, got %T", updated)
	}
	if m3.state != viewList {
		t.Errorf("expected to return to list view, got state %d", m3.state)
	}
	if !strings.HasPrefix(m3.statusMsg, "Deleted stale_section") {
		t.Errorf("expected delete status message, got %q", m3.statusMsg)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to re-read project config: %v", err)
	}
	if strings.Contains(string(data), "stale_section") || strings.Contains(string(data), "old_key") {
		t.Errorf("expected orphan key removed from file, got:\n%s", data)
	}
	if !strings.Contains(string(data), "workspaces") {
		t.Errorf("expected unrelated keys preserved, got:\n%s", data)
	}
}

func TestDeleteCancelLeavesFileUntouched(t *testing.T) {
	layered, path := writeProjectLayer(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read project config: %v", err)
	}

	svc := setup.NewService(false)
	m := New(layered, setup.NewYAMLHandler(svc), setup.NewTOMLHandler(svc), grovekeymap.NewConfigKeyMap(nil))
	m.workspacePath = filepath.Dir(path)

	node := findAuditNode(m.layerPages[3])
	if node == nil {
		t.Fatal("expected an audit row for the orphan key")
	}

	updated, _ := m.Update(deleteNodeMsg{node: node})
	m2 := updated.(Model)
	updated, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m3 := updated.(Model)

	if m3.state != viewList {
		t.Errorf("expected esc to return to list view, got state %d", m3.state)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to re-read project config: %v", err)
	}
	if string(before) != string(after) {
		t.Error("expected cancel to leave the file untouched")
	}
}
