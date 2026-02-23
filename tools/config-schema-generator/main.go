// config-schema-generator parses JSON Schema files from the grove ecosystem
// and generates Go code with embedded schema metadata for the config TUI.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/pkg/workspace"
)

// JSONSchema represents a simplified JSON Schema structure with x-* extensions.
type JSONSchema struct {
	Schema      string                 `json:"$schema"`
	ID          string                 `json:"$id"`
	Defs        map[string]*JSONSchema `json:"$defs"`
	Ref         string                 `json:"$ref"`
	Type        interface{}            `json:"type"` // Can be string or []string
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Properties  map[string]*JSONSchema `json:"properties"`
	Items       *JSONSchema            `json:"items"`
	Enum        []interface{}          `json:"enum"`
	Default     interface{}            `json:"default"`
	Required    []string               `json:"required"`

	// Additional properties for map types
	AdditionalProperties interface{} `json:"additionalProperties"`

	// Custom x-* extensions for UI metadata
	// Note: jsonschema library outputs these as strings, so we use interface{}
	XLayer     string      `json:"x-layer"`
	XPriority  interface{} `json:"x-priority"`  // Can be string or int from JSON
	XImportant interface{} `json:"x-important"` // Can be string or bool from JSON
	XSensitive interface{} `json:"x-sensitive"` // Can be string or bool from JSON
	XHint      string      `json:"x-hint"`

	// Unified status extensions (alpha, beta, deprecated)
	XStatus           string `json:"x-status"`             // "alpha", "beta", "deprecated"
	XStatusMessage    string `json:"x-status-message"`     // Human-readable notice
	XStatusSince      string `json:"x-status-since"`       // Version when entered this status
	XStatusTarget     string `json:"x-status-target"`      // Graduation/removal target version
	XStatusReplaces   string `json:"x-status-replaces"`    // Alpha/Beta: what this will replace
	XStatusReplacedBy string `json:"x-status-replaced-by"` // Deprecated: what to use instead

	// Legacy deprecation (for backwards compatibility parsing)
	Deprecated  bool        `json:"deprecated"`
	XDeprecated interface{} `json:"x-deprecated"` // handle bool or string "true"
}

// SchemaSource represents a schema file to process.
type SchemaSource struct {
	Path      string // Absolute path to the schema file
	Namespace string // Namespace prefix (e.g., "gemini", "tmux", empty for core)
}

// FieldInfo holds parsed field metadata for code generation.
type FieldInfo struct {
	Path        []string
	Type        string // "string", "bool", "int", "select", "array", "object", "map"
	Description string
	Options     []string
	Default     interface{}
	Layer       string // "global", "ecosystem", "project"
	Priority    int
	Sensitive   bool
	Important   bool
	Hint        string
	Namespace   string
	Required    bool
	RefType     string
	Children    []FieldInfo

	// Unified status fields (alpha, beta, stable, deprecated)
	Status           string // "alpha", "beta", "deprecated" (stable is default/absent)
	StatusMessage    string
	StatusSince      string
	StatusTarget     string
	StatusReplaces   string
	StatusReplacedBy string
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get working directory: %v", err)
	}

	ecosystemRoot, err := workspace.FindEcosystemRoot("")
	if err != nil {
		log.Fatalf("Could not find ecosystem root: %v", err)
	}
	log.Printf("Found ecosystem root: %s", ecosystemRoot)

	// Define schema sources to process
	sources := []SchemaSource{
		{Path: filepath.Join(ecosystemRoot, "core", "schema", "definitions", "base.schema.json"), Namespace: ""},
		{Path: filepath.Join(ecosystemRoot, "grove-gemini", "gemini.schema.json"), Namespace: "gemini"},
		{Path: filepath.Join(ecosystemRoot, "nav", "tmux.schema.json"), Namespace: "tmux"},
	}

	var allFields []FieldInfo

	for _, source := range sources {
		fields, err := processSchema(source)
		if err != nil {
			log.Printf("Warning: failed to process %s: %v", source.Path, err)
			continue
		}
		allFields = append(allFields, fields...)
	}

	// Sort by priority
	sort.Slice(allFields, func(i, j int) bool {
		return allFields[i].Priority < allFields[j].Priority
	})

	// Generate Go code
	code, err := generateCode(allFields)
	if err != nil {
		log.Fatalf("Failed to generate code: %v", err)
	}

	// Write to pkg/configui/schema_generated.go
	outputPath := filepath.Join(cwd, "pkg", "configui", "schema_generated.go")
	if err := os.WriteFile(outputPath, code, 0644); err != nil {
		log.Fatalf("Failed to write schema file: %v", err)
	}

	log.Printf("Successfully generated config schema with %d fields at %s", len(allFields), outputPath)
}

