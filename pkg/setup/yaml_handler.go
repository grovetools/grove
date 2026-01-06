package setup

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// YAMLHandler provides utilities for reading and writing YAML files
// while preserving comments and structure using yaml.Node.
type YAMLHandler struct {
	service *Service
}

// NewYAMLHandler creates a new YAML handler
func NewYAMLHandler(service *Service) *YAMLHandler {
	return &YAMLHandler{service: service}
}

// LoadGlobalConfig loads the global grove configuration file (~/.config/grove/grove.yml)
// into a yaml.Node tree. If the file doesn't exist, returns an empty document node.
func (h *YAMLHandler) LoadGlobalConfig() (*yaml.Node, error) {
	configPath := GlobalConfigPath()
	return h.LoadYAML(configPath)
}

// LoadYAML loads a YAML file into a yaml.Node tree.
// If the file doesn't exist, returns an empty document node.
func (h *YAMLHandler) LoadYAML(path string) (*yaml.Node, error) {
	expandedPath := expandPath(path)

	data, err := os.ReadFile(expandedPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return an empty document with a mapping node
			return &yaml.Node{
				Kind: yaml.DocumentNode,
				Content: []*yaml.Node{
					{
						Kind: yaml.MappingNode,
					},
				},
			}, nil
		}
		return nil, fmt.Errorf("failed to read YAML file %s: %w", path, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("failed to parse YAML file %s: %w", path, err)
	}

	// Ensure we have a document node with content
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return &yaml.Node{
			Kind: yaml.DocumentNode,
			Content: []*yaml.Node{
				{
					Kind: yaml.MappingNode,
				},
			},
		}, nil
	}

	return &root, nil
}

// SaveGlobalConfig saves a yaml.Node tree back to the global grove configuration file.
// Respects the service's dry-run mode.
func (h *YAMLHandler) SaveGlobalConfig(root *yaml.Node) error {
	configPath := GlobalConfigPath()
	return h.SaveYAML(configPath, root)
}

// SaveYAML saves a yaml.Node tree to a file, respecting dry-run mode.
func (h *YAMLHandler) SaveYAML(path string, root *yaml.Node) error {
	expandedPath := expandPath(path)

	data, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	displayPath := AbbreviatePath(expandedPath)

	// Use the service to write the file (respects dry-run)
	if h.service.IsDryRun() {
		h.service.logger.Infof("[dry-run] Would write YAML to %s", displayPath)
		h.service.logAction(ActionUpdateYAML, fmt.Sprintf("Update %s", displayPath), expandedPath, true, nil)
		return nil
	}

	// Ensure parent directory exists
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	if err := os.WriteFile(expandedPath, data, 0644); err != nil {
		h.service.logAction(ActionUpdateYAML, fmt.Sprintf("Update %s", displayPath), expandedPath, false, err)
		return fmt.Errorf("failed to write YAML file %s: %w", path, err)
	}

	h.service.logger.Infof("Updated %s", displayPath)
	h.service.logAction(ActionUpdateYAML, fmt.Sprintf("Update %s", displayPath), expandedPath, true, nil)
	return nil
}

// GetOrCreateNode traverses the YAML tree and returns or creates a node at the given path.
// If any intermediate keys don't exist, they are created as mapping nodes.
func GetOrCreateNode(root *yaml.Node, path ...string) *yaml.Node {
	if root == nil || len(path) == 0 {
		return root
	}

	// If this is a document node, work with its content
	current := root
	if current.Kind == yaml.DocumentNode {
		if len(current.Content) == 0 {
			current.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
		}
		current = current.Content[0]
	}

	// Ensure current is a mapping node
	if current.Kind != yaml.MappingNode {
		return nil
	}

	for _, key := range path {
		found := false
		// Search for the key in the current mapping
		for i := 0; i < len(current.Content)-1; i += 2 {
			if current.Content[i].Value == key {
				current = current.Content[i+1]
				found = true
				break
			}
		}

		if !found {
			// Create the key-value pair
			keyNode := &yaml.Node{
				Kind:  yaml.ScalarNode,
				Tag:   "!!str",
				Value: key,
			}
			valueNode := &yaml.Node{
				Kind: yaml.MappingNode,
			}
			current.Content = append(current.Content, keyNode, valueNode)
			current = valueNode
		}
	}

	return current
}

