// Package configui provides schema-driven configuration UI types and metadata.
package configui

import "github.com/grovetools/core/config"

// FieldType represents the type of a configuration field for UI rendering.
type FieldType int

const (
	FieldString FieldType = iota // Plain text input
	FieldBool                    // Boolean toggle
	FieldInt                     // Integer input
	FieldSelect                  // String with enum (dropdown/select)
	FieldArray                   // Array of strings
	FieldObject                  // Nested object (collapsible in UI)
	FieldMap                     // Object with additionalProperties (key-value editor)
)

// String returns a string representation of the FieldType.
func (ft FieldType) String() string {
	switch ft {
	case FieldString:
		return "string"
	case FieldBool:
		return "bool"
	case FieldInt:
		return "int"
	case FieldSelect:
		return "select"
	case FieldArray:
		return "array"
	case FieldObject:
		return "object"
	case FieldMap:
		return "map"
	default:
		return "unknown"
	}
}

// FieldMeta contains metadata for a configuration field derived from JSON Schema.
type FieldMeta struct {
	// Path is the dot-separated path to the field (e.g., ["tui", "theme"]).
	Path []string

	// Type is the UI field type.
	Type FieldType

	// Description is the human-readable description from the schema.
	Description string

	// Options contains enum values for FieldSelect type fields.
	Options []string

	// Default is the default value if specified in the schema.
	Default interface{}

	// Layer is the recommended config layer for saving (from x-layer).
	Layer config.ConfigSource

	// Priority determines the sort order (lower = higher in list).
	// 1-10 for wizard fields, 50+ for common, 100+ for advanced.
	Priority int

	// Sensitive indicates if the field contains sensitive data (from x-sensitive).
	// If true, the UI should mask the value and suggest alternatives.
	Sensitive bool

	// Wizard indicates if the field appears in the setup wizard (from x-wizard).
	Wizard bool

	// Hint provides additional guidance shown in the edit dialog (from x-hint).
	Hint string

	// Children contains nested fields for FieldObject type.
	Children []FieldMeta

	// Namespace is the schema namespace (e.g., "gemini", "tmux", empty for core).
	Namespace string

	// Required indicates if this field is required by the schema.
	Required bool

	// RefType is the $ref type name if this field references a definition.
	RefType string
}

// Label returns a human-readable label for the field.
// Uses the last path component, converted to title case.
func (f FieldMeta) Label() string {
	if len(f.Path) == 0 {
		return ""
	}
	return f.Path[len(f.Path)-1]
}

// FullPath returns the dot-separated full path including namespace.
func (f FieldMeta) FullPath() string {
	if f.Namespace != "" {
		path := f.Namespace
		for _, p := range f.Path {
			path += "." + p
		}
		return path
	}
	result := ""
	for i, p := range f.Path {
		if i > 0 {
			result += "."
		}
		result += p
	}
	return result
}

// IsWizardField returns true if this field should appear in the setup wizard.
func (f FieldMeta) IsWizardField() bool {
	return f.Wizard
}

// IsSensitive returns true if this field contains sensitive data.
func (f FieldMeta) IsSensitive() bool {
	return f.Sensitive
}

// HasChildren returns true if this is a nested object with children.
func (f FieldMeta) HasChildren() bool {
	return len(f.Children) > 0
}
