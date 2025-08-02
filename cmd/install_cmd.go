package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	
	"github.com/mattsolo1/grove-core/cli"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newInstallCmd())
}

func newInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install [tools...]",
		Short: "Install Grove tools",
		Long:  "Install one or more Grove tools from the registry",
		Example: `  grove install context version
  grove install cx nb`,
		Args: cobra.MinimumNArgs(1),
		RunE: runInstall,
	}
	
	return cmd
}

func runInstall(cmd *cobra.Command, args []string) error {
	logger := cli.GetLogger(cmd)
	
	registry, err := LoadRegistry()
	if err != nil {
		return fmt.Errorf("failed to load registry: %w", err)
	}
	
	for _, toolName := range args {
		if err := installTool(registry, toolName, logger); err != nil {
			logger.WithError(err).Errorf("Failed to install %s", toolName)
			// Continue with other tools
		}
	}
	
	return nil
}

func installTool(registry *Registry, toolName string, logger *logrus.Logger) error {
	// Find the tool by name or alias
	var tool *Tool
	for _, t := range registry.Tools {
		if t.Name == toolName || t.Alias == toolName {
			tool = &t
			break
		}
	}
	
	if tool == nil {
		return fmt.Errorf("unknown tool: %s", toolName)
	}
	
	// Check if this is a local repository
	if IsLocalRepository(tool.Repository) {
		return installLocalTool(tool, logger)
	}
	
	// Remote repository installation
	installPath := tool.Repository + "@" + tool.Version
	logger.Infof("Installing %s from %s...", tool.Name, installPath)
	
	cmd := exec.Command("go", "install", installPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go install failed: %w", err)
	}
	
	logger.Infof("✓ Successfully installed %s (alias: %s, binary: %s)", tool.Name, tool.Alias, tool.Binary)
	return nil
}

func installLocalTool(tool *Tool, logger *logrus.Logger) error {
	// Expand the repository path
	repoPath := tool.Repository
	if strings.HasPrefix(repoPath, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		repoPath = filepath.Join(homeDir, repoPath[2:])
	}
	
	// Make it absolute
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}
	
	logger.Infof("Installing %s from local repository: %s", tool.Name, absPath)
	
	// Check if the directory exists
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return fmt.Errorf("local repository not found: %s", absPath)
	}
	
	// Build in the local directory
	cmd := exec.Command("go", "build", "-o", getBinaryPath(tool))
	cmd.Dir = absPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}
	
	logger.Infof("✓ Successfully installed %s from local repository", tool.Name)
	return nil
}

func getBinaryPath(tool *Tool) string {
	groveDir := os.Getenv("GROVE_DIR")
	if groveDir == "" {
		homeDir, _ := os.UserHomeDir()
		groveDir = filepath.Join(homeDir, ".grove")
	}
	
	binDir := filepath.Join(groveDir, "bin")
	os.MkdirAll(binDir, 0755)
	
	binaryName := tool.Binary
	if binaryName == "" {
		binaryName = fmt.Sprintf("grove-%s", tool.Name)
	}
	
	return filepath.Join(binDir, binaryName)
}