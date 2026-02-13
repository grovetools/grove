// Package configui provides schema-driven configuration UI types and metadata.
package configui

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/grovetools/core/config"
)

// ConfigNode represents a node in the configuration tree.
// It holds both schema information (via FieldMeta) and runtime value information.
type ConfigNode struct {
	Field        FieldMeta
	Value        interface{}         // Final merged value
	LayerValues  LayeredValue        // Values at each layer
	ActiveSource config.ConfigSource // Which layer provided the value
	Depth        int
	Collapsed    bool
	Children     []*ConfigNode
	Parent       *ConfigNode

	// Key is used for map entries (the map key) or array indices.
	// For regular schema fields, this is empty.
	Key string

	// IsDynamic indicates if this node was dynamically created from map keys
	// rather than defined in the schema.
	IsDynamic bool
}

// LayeredValue holds the values for a node across all config layers.
type LayeredValue struct {
	Default        interface{}
	Global         interface{}
	GlobalOverride interface{}
	EnvOverlay     interface{}
	Ecosystem      interface{}
	Project        interface{}
	Override       interface{} // From grove.override.toml
}

// HasValue returns true if the node has a non-nil value.
func (n *ConfigNode) HasValue() bool {
	return n.Value != nil
}

// IsContainer returns true if the node can have children (Map, Object, or Array).
func (n *ConfigNode) IsContainer() bool {
	return n.Field.Type == FieldMap || n.Field.Type == FieldObject || n.Field.Type == FieldArray
}

// IsExpandable returns true if the node has children and can be expanded/collapsed.
func (n *ConfigNode) IsExpandable() bool {
	return len(n.Children) > 0
}

// DisplayKey returns the appropriate key/label for display.
// For dynamic map entries, returns the Key; otherwise returns the field label.
func (n *ConfigNode) DisplayKey() string {
	if n.Key != "" {
		return n.Key
	}
	return n.Field.Label()
}

