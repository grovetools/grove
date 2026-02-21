package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/paths"
	"github.com/sirupsen/logrus"
)

// Action represents a single action performed or simulated by the setup service
type Action struct {
	Type        ActionType
	Description string
	Path        string
	Success     bool
	Error       error
}

// ActionType represents the type of action being performed
type ActionType string

const (
	ActionWriteFile      ActionType = "write_file"
	ActionAppendFile     ActionType = "append_file"
	ActionCreateDir      ActionType = "create_dir"
	ActionUpdateYAML     ActionType = "update_yaml"
	ActionCreateEcosystem ActionType = "create_ecosystem"
)

// Service encapsulates all file I/O and command execution logic for the setup wizard.
// It respects dry-run mode and maintains a log of actions for the summary screen.
type Service struct {
	dryRun  bool
	actions []Action
	logger  *logrus.Entry
}

// NewService creates a new setup service
func NewService(dryRun bool) *Service {
	return &Service{
		dryRun:  dryRun,
		actions: []Action{},
		logger:  logging.NewLogger("setup"),
	}
}

// IsDryRun returns whether the service is in dry-run mode
func (s *Service) IsDryRun() bool {
	return s.dryRun
}

// Actions returns all actions performed or simulated
func (s *Service) Actions() []Action {
	return s.actions
}

// logAction adds an action to the action log
func (s *Service) logAction(actionType ActionType, description string, path string, success bool, err error) {
	s.actions = append(s.actions, Action{
		Type:        actionType,
		Description: description,
		Path:        path,
		Success:     success,
		Error:       err,
	})
}

// WriteFile writes content to a file, respecting dry-run mode
func (s *Service) WriteFile(path string, content []byte, perm os.FileMode) error {
	// Expand home directory if needed
	expandedPath := expandPath(path)

	description := fmt.Sprintf("Write %s", AbbreviatePath(expandedPath))
	if s.dryRun {
		s.logger.Infof("[dry-run] Would write to %s", path)
		s.logAction(ActionWriteFile, description, expandedPath, true, nil)
		return nil
	}

	// Ensure parent directory exists
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		s.logAction(ActionWriteFile, description, expandedPath, false, err)
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	if err := os.WriteFile(expandedPath, content, perm); err != nil {
		s.logAction(ActionWriteFile, description, expandedPath, false, err)
		return fmt.Errorf("failed to write file %s: %w", path, err)
	}

	s.logger.Infof("Wrote %s", path)
	s.logAction(ActionWriteFile, description, expandedPath, true, nil)
	return nil
}

// AppendToFile appends content to a file, respecting dry-run mode.
// If the file doesn't exist, it creates it.
func (s *Service) AppendToFile(path string, content string) error {
	expandedPath := expandPath(path)

	description := fmt.Sprintf("Append to %s", AbbreviatePath(expandedPath))
	if s.dryRun {
		s.logger.Infof("[dry-run] Would append to %s", path)
		s.logAction(ActionAppendFile, description, expandedPath, true, nil)
		return nil
	}

	// Ensure parent directory exists
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		s.logAction(ActionAppendFile, description, expandedPath, false, err)
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Open file for appending, create if not exists
	f, err := os.OpenFile(expandedPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		s.logAction(ActionAppendFile, description, expandedPath, false, err)
		return fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		s.logAction(ActionAppendFile, description, expandedPath, false, err)
		return fmt.Errorf("failed to append to file %s: %w", path, err)
	}

	s.logger.Infof("Appended to %s", path)
	s.logAction(ActionAppendFile, description, expandedPath, true, nil)
	return nil
}

// MkdirAll creates a directory and all parent directories, respecting dry-run mode
func (s *Service) MkdirAll(path string, perm os.FileMode) error {
	expandedPath := expandPath(path)

	description := fmt.Sprintf("Create directory %s", AbbreviatePath(expandedPath))
	if s.dryRun {
		s.logger.Infof("[dry-run] Would create directory %s", path)
		s.logAction(ActionCreateDir, description, expandedPath, true, nil)
		return nil
	}

	if err := os.MkdirAll(expandedPath, perm); err != nil {
		s.logAction(ActionCreateDir, description, expandedPath, false, err)
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}

	s.logger.Infof("Created directory %s", path)
	s.logAction(ActionCreateDir, description, expandedPath, true, nil)
	return nil
}

