// Package configui provides schema-driven configuration UI types and metadata.
package configui

import (
	"fmt"
	"reflect"
)

// FormatValue returns a human-readable string representation of a config value.
// It handles primitives, maps (showing key count), slices (showing item count),
// and structs.
func FormatValue(v interface{}) string {
	if v == nil {
		return "(unset)"
	}

	val := reflect.ValueOf(v)
	// Handle pointers
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return "(unset)"
		}
		val = val.Elem()
		v = val.Interface()
	}

	switch val.Kind() {
	case reflect.Map:
		n := val.Len()
		if n == 0 {
			return "(empty)"
		}
		if n == 1 {
			return "{1 key}"
		}
		return fmt.Sprintf("{%d keys}", n)
	case reflect.Slice, reflect.Array:
		n := val.Len()
		if n == 0 {
			return "(empty)"
		}
		if n == 1 {
			return "[1 item]"
		}
		return fmt.Sprintf("[%d items]", n)
	case reflect.Struct:
		// Generic struct summary
		return "{...}"
	case reflect.String:
		s := val.String()
		if s == "" {
			return "(unset)"
		}
		// Truncate long strings
		if len(s) > 50 {
			return s[:47] + "..."
		}
		return s
	case reflect.Bool:
		if val.Bool() {
			return "true"
		}
		return "false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", val.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%d", val.Uint())
	case reflect.Float32, reflect.Float64:
		return fmt.Sprintf("%g", val.Float())
	default:
		return fmt.Sprintf("%v", v)
	}
}

// FormatValueWithType returns a formatted value string, taking field type into account.
// This allows for type-specific formatting (e.g., showing array item count for FieldArray).
func FormatValueWithType(v interface{}, fieldType FieldType) string {
	if v == nil {
		return "(unset)"
	}

	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return "(unset)"
		}
		val = val.Elem()
	}

	switch fieldType {
	case FieldMap:
		if val.Kind() == reflect.Map {
			n := val.Len()
			if n == 0 {
				return "(empty)"
			}
			if n == 1 {
				return "{1 key}"
			}
			return fmt.Sprintf("{%d keys}", n)
		}
	case FieldArray:
		if val.Kind() == reflect.Slice || val.Kind() == reflect.Array {
			n := val.Len()
			if n == 0 {
				return "(empty)"
			}
			if n == 1 {
				return "[1 item]"
			}
			return fmt.Sprintf("[%d items]", n)
		}
	case FieldObject:
		if val.Kind() == reflect.Struct || val.Kind() == reflect.Map {
			return "{...}"
		}
	}

	// Fall back to generic formatting
	return FormatValue(v)
}
