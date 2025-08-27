package templates

import (
	"strings"
	"testing"
)

func TestNewManager(t *testing.T) {
	m := NewManager()

	if m == nil {
		t.Fatal("NewManager() returned nil")
	}

	if m.templates == nil {
		t.Fatal("NewManager() templates map is nil")
	}

	// Templates are no longer embedded, manager is kept for backward compatibility
	// The templates map should be empty since we're using external templates now
	if len(m.templates) != 0 {
		t.Errorf("Expected empty templates map since we use external templates, got %d templates", len(m.templates))
	}
}

// The following tests are commented out since we no longer use embedded templates
// and the Manager.GenerateFile method is deprecated in favor of the external template system

/*
func TestGenerateFile(t *testing.T) {
	// Deprecated - we now use external templates
}

func TestGenerateFileWithSubdirectories(t *testing.T) {
	// Deprecated - we now use external templates
}
*/

func TestGenerateFileNonExistentTemplate(t *testing.T) {
	m := NewManager()

	// This should always fail now since no templates are loaded
	err := m.GenerateFile("non-existent.tmpl", "/tmp/output.txt", TemplateData{})
	if err == nil {
		t.Error("Expected error for any template since manager no longer loads templates, got nil")
	}

	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Expected 'not found' error, got: %v", err)
	}
}

func TestTemplateDataUppercase(t *testing.T) {
	tests := []struct {
		alias    string
		expected string
	}{
		{"cx", "CX"},
		{"grove", "GROVE"},
		{"nb", "NB"},
		{"test", "TEST"},
	}

	for _, tt := range tests {
		t.Run(tt.alias, func(t *testing.T) {
			data := TemplateData{
				BinaryAlias:      tt.alias,
				BinaryAliasUpper: strings.ToUpper(tt.alias),
			}

			if data.BinaryAliasUpper != tt.expected {
				t.Errorf("BinaryAliasUpper = %s, want %s", data.BinaryAliasUpper, tt.expected)
			}
		})
	}
}
