package configui

import (
	"testing"
)

func TestFormatValue(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{"nil", nil, "(unset)"},
		{"empty string", "", "(unset)"},
		{"string", "hello", "hello"},
		{"long string", "this is a very long string that should be truncated at some point to avoid display issues", "this is a very long string that should be trunc..."},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		{"int", 42, "42"},
		{"float", 3.14, "3.14"},
		{"empty map", map[string]string{}, "(empty)"},
		{"map with 1 key", map[string]string{"a": "1"}, "{1 key}"},
		{"map with 3 keys", map[string]string{"a": "1", "b": "2", "c": "3"}, "{3 keys}"},
		{"empty slice", []string{}, "(empty)"},
		{"slice with 1 item", []string{"a"}, "[1 item]"},
		{"slice with 3 items", []string{"a", "b", "c"}, "[3 items]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatValue(tt.input)
			if result != tt.expected {
				t.Errorf("FormatValue(%v) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFormatValueWithPointer(t *testing.T) {
	s := "test"
	result := FormatValue(&s)
	if result != "test" {
		t.Errorf("FormatValue(&string) = %q, want %q", result, "test")
	}

	var nilPtr *string = nil
	result = FormatValue(nilPtr)
	if result != "(unset)" {
		t.Errorf("FormatValue(nil *string) = %q, want %q", result, "(unset)")
	}
}