// SetValue sets a value at the specified path in the YAML tree.
// The path should include the key name.
func SetValue(root *yaml.Node, value interface{}, path ...string) error {
	if len(path) == 0 {
		return fmt.Errorf("path cannot be empty")
	}

	// Get or create the parent node
	parentPath := path[:len(path)-1]
	keyName := path[len(path)-1]

	parent := GetOrCreateNode(root, parentPath...)
	if parent == nil {
		return fmt.Errorf("failed to get or create parent node at path: %v", parentPath)
	}

	// Ensure parent is a mapping node
	if parent.Kind == yaml.DocumentNode {
		if len(parent.Content) == 0 {
			parent.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
		}
		parent = parent.Content[0]
	}

	if parent.Kind != yaml.MappingNode {
		return fmt.Errorf("parent node is not a mapping node")
	}

	// Create value node
	valueNode, err := createValueNode(value)
	if err != nil {
		return err
	}

	// Look for existing key
	for i := 0; i < len(parent.Content)-1; i += 2 {
		if parent.Content[i].Value == keyName {
			// Replace existing value
			parent.Content[i+1] = valueNode
			return nil
		}
	}

	// Add new key-value pair
	keyNode := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: keyName,
	}
	parent.Content = append(parent.Content, keyNode, valueNode)
	return nil
}

// createValueNode creates a yaml.Node from a Go value
func createValueNode(value interface{}) (*yaml.Node, error) {
	switch v := value.(type) {
	case string:
		return &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: v,
		}, nil
	case int:
		return &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!int",
			Value: fmt.Sprintf("%d", v),
		}, nil
	case bool:
		return &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!bool",
			Value: fmt.Sprintf("%t", v),
		}, nil
	case []string:
		seqNode := &yaml.Node{
			Kind: yaml.SequenceNode,
		}
		for _, s := range v {
			seqNode.Content = append(seqNode.Content, &yaml.Node{
				Kind:  yaml.ScalarNode,
				Tag:   "!!str",
				Value: s,
			})
		}
		return seqNode, nil
	case map[string]interface{}:
		mapNode := &yaml.Node{
			Kind: yaml.MappingNode,
		}
		for k, val := range v {
			keyNode := &yaml.Node{
				Kind:  yaml.ScalarNode,
				Tag:   "!!str",
				Value: k,
			}
			valNode, err := createValueNode(val)
			if err != nil {
				return nil, err
			}
			mapNode.Content = append(mapNode.Content, keyNode, valNode)
		}
		return mapNode, nil
	default:
		// Try to marshal and unmarshal for complex types
		data, err := yaml.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal value: %w", err)
		}
		var node yaml.Node
		if err := yaml.Unmarshal(data, &node); err != nil {
			return nil, fmt.Errorf("failed to unmarshal value: %w", err)
		}
		if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
			return node.Content[0], nil
		}
		return &node, nil
	}
}

// GetValue retrieves a string value from the YAML tree at the specified path.
// Returns empty string if not found.
func GetValue(root *yaml.Node, path ...string) string {
	if root == nil || len(path) == 0 {
		return ""
	}

	current := root
	if current.Kind == yaml.DocumentNode {
		if len(current.Content) == 0 {
			return ""
		}
		current = current.Content[0]
	}

	for i, key := range path {
		if current.Kind != yaml.MappingNode {
			return ""
		}

		found := false
		for j := 0; j < len(current.Content)-1; j += 2 {
			if current.Content[j].Value == key {
				if i == len(path)-1 {
					// This is the last key, return the value
					return current.Content[j+1].Value
				}
				current = current.Content[j+1]
				found = true
				break
			}
		}

		if !found {
			return ""
		}
	}

	return ""
}

// HasKey checks if a key exists at the specified path
func HasKey(root *yaml.Node, path ...string) bool {
	if root == nil || len(path) == 0 {
		return false
	}

	current := root
	if current.Kind == yaml.DocumentNode {
		if len(current.Content) == 0 {
			return false
		}
		current = current.Content[0]
	}

	for i, key := range path {
		if current.Kind != yaml.MappingNode {
			return false
		}

		found := false
		for j := 0; j < len(current.Content)-1; j += 2 {
			if current.Content[j].Value == key {
				if i == len(path)-1 {
					return true
				}
				current = current.Content[j+1]
				found = true
				break
			}
		}

		if !found {
			return false
		}
	}

	return false
}