// processSchema loads and parses a JSON Schema file.
func processSchema(source SchemaSource) ([]FieldInfo, error) {
	data, err := os.ReadFile(source.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to read schema: %w", err)
	}

	var schema JSONSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("failed to parse schema: %w", err)
	}

	var fields []FieldInfo
	requiredSet := make(map[string]bool)
	for _, r := range schema.Required {
		requiredSet[r] = true
	}

	// Sort property names for deterministic output
	var propNames []string
	for name := range schema.Properties {
		propNames = append(propNames, name)
	}
	sort.Strings(propNames)

	for _, name := range propNames {
		prop := schema.Properties[name]
		field := extractField(name, prop, []string{name}, source.Namespace, &schema, requiredSet[name])
		if field.Priority == 0 {
			field.Priority = 1000 // Default priority for unspecified fields
		}
		fields = append(fields, field)
	}

	return fields, nil
}

// extractField extracts field metadata from a JSON Schema property.
func extractField(name string, prop *JSONSchema, path []string, namespace string, root *JSONSchema, required bool) FieldInfo {
	field := FieldInfo{
		Path:        path,
		Description: prop.Description,
		Layer:       prop.XLayer,
		Priority:    parseIntValue(prop.XPriority),
		Sensitive:   parseBoolValue(prop.XSensitive),
		Important:   parseBoolValue(prop.XImportant),
		Hint:        prop.XHint,
		Namespace:   namespace,
		Required:    required,
		Default:     prop.Default,

		// Unified status fields
		Status:           prop.XStatus,
		StatusMessage:    prop.XStatusMessage,
		StatusSince:      prop.XStatusSince,
		StatusTarget:     prop.XStatusTarget,
		StatusReplaces:   prop.XStatusReplaces,
		StatusReplacedBy: prop.XStatusReplacedBy,
	}

	// Handle legacy deprecated flag - convert to unified status
	if field.Status == "" && (prop.Deprecated || parseBoolValue(prop.XDeprecated)) {
		field.Status = "deprecated"
	}

	// Handle $ref
	if prop.Ref != "" {
		field.RefType = extractRefType(prop.Ref)
		// Resolve the reference to get more details
		if resolved := resolveRef(prop.Ref, root); resolved != nil {
			// Inherit description if not set
			if field.Description == "" {
				field.Description = resolved.Description
			}
			// Process children for object types
			if len(resolved.Properties) > 0 {
				field.Type = "object"
				requiredSet := make(map[string]bool)
				for _, r := range resolved.Required {
					requiredSet[r] = true
				}
				// Sort property names for deterministic output
				var childNames []string
				for childName := range resolved.Properties {
					childNames = append(childNames, childName)
				}
				sort.Strings(childNames)

				for _, childName := range childNames {
					childProp := resolved.Properties[childName]
					childPath := append([]string{}, path...)
					childPath = append(childPath, childName)
					child := extractField(childName, childProp, childPath, namespace, root, requiredSet[childName])
					field.Children = append(field.Children, child)
				}
				// Sort children by priority, then by name for stability
				sort.Slice(field.Children, func(i, j int) bool {
					if field.Children[i].Priority != field.Children[j].Priority {
						return field.Children[i].Priority < field.Children[j].Priority
					}
					return field.Children[i].Path[len(field.Children[i].Path)-1] < field.Children[j].Path[len(field.Children[j].Path)-1]
				})
				return field
			}
		}
	}

	// Determine field type
	schemaType := getSchemaType(prop)

	switch schemaType {
	case "string":
		if len(prop.Enum) > 0 {
			field.Type = "select"
			for _, e := range prop.Enum {
				if s, ok := e.(string); ok {
					field.Options = append(field.Options, s)
				}
			}
		} else {
			field.Type = "string"
		}
	case "boolean":
		field.Type = "bool"
	case "integer", "number":
		field.Type = "int"
	case "array":
		field.Type = "array"
	case "object":
		// Check for additionalProperties (map type)
		if prop.AdditionalProperties != nil {
			field.Type = "map"
		} else if len(prop.Properties) > 0 {
			field.Type = "object"
			requiredSet := make(map[string]bool)
			for _, r := range prop.Required {
				requiredSet[r] = true
			}
			// Sort property names for deterministic output
			var childNames []string
			for childName := range prop.Properties {
				childNames = append(childNames, childName)
			}
			sort.Strings(childNames)

			for _, childName := range childNames {
				childProp := prop.Properties[childName]
				childPath := append([]string{}, path...)
				childPath = append(childPath, childName)
				child := extractField(childName, childProp, childPath, namespace, root, requiredSet[childName])
				field.Children = append(field.Children, child)
			}
			// Sort children by priority, then by name for stability
			sort.Slice(field.Children, func(i, j int) bool {
				if field.Children[i].Priority != field.Children[j].Priority {
					return field.Children[i].Priority < field.Children[j].Priority
				}
				return field.Children[i].Path[len(field.Children[i].Path)-1] < field.Children[j].Path[len(field.Children[j].Path)-1]
			})
		} else {
			field.Type = "object"
		}
	default:
		field.Type = "string"
	}

	return field
}