// RunGitInit initializes a git repository in the given directory
func (s *Service) RunGitInit(path string) error {
	expandedPath := expandPath(path)
	description := fmt.Sprintf("Initialize git in %s", AbbreviatePath(expandedPath))

	if s.dryRun {
		s.logger.Infof("[dry-run] Would run git init in %s", path)
		s.logAction(ActionCreateDir, description, expandedPath, true, nil)
		return nil
	}

	// Check if .git already exists
	gitDir := filepath.Join(expandedPath, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		s.logger.Infof("Git already initialized in %s", path)
		return nil
	}

	// Run git init
	cmd := exec.Command("git", "init")
	cmd.Dir = expandedPath
	if err := cmd.Run(); err != nil {
		s.logAction(ActionCreateDir, description, expandedPath, false, err)
		return fmt.Errorf("failed to initialize git in %s: %w", path, err)
	}

	s.logger.Infof("Initialized git in %s", path)
	s.logAction(ActionCreateDir, description, expandedPath, true, nil)
	return nil
}

// FileExists checks if a file exists at the given path
func (s *Service) FileExists(path string) bool {
	expandedPath := expandPath(path)
	_, err := os.Stat(expandedPath)
	return err == nil
}

// ReadFile reads a file and returns its content
func (s *Service) ReadFile(path string) ([]byte, error) {
	expandedPath := expandPath(path)
	return os.ReadFile(expandedPath)
}

// FileContains checks if a file contains the given substring
func (s *Service) FileContains(path string, substr string) (bool, error) {
	content, err := s.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return contains(string(content), substr), nil
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// expandPath expands ~ to the user's home directory
func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(homeDir, path[1:])
	}
	return path
}

// AbbreviatePath replaces the home directory with ~ for display
func AbbreviatePath(path string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, homeDir) {
		return "~" + path[len(homeDir):]
	}
	return path
}

// RunCommand executes a command, respecting dry-run mode
func (s *Service) RunCommand(name string, args ...string) error {
	description := fmt.Sprintf("Run %s %s", name, strings.Join(args, " "))

	if s.dryRun {
		s.logger.Infof("[dry-run] Would run: %s %s", name, strings.Join(args, " "))
		s.logAction(ActionCreateDir, description, "", true, nil)
		return nil
	}

	cmd := exec.Command(name, args...)
	if err := cmd.Run(); err != nil {
		s.logAction(ActionCreateDir, description, "", false, err)
		return fmt.Errorf("failed to run %s: %w", name, err)
	}

	s.logger.Infof("Ran: %s %s", name, strings.Join(args, " "))
	s.logAction(ActionCreateDir, description, "", true, nil)
	return nil
}

// ReplaceInFile replaces all occurrences of old with new in a file
func (s *Service) ReplaceInFile(path, old, new string) error {
	expandedPath := expandPath(path)
	description := fmt.Sprintf("Update %s", AbbreviatePath(expandedPath))

	if s.dryRun {
		s.logger.Infof("[dry-run] Would replace in %s: %q -> %q", path, old, new)
		s.logAction(ActionWriteFile, description, expandedPath, true, nil)
		return nil
	}

	content, err := os.ReadFile(expandedPath)
	if err != nil {
		s.logAction(ActionWriteFile, description, expandedPath, false, err)
		return fmt.Errorf("failed to read %s: %w", path, err)
	}

	newContent := strings.ReplaceAll(string(content), old, new)
	if err := os.WriteFile(expandedPath, []byte(newContent), 0644); err != nil {
		s.logAction(ActionWriteFile, description, expandedPath, false, err)
		return fmt.Errorf("failed to write %s: %w", path, err)
	}

	s.logger.Infof("Updated %s", path)
	s.logAction(ActionWriteFile, description, expandedPath, true, nil)
	return nil
}

// GlobalConfigPath returns the path to the global grove configuration file.
func GlobalConfigPath() string {
	configDir := paths.ConfigDir()
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "grove.yml")
}