// BuildTree constructs a tree of ConfigNodes from the schema and layered config.
func BuildTree(schema []FieldMeta, layered *config.LayeredConfig) []*ConfigNode {
	var nodes []*ConfigNode
	for _, field := range schema {
		node := buildNode(field, layered, nil, 0, nil)
		if node != nil {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

// buildNode recursively builds a ConfigNode from a FieldMeta and config values.
func buildNode(field FieldMeta, layered *config.LayeredConfig, parent *ConfigNode, depth int, pathPrefix []string) *ConfigNode {
	node := &ConfigNode{
		Field:     field,
		Depth:     depth,
		Parent:    parent,
		Collapsed: depth > 0, // Start collapsed except root level
	}

	// Calculate the full path for value lookup
	path := field.Path
	if field.Namespace != "" && len(pathPrefix) == 0 {
		path = append([]string{field.Namespace}, field.Path...)
	} else if len(pathPrefix) > 0 {
		path = append(pathPrefix, field.Path...)
	}

	// Get values from each layer
	if layered.Final != nil {
		node.Value = getConfigValueInterface(layered.Final, path)
	}
	if layered.Default != nil {
		node.LayerValues.Default = getConfigValueInterface(layered.Default, path)
	}
	if layered.Global != nil {
		node.LayerValues.Global = getConfigValueInterface(layered.Global, path)
	}
	if layered.GlobalOverride != nil && layered.GlobalOverride.Config != nil {
		node.LayerValues.GlobalOverride = getConfigValueInterface(layered.GlobalOverride.Config, path)
	}
	if layered.EnvOverlay != nil && layered.EnvOverlay.Config != nil {
		node.LayerValues.EnvOverlay = getConfigValueInterface(layered.EnvOverlay.Config, path)
	}
	if layered.Ecosystem != nil {
		node.LayerValues.Ecosystem = getConfigValueInterface(layered.Ecosystem, path)
	}
	if layered.Project != nil {
		node.LayerValues.Project = getConfigValueInterface(layered.Project, path)
	}
	// Check override files (use the last one as highest priority)
	if len(layered.Overrides) > 0 {
		for i := len(layered.Overrides) - 1; i >= 0; i-- {
			if layered.Overrides[i].Config != nil {
				val := getConfigValueInterface(layered.Overrides[i].Config, path)
				if val != nil && !isEmptyValue(val) {
					node.LayerValues.Override = val
					break
				}
			}
		}
	}

	// Determine active source (highest priority wins)
	node.ActiveSource = determineActiveSource(node.LayerValues)

	// Build children based on field type
	switch field.Type {
	case FieldObject:
		// Static children defined in schema
		if len(field.Children) > 0 {
			for _, childField := range field.Children {
				childNode := buildNode(childField, layered, node, depth+1, nil)
				if childNode != nil {
					node.Children = append(node.Children, childNode)
				}
			}
		}

	case FieldMap:
		// Dynamic children from map keys in the final value
		if node.Value != nil {
			node.Children = buildMapChildren(node, layered, depth+1)
		}

	case FieldArray:
		// Dynamic children from array indices in the final value
		if node.Value != nil {
			node.Children = buildArrayChildren(node, layered, depth+1)
		}
	}

	return node
}

// buildMapChildren creates child nodes for each key in a map value.
func buildMapChildren(parent *ConfigNode, layered *config.LayeredConfig, depth int) []*ConfigNode {
	val := reflect.ValueOf(parent.Value)
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}
	if val.Kind() != reflect.Map {
		return nil
	}

	var children []*ConfigNode

	// Collect and sort keys for stable ordering
	keys := val.MapKeys()
	var sortedKeys []string
	for _, k := range keys {
		sortedKeys = append(sortedKeys, k.String())
	}
	sort.Strings(sortedKeys)

	for _, k := range sortedKeys {
		mapVal := val.MapIndex(reflect.ValueOf(k))
		if !mapVal.IsValid() {
			continue
		}

		childValue := mapVal.Interface()

		// Determine the field type for the child based on the value
		childFieldType := inferFieldType(childValue)

		childField := FieldMeta{
			Path:        []string{k},
			Type:        childFieldType,
			Description: "",
			Namespace:   parent.Field.Namespace,
		}

		childNode := &ConfigNode{
			Field:        childField,
			Value:        childValue,
			Depth:        depth,
			Parent:       parent,
			Key:          k,
			IsDynamic:    true,
			Collapsed:    true,
			ActiveSource: parent.ActiveSource, // Inherit from parent for simplicity
		}

		// If the map value is a struct or map, create children for it
		if childFieldType == FieldObject {
			childNode.Children = buildStructChildren(childNode, childValue, depth+1)
		}

		children = append(children, childNode)
	}

	return children
}

// buildArrayChildren creates child nodes for each item in an array value.
func buildArrayChildren(parent *ConfigNode, layered *config.LayeredConfig, depth int) []*ConfigNode {
	val := reflect.ValueOf(parent.Value)
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}
	if val.Kind() != reflect.Slice && val.Kind() != reflect.Array {
		return nil
	}

	var children []*ConfigNode

	for i := 0; i < val.Len(); i++ {
		itemVal := val.Index(i)
		if !itemVal.IsValid() {
			continue
		}

		itemValue := itemVal.Interface()
		childFieldType := inferFieldType(itemValue)

		childField := FieldMeta{
			Path:        []string{},
			Type:        childFieldType,
			Description: "",
			Namespace:   parent.Field.Namespace,
		}

		childNode := &ConfigNode{
			Field:        childField,
			Value:        itemValue,
			Depth:        depth,
			Parent:       parent,
			Key:          fmt.Sprintf("[%d]", i),
			IsDynamic:    true,
			Collapsed:    true,
			ActiveSource: parent.ActiveSource,
		}

		// If the array item is a struct or map, create children for it
		if childFieldType == FieldObject {
			childNode.Children = buildStructChildren(childNode, itemValue, depth+1)
		}

		children = append(children, childNode)
	}

	return children
}

// buildStructChildren creates child nodes for struct fields.
func buildStructChildren(parent *ConfigNode, structValue interface{}, depth int) []*ConfigNode {
	val := reflect.ValueOf(structValue)
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}

	var children []*ConfigNode

	switch val.Kind() {
	case reflect.Struct:
		t := val.Type()
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			fieldVal := val.Field(i)

			// Get the field name from YAML/TOML tag or use field name
			fieldName := getFieldName(field)
			if fieldName == "" || fieldName == "-" {
				continue
			}

			childValue := fieldVal.Interface()
			childFieldType := inferFieldType(childValue)

			childField := FieldMeta{
				Path:        []string{fieldName},
				Type:        childFieldType,
				Description: "",
				Namespace:   parent.Field.Namespace,
			}

			childNode := &ConfigNode{
				Field:        childField,
				Value:        childValue,
				Depth:        depth,
				Parent:       parent,
				Key:          fieldName,
				IsDynamic:    true,
				Collapsed:    true,
				ActiveSource: parent.ActiveSource,
			}

			children = append(children, childNode)
		}

	case reflect.Map:
		// Handle map[string]interface{} or similar
		keys := val.MapKeys()
		var sortedKeys []string
		for _, k := range keys {
			sortedKeys = append(sortedKeys, k.String())
		}
		sort.Strings(sortedKeys)

		for _, k := range sortedKeys {
			mapVal := val.MapIndex(reflect.ValueOf(k))
			if !mapVal.IsValid() {
				continue
			}

			childValue := mapVal.Interface()
			childFieldType := inferFieldType(childValue)

			childField := FieldMeta{
				Path:        []string{k},
				Type:        childFieldType,
				Description: "",
				Namespace:   parent.Field.Namespace,
			}

			childNode := &ConfigNode{
				Field:        childField,
				Value:        childValue,
				Depth:        depth,
				Parent:       parent,
				Key:          k,
				IsDynamic:    true,
				Collapsed:    true,
				ActiveSource: parent.ActiveSource,
			}

			children = append(children, childNode)
		}
	}

	return children
}