// getSchemaType extracts the type string from a schema type field.
func getSchemaType(prop *JSONSchema) string {
	if prop.Type == nil {
		return ""
	}

	switch t := prop.Type.(type) {
	case string:
		return t
	case []interface{}:
		// For union types like ["string", "null"], return the first non-null type
		for _, v := range t {
			if s, ok := v.(string); ok && s != "null" {
				return s
			}
		}
	}
	return ""
}

// extractRefType extracts the type name from a $ref string.
func extractRefType(ref string) string {
	// Handle "#/$defs/TypeName" format
	if strings.HasPrefix(ref, "#/$defs/") {
		return strings.TrimPrefix(ref, "#/$defs/")
	}
	return ref
}

// resolveRef resolves a $ref to its schema definition.
func resolveRef(ref string, root *JSONSchema) *JSONSchema {
	typeName := extractRefType(ref)
	if root.Defs != nil {
		return root.Defs[typeName]
	}
	return nil
}

// generateCode generates Go source code from the extracted fields.
func generateCode(fields []FieldInfo) ([]byte, error) {
	var buf bytes.Buffer

	buf.WriteString("// Code generated by tools/config-schema-generator. DO NOT EDIT.\n\n")
	buf.WriteString("package configui\n\n")
	buf.WriteString("import \"github.com/grovetools/core/config\"\n\n")

	// Generate SchemaFields slice
	buf.WriteString("// SchemaFields contains all config fields sorted by priority.\n")
	buf.WriteString("var SchemaFields = []FieldMeta{\n")

	for _, field := range fields {
		writeFieldMeta(&buf, field, "\t")
	}

	buf.WriteString("}\n\n")

	// Generate FieldsByPath map
	buf.WriteString("// FieldsByPath provides O(1) lookup by full path.\n")
	buf.WriteString("var FieldsByPath = map[string]*FieldMeta{\n")

	for i, field := range fields {
		fullPath := field.fullPath()
		buf.WriteString(fmt.Sprintf("\t%q: &SchemaFields[%d],\n", fullPath, i))
	}

	buf.WriteString("}\n\n")

	// Generate ImportantFields slice
	buf.WriteString("// ImportantFields contains only fields marked as important/key configuration options.\n")
	buf.WriteString("var ImportantFields = []*FieldMeta{\n")

	for i, field := range fields {
		if field.Important {
			buf.WriteString(fmt.Sprintf("\t&SchemaFields[%d],\n", i))
		}
	}

	buf.WriteString("}\n")

	return format.Source(buf.Bytes())
}

