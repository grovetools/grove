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

	// Root level (depth=0) should not be collapsed
	if nodes[0].Collapsed {
		t.Error("Root level should not be collapsed by CollapseAll")
	}
	// Child (depth=1) should be collapsed
	if !nodes[0].Children[0].Collapsed {
		t.Error("Child should be collapsed")
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