// inferFieldType determines the FieldType based on a Go value.
func inferFieldType(v interface{}) FieldType {
	if v == nil {
		return FieldString
	}

	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return FieldString
		}
		val = val.Elem()
	}

	switch val.Kind() {
	case reflect.Map:
		return FieldMap
	case reflect.Struct:
		return FieldObject
	case reflect.Slice, reflect.Array:
		return FieldArray
	case reflect.Bool:
		return FieldBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return FieldInt
	default:
		return FieldString
	}
}

// getFieldName extracts the field name from YAML/TOML tags or returns the field name.
func getFieldName(field reflect.StructField) string {
	// Check YAML tag first
	if tag := field.Tag.Get("yaml"); tag != "" {
		parts := strings.Split(tag, ",")
		if parts[0] != "" {
			return parts[0]
		}
	}
	// Check TOML tag
	if tag := field.Tag.Get("toml"); tag != "" {
		parts := strings.Split(tag, ",")
		if parts[0] != "" {
			return parts[0]
		}
	}
	return field.Name
}

// determineActiveSource returns the highest-priority layer that has a value.
// Priority order (highest to lowest): Override > Project > Ecosystem > EnvOverlay > GlobalOverride > Global > Default
func determineActiveSource(lv LayeredValue) config.ConfigSource {
	if lv.Override != nil && !isEmptyValue(lv.Override) {
		return config.SourceOverride
	}
	if lv.Project != nil && !isEmptyValue(lv.Project) {
		return config.SourceProject
	}
	if lv.Ecosystem != nil && !isEmptyValue(lv.Ecosystem) {
		return config.SourceEcosystem
	}
	if lv.EnvOverlay != nil && !isEmptyValue(lv.EnvOverlay) {
		return config.SourceEnvOverlay
	}
	if lv.GlobalOverride != nil && !isEmptyValue(lv.GlobalOverride) {
		return config.SourceGlobalOverride
	}
	if lv.Global != nil && !isEmptyValue(lv.Global) {
		return config.SourceGlobal
	}
	return config.SourceDefault
}

// isEmptyValue checks if a value is considered "empty" (zero value or empty string).
func isEmptyValue(v interface{}) bool {
	if v == nil {
		return true
	}
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return true
		}
		val = val.Elem()
	}

	switch val.Kind() {
	case reflect.String:
		return val.String() == ""
	case reflect.Map, reflect.Slice, reflect.Array:
		return val.Len() == 0
	case reflect.Bool:
		return false // false is a valid value, not "empty"
	default:
		return false
	}
}

