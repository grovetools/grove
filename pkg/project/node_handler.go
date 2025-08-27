package project

import (
	"fmt"
)

type NodeHandler struct{}

func NewNodeHandler() *NodeHandler {
	return &NodeHandler{}
}

func (h *NodeHandler) HasProjectFile(workspacePath string) bool {
	// Check for package.json
	return false
}

func (h *NodeHandler) ParseDependencies(workspacePath string) ([]Dependency, error) {
	return nil, fmt.Errorf("Node.js handler not implemented yet")
}

func (h *NodeHandler) UpdateDependency(workspacePath string, dep Dependency) error {
	return fmt.Errorf("Node.js handler not implemented yet")
}

func (h *NodeHandler) GetVersion(workspacePath string) (string, error) {
	return "", fmt.Errorf("Node.js handler not implemented yet")
}

func (h *NodeHandler) SetVersion(workspacePath string, version string) error {
	return fmt.Errorf("Node.js handler not implemented yet")
}

func (h *NodeHandler) GetBuildCommand() string  { return "make build" }
func (h *NodeHandler) GetTestCommand() string   { return "make test" }
func (h *NodeHandler) GetVerifyCommand() string { return "make verify" }