// writeFieldMeta writes a FieldMeta struct literal to the buffer.
func writeFieldMeta(buf *bytes.Buffer, field FieldInfo, indent string) {
	buf.WriteString(indent + "{\n")

	// Path
	buf.WriteString(indent + "\tPath: []string{")
	for i, p := range field.Path {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(fmt.Sprintf("%q", p))
	}
	buf.WriteString("},\n")

	// Type
	buf.WriteString(indent + fmt.Sprintf("\tType: %s,\n", fieldTypeConstant(field.Type)))

	// Description
	if field.Description != "" {
		buf.WriteString(indent + fmt.Sprintf("\tDescription: %q,\n", field.Description))
	}

	// Options
	if len(field.Options) > 0 {
		buf.WriteString(indent + "\tOptions: []string{")
		for i, opt := range field.Options {
			if i > 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(fmt.Sprintf("%q", opt))
		}
		buf.WriteString("},\n")
	}

	// Layer
	if field.Layer != "" {
		buf.WriteString(indent + fmt.Sprintf("\tLayer: %s,\n", layerConstant(field.Layer)))
	}

	// Priority
	if field.Priority > 0 {
		buf.WriteString(indent + fmt.Sprintf("\tPriority: %d,\n", field.Priority))
	}

	// Sensitive
	if field.Sensitive {
		buf.WriteString(indent + "\tSensitive: true,\n")
	}

	// Important
	if field.Important {
		buf.WriteString(indent + "\tImportant: true,\n")
	}

	// Hint
	if field.Hint != "" {
		buf.WriteString(indent + fmt.Sprintf("\tHint: %q,\n", field.Hint))
	}

	// Namespace
	if field.Namespace != "" {
		buf.WriteString(indent + fmt.Sprintf("\tNamespace: %q,\n", field.Namespace))
	}

	// Required
	if field.Required {
		buf.WriteString(indent + "\tRequired: true,\n")
	}

	// RefType
	if field.RefType != "" {
		buf.WriteString(indent + fmt.Sprintf("\tRefType: %q,\n", field.RefType))
	}

	// Unified status fields
	if field.Status != "" {
		buf.WriteString(indent + fmt.Sprintf("\tStatus: %s,\n", statusConstant(field.Status)))
	}
	if field.StatusMessage != "" {
		buf.WriteString(indent + fmt.Sprintf("\tStatusMessage: %q,\n", field.StatusMessage))
	}
	if field.StatusSince != "" {
		buf.WriteString(indent + fmt.Sprintf("\tStatusSince: %q,\n", field.StatusSince))
	}
	if field.StatusTarget != "" {
		buf.WriteString(indent + fmt.Sprintf("\tStatusTarget: %q,\n", field.StatusTarget))
	}
	if field.StatusReplaces != "" {
		buf.WriteString(indent + fmt.Sprintf("\tStatusReplaces: %q,\n", field.StatusReplaces))
	}
	if field.StatusReplacedBy != "" {
		buf.WriteString(indent + fmt.Sprintf("\tStatusReplacedBy: %q,\n", field.StatusReplacedBy))
	}

	// Children
	if len(field.Children) > 0 {
		buf.WriteString(indent + "\tChildren: []FieldMeta{\n")
		for _, child := range field.Children {
			writeFieldMeta(buf, child, indent+"\t\t")
		}
		buf.WriteString(indent + "\t},\n")
	}

	buf.WriteString(indent + "},\n")
}

// fieldTypeConstant returns the Go constant name for a field type.
func fieldTypeConstant(t string) string {
	switch t {
	case "string":
		return "FieldString"
	case "bool":
		return "FieldBool"
	case "int":
		return "FieldInt"
	case "select":
		return "FieldSelect"
	case "array":
		return "FieldArray"
	case "object":
		return "FieldObject"
	case "map":
		return "FieldMap"
	default:
		return "FieldString"
	}
}

// layerConstant returns the Go constant name for a config layer.
func layerConstant(layer string) string {
	switch layer {
	case "global":
		return "config.SourceGlobal"
	case "ecosystem":
		return "config.SourceEcosystem"
	case "project":
		return "config.SourceProject"
	default:
		return "config.SourceDefault"
	}
}

// statusConstant returns the Go constant name for a field status.
func statusConstant(status string) string {
	switch status {
	case "alpha":
		return "StatusAlpha"
	case "beta":
		return "StatusBeta"
	case "deprecated":
		return "StatusDeprecated"
	default:
		return "StatusStable"
	}
}

// fullPath returns the full dot-separated path including namespace.
func (f FieldInfo) fullPath() string {
	if f.Namespace != "" {
		path := f.Namespace
		for _, p := range f.Path {
			path += "." + p
		}
		return path
	}
	return strings.Join(f.Path, ".")
}

// parseIntValue converts an interface{} to int, handling string values.
func parseIntValue(v interface{}) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		var i int
		fmt.Sscanf(val, "%d", &i)
		return i
	default:
		return 0
	}
}

// parseBoolValue converts an interface{} to bool, handling string values.
func parseBoolValue(v interface{}) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val == "true"
	default:
		return false
	}
}