// Flatten returns a list of visible nodes based on collapsed state.
// Only visible (expanded) nodes are included in the result.
func Flatten(nodes []*ConfigNode) []*ConfigNode {
	var result []*ConfigNode
	for _, node := range nodes {
		result = append(result, node)
		if !node.Collapsed && len(node.Children) > 0 {
			result = append(result, Flatten(node.Children)...)
		}
	}
	return result
}

// ToggleNode toggles the collapsed state of a node.
func ToggleNode(node *ConfigNode) {
	if node.IsExpandable() {
		node.Collapsed = !node.Collapsed
	}
}

// ExpandAll expands all nodes in the tree.
func ExpandAll(nodes []*ConfigNode) {
	for _, node := range nodes {
		node.Collapsed = false
		if len(node.Children) > 0 {
			ExpandAll(node.Children)
		}
	}
}

// CollapseAll collapses all expandable nodes in the tree including root level.
func CollapseAll(nodes []*ConfigNode) {
	for _, node := range nodes {
		if node.IsExpandable() {
			node.Collapsed = true
		}
		if len(node.Children) > 0 {
			CollapseAll(node.Children)
		}
	}
}

// FindParentIndex returns the index of the parent node in a flattened list.
func FindParentIndex(flatNodes []*ConfigNode, node *ConfigNode) int {
	if node.Parent == nil {
		return -1
	}
	for i, n := range flatNodes {
		if n == node.Parent {
			return i
		}
	}
	return -1
}

// getConfigValueInterface extracts a value from a Config struct using a path,
// returning the raw interface{} rather than a string.
func getConfigValueInterface(cfg *config.Config, path []string) interface{} {
	if cfg == nil || len(path) == 0 {
		return nil
	}

	v := reflect.ValueOf(cfg).Elem()

	for i, key := range path {
		field := findFieldByTag(v, key)
		if !field.IsValid() {
			// Check extensions map for unknown fields
			if i == 0 && cfg.Extensions != nil {
				if ext, ok := cfg.Extensions[key]; ok {
					return getNestedMapValueInterface(ext, path[1:])
				}
			}
			return nil
		}

		if i == len(path)-1 {
			// Return the interface value
			if field.Kind() == reflect.Ptr && !field.IsNil() {
				return field.Elem().Interface()
			}
			if field.CanInterface() {
				return field.Interface()
			}
			return nil
		}

		// Navigate deeper (handle pointer types)
		if field.Kind() == reflect.Ptr {
			if field.IsNil() {
				return nil
			}
			field = field.Elem()
		}
		v = field
	}

	return nil
}

// findFieldByTag finds a struct field by TOML/YAML tag or field name.
func findFieldByTag(v reflect.Value, key string) reflect.Value {
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return reflect.Value{}
	}

	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// Check TOML tag
		if tag := field.Tag.Get("toml"); tag != "" {
			tagName := strings.Split(tag, ",")[0]
			if tagName == key {
				return v.Field(i)
			}
		}

		// Check YAML tag
		if tag := field.Tag.Get("yaml"); tag != "" {
			tagName := strings.Split(tag, ",")[0]
			if tagName == key {
				return v.Field(i)
			}
		}

		// Check field name (case insensitive)
		if strings.EqualFold(field.Name, key) {
			return v.Field(i)
		}
	}

	return reflect.Value{}
}

// getNestedMapValueInterface extracts a value from a nested map using a path.
func getNestedMapValueInterface(v interface{}, path []string) interface{} {
	if len(path) == 0 {
		return v
	}

	m, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}

	if val, ok := m[path[0]]; ok {
		return getNestedMapValueInterface(val, path[1:])
	}
	return nil
}

// FilterSchema returns a subset of fields belonging to the specified layer.
// This is used by tabbed config pages to show only fields relevant to each layer.
func FilterSchema(schema []FieldMeta, layer config.ConfigSource) []FieldMeta {
	var filtered []FieldMeta
	for _, f := range schema {
		if f.Layer == layer {
			filtered = append(filtered, f)
		}
	}
	return filtered
}

