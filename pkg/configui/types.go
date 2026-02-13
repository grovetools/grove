// Package configui provides schema-driven configuration UI types and metadata.
package configui

import (
	"fmt"

	"github.com/grovetools/core/config"
)

// ViewMode controls whether we show all fields or only configured ones.
type ViewMode string

const (
	ViewAll        ViewMode = "all"
	ViewConfigured ViewMode = "configured"
)

// MaturityFilter controls which maturity levels are displayed.
type MaturityFilter string

const (
	MaturityStable       MaturityFilter = "stable"        // stable only
	MaturityExperimental MaturityFilter = "+experimental" // stable + alpha/beta
	MaturityDeprecated   MaturityFilter = "+deprecated"   // stable + deprecated
	MaturityAll          MaturityFilter = "all"           // everything
)

// SortMode controls the order of fields in the TUI.
type SortMode string

const (
	SortConfiguredFirst SortMode = "configured-first"
	SortPriority        SortMode = "priority"
	SortAlpha           SortMode = "alpha"
)

// FieldStatus represents the lifecycle stage of a config field.
type FieldStatus string

const (
	StatusAlpha      FieldStatus = "alpha"
	StatusBeta       FieldStatus = "beta"
	StatusStable     FieldStatus = "stable" // default
	StatusDeprecated FieldStatus = "deprecated"
)

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

	// Unified Status Fields (alpha, beta, stable, deprecated)
	Status           FieldStatus `json:"status,omitempty"`
	StatusMessage    string      `json:"status_message,omitempty"`
	StatusSince      string      `json:"status_since,omitempty"`
	StatusTarget     string      `json:"status_target,omitempty"`
	StatusReplaces   string      `json:"status_replaces,omitempty"`
	StatusReplacedBy string      `json:"status_replaced_by,omitempty"`
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

// IsStable returns true if the field is production-ready.
func (f FieldMeta) IsStable() bool {
	return f.Status == "" || f.Status == StatusStable
}

// IsNonStable returns true for alpha, beta, or deprecated fields.
func (f FieldMeta) IsNonStable() bool {
	return f.Status == StatusAlpha || f.Status == StatusBeta || f.Status == StatusDeprecated
}

// IsDeprecated returns true if this field is deprecated.
func (f FieldMeta) IsDeprecated() bool {
	return f.Status == StatusDeprecated
}

// StatusBadge returns the display badge for the status.
func (f FieldMeta) StatusBadge() string {
	switch f.Status {
	case StatusAlpha:
		return "α ALPHA"
	case StatusBeta:
		return "β BETA"
	case StatusDeprecated:
		return "⚠ DEPRECATED"
	default:
		return ""
	}
}

// StatusNotice returns a formatted status message for the UI.
func (f FieldMeta) StatusNotice() string {
	if f.IsStable() {
		return ""
	}

	msg := f.StatusMessage
	if f.StatusSince != "" {
		switch f.Status {
		case StatusAlpha, StatusBeta:
			msg += fmt.Sprintf(" Added in %s.", f.StatusSince)
		case StatusDeprecated:
			msg += fmt.Sprintf(" Deprecated in %s.", f.StatusSince)
		}
	}
	if f.StatusTarget != "" {
		switch f.Status {
		case StatusAlpha:
			msg += fmt.Sprintf(" Expected beta in %s.", f.StatusTarget)
		case StatusBeta:
			msg += fmt.Sprintf(" Expected stable in %s.", f.StatusTarget)
		case StatusDeprecated:
			msg += fmt.Sprintf(" Will be removed in %s.", f.StatusTarget)
		}
	}
	if f.StatusReplaces != "" {
		msg += fmt.Sprintf(" Will replace '%s'.", f.StatusReplaces)
	}
	if f.StatusReplacedBy != "" {
		msg += fmt.Sprintf(" Use '%s' instead.", f.StatusReplacedBy)
	}
	return msg
}
