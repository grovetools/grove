package setup

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDeleteTOMLValue(t *testing.T) {
	t.Run("nested key", func(t *testing.T) {
		data := map[string]interface{}{
			"tui": map[string]interface{}{
				"theme":   "dark",
				"preview": true,
			},
		}

		if !DeleteTOMLValue(data, "tui", "theme") {
			t.Fatal("expected DeleteTOMLValue to report deletion")
		}

		tui, ok := data["tui"].(map[string]interface{})
		if !ok {
			t.Fatal("expected tui table to survive (still has keys)")
		}
		if _, ok := tui["theme"]; ok {
			t.Error("expected tui.theme to be deleted")
		}
		if _, ok := tui["preview"]; !ok {
			t.Error("expected tui.preview to be preserved")
		}
	})

	t.Run("top-level key", func(t *testing.T) {
		data := map[string]interface{}{
			"editor": "vim",
			"tui":    map[string]interface{}{"theme": "dark"},
		}

		if !DeleteTOMLValue(data, "editor") {
			t.Fatal("expected DeleteTOMLValue to report deletion")
		}
		if _, ok := data["editor"]; ok {
			t.Error("expected editor to be deleted")
		}
		if _, ok := data["tui"]; !ok {
			t.Error("expected tui to be preserved")
		}
	})

	t.Run("last key removes empty table", func(t *testing.T) {
		data := map[string]interface{}{
			"tui":    map[string]interface{}{"theme": "dark"},
			"editor": "vim",
		}

		if !DeleteTOMLValue(data, "tui", "theme") {
			t.Fatal("expected DeleteTOMLValue to report deletion")
		}
		if _, ok := data["tui"]; ok {
			t.Error("expected now-empty tui table to be removed")
		}
		if _, ok := data["editor"]; !ok {
			t.Error("expected editor to be preserved")
		}
	})

	t.Run("last key cascades empty parents", func(t *testing.T) {
		data := map[string]interface{}{
			"a": map[string]interface{}{
				"b": map[string]interface{}{"c": 1},
			},
		}

		if !DeleteTOMLValue(data, "a", "b", "c") {
			t.Fatal("expected DeleteTOMLValue to report deletion")
		}
		if len(data) != 0 {
			t.Errorf("expected all empty parent tables removed, got %v", data)
		}
	})

	t.Run("missing key is a no-op", func(t *testing.T) {
		data := map[string]interface{}{
			"tui": map[string]interface{}{"theme": "dark"},
		}

		if DeleteTOMLValue(data, "tui", "nope") {
			t.Error("expected missing nested key to report false")
		}
		if DeleteTOMLValue(data, "nope") {
			t.Error("expected missing top-level key to report false")
		}
		if DeleteTOMLValue(data, "nope", "deeper") {
			t.Error("expected missing intermediate table to report false")
		}
		if DeleteTOMLValue(data, "tui", "theme", "deeper") {
			t.Error("expected scalar in path to report false")
		}
		if DeleteTOMLValue(data) {
			t.Error("expected empty path to report false")
		}

		tui, ok := data["tui"].(map[string]interface{})
		if !ok || tui["theme"] != "dark" {
			t.Errorf("expected data to be unchanged, got %v", data)
		}
	})
}

// parseYAML unmarshals a YAML document into a node tree for tests.
func parseYAML(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(src), &root); err != nil {
		t.Fatalf("failed to parse test YAML: %v", err)
	}
	return &root
}

func TestDeleteValue(t *testing.T) {
	t.Run("nested key", func(t *testing.T) {
		root := parseYAML(t, "tui:\n  theme: dark\n  preview: true\neditor: vim\n")

		if !DeleteValue(root, "tui", "theme") {
			t.Fatal("expected DeleteValue to report deletion")
		}
		if HasKey(root, "tui", "theme") {
			t.Error("expected tui.theme to be deleted")
		}
		if !HasKey(root, "tui", "preview") {
			t.Error("expected tui.preview to be preserved")
		}
		if !HasKey(root, "editor") {
			t.Error("expected editor to be preserved")
		}
	})

	t.Run("last key removes empty mapping", func(t *testing.T) {
		root := parseYAML(t, "tui:\n  theme: dark\neditor: vim\n")

		if !DeleteValue(root, "tui", "theme") {
			t.Fatal("expected DeleteValue to report deletion")
		}
		if HasKey(root, "tui") {
			t.Error("expected now-empty tui mapping to be removed")
		}
		if !HasKey(root, "editor") {
			t.Error("expected editor to be preserved")
		}
	})

	t.Run("last key cascades empty parents", func(t *testing.T) {
		root := parseYAML(t, "a:\n  b:\n    c: 1\n")

		if !DeleteValue(root, "a", "b", "c") {
			t.Fatal("expected DeleteValue to report deletion")
		}
		if HasKey(root, "a") {
			t.Error("expected all empty parent mappings to be removed")
		}
	})

	t.Run("missing key is a no-op", func(t *testing.T) {
		root := parseYAML(t, "tui:\n  theme: dark\n")

		if DeleteValue(root, "tui", "nope") {
			t.Error("expected missing nested key to report false")
		}
		if DeleteValue(root, "nope") {
			t.Error("expected missing top-level key to report false")
		}
		if DeleteValue(root, "tui", "theme", "deeper") {
			t.Error("expected scalar in path to report false")
		}
		if DeleteValue(root) {
			t.Error("expected empty path to report false")
		}
		if DeleteValue(nil, "tui") {
			t.Error("expected nil root to report false")
		}

		if GetValue(root, "tui", "theme") != "dark" {
			t.Error("expected document to be unchanged")
		}
	})
}
