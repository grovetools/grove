package configui

import (
	"testing"
)

func TestConfigNode_IsExpandable(t *testing.T) {
	// Node with children should be expandable
	parent := &ConfigNode{
		Field: FieldMeta{Type: FieldMap},
		Children: []*ConfigNode{
			{Field: FieldMeta{Type: FieldString}},
		},
	}
	if !parent.IsExpandable() {
		t.Error("Node with children should be expandable")
	}

	// Node without children should not be expandable
	leaf := &ConfigNode{
		Field: FieldMeta{Type: FieldString},
	}
	if leaf.IsExpandable() {
		t.Error("Node without children should not be expandable")
	}
}

func TestConfigNode_IsContainer(t *testing.T) {
	tests := []struct {
		name     string
		field    FieldMeta
		expected bool
	}{
		{"FieldMap", FieldMeta{Type: FieldMap}, true},
		{"FieldObject", FieldMeta{Type: FieldObject}, true},
		{"FieldArray", FieldMeta{Type: FieldArray}, true},
		{"FieldString", FieldMeta{Type: FieldString}, false},
		{"FieldBool", FieldMeta{Type: FieldBool}, false},
		{"FieldInt", FieldMeta{Type: FieldInt}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &ConfigNode{Field: tt.field}
			if node.IsContainer() != tt.expected {
				t.Errorf("IsContainer() = %v, want %v", node.IsContainer(), tt.expected)
			}
		})
	}
}

func TestFlatten(t *testing.T) {
	// Create a simple tree
	root := []*ConfigNode{
		{
			Field:     FieldMeta{Path: []string{"parent1"}},
			Collapsed: false,
			Children: []*ConfigNode{
				{Field: FieldMeta{Path: []string{"child1"}}, Depth: 1},
				{Field: FieldMeta{Path: []string{"child2"}}, Depth: 1},
			},
		},
		{
			Field:     FieldMeta{Path: []string{"parent2"}},
			Collapsed: true,
			Children: []*ConfigNode{
				{Field: FieldMeta{Path: []string{"child3"}}, Depth: 1},
			},
		},
	}

	flat := Flatten(root)

	// Expect: parent1, child1, child2, parent2 (child3 is hidden because parent2 is collapsed)
	if len(flat) != 4 {
		t.Errorf("Flatten returned %d nodes, expected 4", len(flat))
	}

	expected := []string{"parent1", "child1", "child2", "parent2"}
	for i, node := range flat {
		if node.Field.Label() != expected[i] {
			t.Errorf("Flatten node %d = %q, expected %q", i, node.Field.Label(), expected[i])
		}
	}
}

func TestToggleNode(t *testing.T) {
	node := &ConfigNode{
		Field:     FieldMeta{Type: FieldMap},
		Collapsed: true,
		Children:  []*ConfigNode{{Field: FieldMeta{Type: FieldString}}},
	}

	// Toggle should expand
	ToggleNode(node)
	if node.Collapsed {
		t.Error("ToggleNode should have expanded the node")
	}

	// Toggle again should collapse
	ToggleNode(node)
	if !node.Collapsed {
		t.Error("ToggleNode should have collapsed the node")
	}
}

func TestExpandAll(t *testing.T) {
	nodes := []*ConfigNode{
		{
			Field:     FieldMeta{Path: []string{"parent"}},
			Collapsed: true,
			Children: []*ConfigNode{
				{
					Field:     FieldMeta{Path: []string{"child"}},
					Collapsed: true,
					Depth:     1,
				},
			},
		},
	}

	ExpandAll(nodes)

	if nodes[0].Collapsed {
		t.Error("Parent should be expanded")
	}
	if nodes[0].Children[0].Collapsed {
		t.Error("Child should be expanded")
	}
}

func TestCollapseAll(t *testing.T) {
	nodes := []*ConfigNode{
		{
			Field:     FieldMeta{Path: []string{"parent"}},
			Collapsed: false,
			Depth:     0,
			Children: []*ConfigNode{
				{
					Field:     FieldMeta{Path: []string{"child"}},
					Collapsed: false,
					Depth:     1,
				},
			},
		},
	}

	CollapseAll(nodes)

	// All expandable nodes should be collapsed
	if !nodes[0].Collapsed {
		t.Error("Root level should be collapsed by CollapseAll")
	}
	if nodes[0].Children[0].Collapsed {
		t.Error("Leaf child should not be collapsed (not expandable)")
	}
}

func TestInferFieldType(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected FieldType
	}{
		{"nil", nil, FieldString},
		{"string", "hello", FieldString},
		{"bool", true, FieldBool},
		{"int", 42, FieldInt},
		{"map", map[string]string{}, FieldMap},
		{"slice", []string{}, FieldArray},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := inferFieldType(tt.input)
			if result != tt.expected {
				t.Errorf("inferFieldType(%v) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestLabelDisplayNameOverride(t *testing.T) {
	// The [claude] extension section renders as "Agent Settings" in the
	// Configuration Editor; every other field keeps its raw key.
	claude := FieldMeta{Path: []string{"claude"}}
	if got := claude.Label(); got != "Agent Settings" {
		t.Errorf("expected claude section label %q, got %q", "Agent Settings", got)
	}
	plain := FieldMeta{Path: []string{"workspaces"}}
	if got := plain.Label(); got != "workspaces" {
		t.Errorf("expected plain field label %q, got %q", "workspaces", got)
	}
}

func TestGeneratedSchemaHasClaudeSection(t *testing.T) {
	// The claude fragment must be composed into the generated schema as a
	// top-level object section with the expanded managed fields present.
	meta, ok := FieldsByPath["claude"]
	if !ok {
		t.Fatal("expected generated schema to contain a top-level 'claude' section")
	}
	if meta.Type != FieldObject {
		t.Errorf("expected claude section to be an object, got %v", meta.Type)
	}
	want := map[string]bool{
		"permissions": false, "sandbox": false, "model": false,
		"effortLevel": false, "editorMode": false, "tui": false,
		"skipDangerousModePermissionPrompt": false, "skipWorkflowUsageWarning": false,
		"agentPushNotifEnabled": false, "enabledPlugins": false,
	}
	for _, child := range meta.Children {
		key := child.Path[len(child.Path)-1]
		if _, tracked := want[key]; tracked {
			want[key] = true
		}
	}
	for key, seen := range want {
		if !seen {
			t.Errorf("expected claude section child %q in generated schema", key)
		}
	}
}
