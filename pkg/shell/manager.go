// Package shell provides utilities for detecting and configuring user shells,
// particularly for managing PATH modifications during Grove onboarding.
package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ShellType represents a supported shell type
type ShellType string

const (
	ShellBash ShellType = "bash"
	ShellZsh  ShellType = "zsh"
	ShellFish ShellType = "fish"
)

// Manager handles shell configuration interactions
type Manager struct {
	homeDir string
}

// NewManager creates a new shell manager
func NewManager() (*Manager, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	return &Manager{homeDir: homeDir}, nil
}

// Detect returns the user's current shell type based on the SHELL environment variable
func (m *Manager) Detect() (ShellType, error) {
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		return "", fmt.Errorf("SHELL environment variable not set")
	}

	shell := filepath.Base(shellPath)
	switch shell {
	case "bash":
		return ShellBash, nil
	case "zsh":
		return ShellZsh, nil
	case "fish":
		return ShellFish, nil
	default:
		// Try to handle common variations
		if strings.Contains(shell, "bash") {
			return ShellBash, nil
		}
		if strings.Contains(shell, "zsh") {
			return ShellZsh, nil
		}
		if strings.Contains(shell, "fish") {
			return ShellFish, nil
		}
		return "", fmt.Errorf("unsupported shell: %s", shell)
	}
}

// GetRcFile returns the appropriate RC file path for the given shell type
func (m *Manager) GetRcFile(shell ShellType) (string, error) {
	switch shell {
	case ShellBash:
		// Prefer .bashrc, fall back to .bash_profile
		bashrc := filepath.Join(m.homeDir, ".bashrc")
		if _, err := os.Stat(bashrc); err == nil {
			return bashrc, nil
		}
		return filepath.Join(m.homeDir, ".bash_profile"), nil
	case ShellZsh:
		return filepath.Join(m.homeDir, ".zshrc"), nil
	case ShellFish:
		return filepath.Join(m.homeDir, ".config", "fish", "config.fish"), nil
	default:
		return "", fmt.Errorf("unsupported shell type: %s", shell)
	}
}

// PathIncludes checks if the given directory is already in the user's PATH
func (m *Manager) PathIncludes(dir string) bool {
	pathEnv := os.Getenv("PATH")
	paths := filepath.SplitList(pathEnv)
	for _, p := range paths {
		if p == dir {
			return true
		}
		// Also check with trailing slash removed
		if strings.TrimSuffix(p, "/") == strings.TrimSuffix(dir, "/") {
			return true
		}
	}
	return false
}

// GetPathExportLine returns the shell-specific line to add a directory to PATH
func (m *Manager) GetPathExportLine(dir string, shell ShellType) string {
	switch shell {
	case ShellFish:
		return fmt.Sprintf("fish_add_path %s", dir)
	default:
		// Bash and Zsh use the same syntax
		return fmt.Sprintf("export PATH=\"%s:$PATH\"", dir)
	}
}

// AddToPath adds the given directory to the user's shell PATH configuration.
// It detects the shell type, finds the appropriate RC file, and appends the
// necessary export line if the directory isn't already configured.
func (m *Manager) AddToPath(dir string) error {
	shell, err := m.Detect()
	if err != nil {
		return err
	}

	rcFile, err := m.GetRcFile(shell)
	if err != nil {
		return err
	}

	// Check if the rc file already contains the path
	content, err := os.ReadFile(rcFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read %s: %w", rcFile, err)
	}

	// Check if the path is already configured in the rc file
	exportLine := m.GetPathExportLine(dir, shell)
	if strings.Contains(string(content), dir) {
		// Already configured
		return nil
	}

	// Ensure parent directory exists (for fish config)
	parentDir := filepath.Dir(rcFile)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", parentDir, err)
	}

	// Append to the rc file
	f, err := os.OpenFile(rcFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", rcFile, err)
	}
	defer f.Close()

	// Add a newline before the export line if the file doesn't end with one
	prefix := "\n"
	if len(content) == 0 || content[len(content)-1] == '\n' {
		prefix = ""
	}

	comment := "# Grove tools"
	_, err = f.WriteString(fmt.Sprintf("%s%s\n%s\n", prefix, comment, exportLine))
	if err != nil {
		return fmt.Errorf("failed to write to %s: %w", rcFile, err)
	}

	return nil
}

// GetShellName returns a human-readable name for the shell type
func (m *Manager) GetShellName(shell ShellType) string {
	switch shell {
	case ShellBash:
		return "Bash"
	case ShellZsh:
		return "Zsh"
	case ShellFish:
		return "Fish"
	default:
		return string(shell)
	}
}

// GetRcFileName returns the file name (not full path) of the RC file
func (m *Manager) GetRcFileName(shell ShellType) string {
	switch shell {
	case ShellBash:
		bashrc := filepath.Join(m.homeDir, ".bashrc")
		if _, err := os.Stat(bashrc); err == nil {
			return ".bashrc"
		}
		return ".bash_profile"
	case ShellZsh:
		return ".zshrc"
	case ShellFish:
		return "config.fish"
	default:
		return "shell config"
	}
}
