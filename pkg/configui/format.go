// Package configui provides schema-driven configuration UI types and metadata.
package configui

import (
	"fmt"
	"reflect"
	"sort"
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

// FormatValuePreview returns a value string with a preview of content, limited to maxWidth.
// For maps and structs, it shows key-value pairs up to the width limit.
func FormatValuePreview(v interface{}, maxWidth int) string {
	if v == nil {
		return "(unset)"
	}

	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return "(unset)"
		}
		val = val.Elem()
		v = val.Interface()
	}

	switch val.Kind() {
	case reflect.Map:
		return formatMapPreview(val, maxWidth)
	case reflect.Struct:
		return formatStructPreview(val, maxWidth)
	case reflect.Slice, reflect.Array:
		return formatSlicePreview(val, maxWidth)
	default:
		return FormatValue(v)
	}
}

// formatMapPreview formats a map showing key-value pairs up to maxWidth.
func formatMapPreview(val reflect.Value, maxWidth int) string {
	n := val.Len()
	if n == 0 {
		return "(empty)"
	}

	// Sort keys for stable output
	keys := val.MapKeys()
	type keyPair struct {
		key    reflect.Value
		keyStr string
	}
	keyPairs := make([]keyPair, 0, len(keys))
	for _, k := range keys {
		keyPairs = append(keyPairs, keyPair{key: k, keyStr: fmt.Sprintf("%v", k.Interface())})
	}
	sort.Slice(keyPairs, func(i, j int) bool {
		return keyPairs[i].keyStr < keyPairs[j].keyStr
	})

	var parts []string
	currentLen := 1 // Starting "{"

	for _, kp := range keyPairs {
		mapVal := val.MapIndex(kp.key)
		valStr := formatPreviewValue(mapVal)
		part := kp.keyStr + ": " + valStr

		// Check if adding this part would exceed maxWidth
		// Account for ", " separator and closing "}"
		needed := len(part)
		if len(parts) > 0 {
			needed += 2 // ", "
		}
		needed += 1 // "}"

		if currentLen+needed > maxWidth && len(parts) > 0 {
			// Add ellipsis and remaining count
			remaining := n - len(parts)
			if remaining > 0 {
				parts = append(parts, fmt.Sprintf("…+%d", remaining))
			}
			break
		}

		parts = append(parts, part)
		currentLen += needed
		if len(parts) >= n {
			break
		}
	}

	if len(parts) == 0 {
		// Width too small, fall back to count
		if n == 1 {
			return "{1 key}"
		}
		return fmt.Sprintf("{%d keys}", n)
	}

	result := "{"
	for i, p := range parts {
		if i > 0 {
			result += ", "
		}
		result += p
	}
	result += "}"
	return result
}

// formatStructPreview formats a struct showing fields up to maxWidth.
func formatStructPreview(val reflect.Value, maxWidth int) string {
	t := val.Type()
	n := t.NumField()
	if n == 0 {
		return "{}"
	}

	var parts []string
	currentLen := 1 // Starting "{"

	for i := 0; i < n; i++ {
		field := t.Field(i)
		fieldVal := val.Field(i)

		// Skip unexported fields
		if field.PkgPath != "" {
			continue
		}

		// Get field name from tag or use field name
		name := getTagName(field)
		if name == "" || name == "-" {
			continue
		}

		valStr := formatPreviewValue(fieldVal)
		part := name + ": " + valStr

		// Check if adding this part would exceed maxWidth
		needed := len(part)
		if len(parts) > 0 {
			needed += 2 // ", "
		}
		needed += 1 // "}"

		if currentLen+needed > maxWidth && len(parts) > 0 {
			parts = append(parts, "…")
			break
		}

		parts = append(parts, part)
		currentLen += needed
	}

	if len(parts) == 0 {
		return "{...}"
	}

	result := "{"
	for i, p := range parts {
		if i > 0 {
			result += ", "
		}
		result += p
	}
	result += "}"
	return result
}

// formatSlicePreview formats a slice showing items up to maxWidth.
func formatSlicePreview(val reflect.Value, maxWidth int) string {
	n := val.Len()
	if n == 0 {
		return "(empty)"
	}

	var parts []string
	currentLen := 1 // Starting "["

	for i := 0; i < n; i++ {
		itemVal := val.Index(i)
		valStr := formatPreviewValue(itemVal)

		// Check if adding this part would exceed maxWidth
		needed := len(valStr)
		if len(parts) > 0 {
			needed += 2 // ", "
		}
		needed += 1 // "]"

		if currentLen+needed > maxWidth && len(parts) > 0 {
			remaining := n - len(parts)
			if remaining > 0 {
				parts = append(parts, fmt.Sprintf("…+%d", remaining))
			}
			break
		}

		parts = append(parts, valStr)
		currentLen += needed
	}

	if len(parts) == 0 {
		if n == 1 {
			return "[1 item]"
		}
		return fmt.Sprintf("[%d items]", n)
	}

	result := "["
	for i, p := range parts {
		if i > 0 {
			result += ", "
		}
		result += p
	}
	result += "]"
	return result
}

// formatPreviewValue formats a single value for preview (compact form).
func formatPreviewValue(val reflect.Value) string {
	if !val.IsValid() {
		return "nil"
	}

	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return "nil"
		}
		val = val.Elem()
	}

	switch val.Kind() {
	case reflect.String:
		s := val.String()
		if len(s) > 20 {
			return s[:17] + "…"
		}
		return s
	case reflect.Map:
		n := val.Len()
		if n == 0 {
			return "{}"
		}
		return fmt.Sprintf("{%d}", n)
	case reflect.Slice, reflect.Array:
		n := val.Len()
		if n == 0 {
			return "[]"
		}
		return fmt.Sprintf("[%d]", n)
	case reflect.Struct:
		return "{…}"
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
		s := fmt.Sprintf("%v", val.Interface())
		if len(s) > 20 {
			return s[:17] + "…"
		}
		return s
	}
}

// getTagName gets the field name from yaml/toml tag or returns the field name.
func getTagName(field reflect.StructField) string {
	if tag := field.Tag.Get("yaml"); tag != "" {
		parts := splitTag(tag)
		if parts[0] != "" {
			return parts[0]
		}
	}
	if tag := field.Tag.Get("toml"); tag != "" {
		parts := splitTag(tag)
		if parts[0] != "" {
			return parts[0]
		}
	}
	return field.Name
}

// splitTag splits a struct tag by comma.
func splitTag(tag string) []string {
	for i := 0; i < len(tag); i++ {
		if tag[i] == ',' {
			return []string{tag[:i], tag[i+1:]}
		}
	}
	return []string{tag}
}
