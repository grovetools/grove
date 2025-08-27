package project

type Type string

const (
	TypeGo       Type = "go"
	TypeMaturin  Type = "maturin"
	TypeNode     Type = "node"
	TypeTemplate Type = "template"
)

// ProjectHandler defines the interface for language-specific operations
type ProjectHandler interface {
	// Dependency management
	ParseDependencies(workspacePath string) ([]Dependency, error)
	UpdateDependency(workspacePath string, dep Dependency) error

	// Version management
	GetVersion(workspacePath string) (string, error)
	SetVersion(workspacePath string, version string) error

	// Build commands (leverage Makefile contract)
	GetBuildCommand() string
	GetTestCommand() string
	GetVerifyCommand() string

	// Project detection
	HasProjectFile(workspacePath string) bool
}

type Dependency struct {
	Name      string
	Version   string
	Type      DependencyType
	Workspace bool // true if dependency is another grove workspace
}

type DependencyType string

const (
	DependencyTypeLibrary DependencyType = "library"
	DependencyTypeBinary  DependencyType = "binary"
)
