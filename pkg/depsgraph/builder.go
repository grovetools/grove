package depsgraph

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/grove/pkg/project"
	"github.com/sirupsen/logrus"
)

// Builder constructs a dependency graph for all workspaces
type Builder struct {
	workspaces      []string
	projectRegistry *project.Registry
	logger          *logrus.Logger
}

// NewBuilder creates a new dependency graph builder
func NewBuilder(workspaces []string, logger *logrus.Logger) *Builder {
	return &Builder{
		workspaces:      workspaces,
		projectRegistry: project.NewRegistry(),
		logger:          logger,
	}
}

// Build constructs the dependency graph
func (b *Builder) Build() (*Graph, error) {
	graph := NewGraph()
	modulePathToName := make(map[string]string)

	// First pass: collect all modules and determine their types
	for _, ws := range b.workspaces {
		wsName := filepath.Base(ws)

		// Find and load grove config (supports .yml, .yaml, .toml)
		configPath, err := config.FindConfigFile(ws)
		if err != nil {
			b.logger.WithError(err).Warnf("No config found for %s, skipping", wsName)
			continue
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			b.logger.WithError(err).Warnf("Failed to load config for %s, skipping", wsName)
			continue
		}

		// Determine project type
		projectType := b.getProjectType(cfg)

		// Get appropriate handler
		handler, err := b.projectRegistry.Get(projectType)
		if err != nil {
			b.logger.WithError(err).Warnf("No handler for project type %s in %s, skipping", projectType, wsName)
			continue
		}

		// Check if this project type has a project file
		if !handler.HasProjectFile(ws) {
			b.logger.Debugf("No project file found for %s (type: %s), skipping", wsName, projectType)
			continue
		}

		// For Go projects, we need to get the module path
		var modulePath string
		if projectType == project.TypeGo {
			// Extract module path from go.mod parsing
			// This is a bit hacky, but we need the module path for Go projects
			// In a real implementation, we might extend the handler interface
			modulePath = b.getGoModulePath(ws)
		}

		node := &Node{
			Name: wsName,
			Path: modulePath, // For Go projects, this is the module path; empty for others
			Dir:  ws,
			Deps: []string{},
		}

		graph.AddNode(node)
		if modulePath != "" {
			modulePathToName[modulePath] = wsName
		}
	}

	// Second pass: build edges based on dependencies
	for _, ws := range b.workspaces {
		wsName := filepath.Base(ws)
		node, exists := graph.GetNode(wsName)
		if !exists {
			continue
		}

		// Find and load grove config (supports .yml, .yaml, .toml)
		configPath, err := config.FindConfigFile(ws)
		if err != nil {
			continue
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			continue
		}

		projectType := b.getProjectType(cfg)
		handler, err := b.projectRegistry.Get(projectType)
		if err != nil {
			continue
		}

		// Parse dependencies
		deps, err := handler.ParseDependencies(ws)
		if err != nil {
			b.logger.WithError(err).Warnf("Failed to parse dependencies for %s", wsName)
			continue
		}

		// Add edges for workspace dependencies
		for _, dep := range deps {
			if !dep.Workspace {
				continue
			}

			// For Go projects, use module path mapping
			if projectType == project.TypeGo {
				if depName, ok := modulePathToName[dep.Name]; ok {
					node.Deps = append(node.Deps, dep.Name)
					graph.AddEdge(wsName, depName)
				}
			} else {
				// For other project types, use name directly
				// This assumes the dependency name matches the workspace name
				if _, exists := graph.GetNode(dep.Name); exists {
					node.Deps = append(node.Deps, dep.Name)
					graph.AddEdge(wsName, dep.Name)
				}
			}
		}
	}

	return graph, nil
}

// getProjectType extracts the project type from config
func (b *Builder) getProjectType(cfg *config.Config) project.Type {
	// Check if type is specified in extensions
	var typeStr string
	if err := cfg.UnmarshalExtension("type", &typeStr); err == nil && typeStr != "" {
		return project.Type(typeStr)
	}

	// Default to Go for backward compatibility
	return project.TypeGo
}

// getGoModulePath is a helper to extract the module path from go.mod
// This is needed for Go projects to maintain compatibility
func (b *Builder) getGoModulePath(workspacePath string) string {
	// Extract from go.mod file directly
	// This is a temporary solution - ideally the handler would expose this
	goModPath := filepath.Join(workspacePath, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}

	return ""
}
