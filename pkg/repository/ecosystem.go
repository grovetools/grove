package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mattsolo1/grove-core/config"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/sirupsen/logrus"
)

// Ecosystem handles ecosystem-level operations
type Ecosystem struct {
	logger *logrus.Logger
}

// removeFromGoWork removes a repository from the go.work file
func (e *Ecosystem) removeFromGoWork(repoName string) error {
	// Find the grove root
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find grove root: %w", err)
	}
	
	goWorkPath := filepath.Join(rootDir, "go.work")
	
	// Check if go.work exists
	if _, err := os.Stat(goWorkPath); os.IsNotExist(err) {
		// No go.work file, nothing to remove
		return nil
	}
	
	// Read the current go.work file
	content, err := os.ReadFile(goWorkPath)
	if err != nil {
		return fmt.Errorf("failed to read go.work: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	var newLines []string
	inUseBlock := false
	removed := false
	pathToRemove := "./" + repoName
	
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		// Track when we're in the use block
		if trimmed == "use (" {
			inUseBlock = true
			newLines = append(newLines, line)
			continue
		}
		if trimmed == ")" && inUseBlock {
			inUseBlock = false
			newLines = append(newLines, line)
			continue
		}
		
		// Skip the line if it contains the repo we're removing
		if inUseBlock && strings.Contains(trimmed, pathToRemove) {
			removed = true
			continue
		}
		
		// Handle single use directive
		if strings.HasPrefix(trimmed, "use ") && strings.Contains(trimmed, pathToRemove) {
			removed = true
			continue
		}
		
		newLines = append(newLines, line)
	}
	
	if !removed {
		// Module wasn't in the file, nothing to do
		return nil
	}
	
	// Write the updated content back
	if err := os.WriteFile(goWorkPath, []byte(strings.Join(newLines, "\n")), 0644); err != nil {
		return fmt.Errorf("failed to write go.work: %w", err)
	}
	
	return nil
}



// updateGoWork updates the go.work file to include the new module
func updateGoWork(repoName string) error {
	// Find the grove root
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find grove root: %w", err)
	}
	
	workPath := filepath.Join(rootDir, "go.work")
	
	// Check if go.work exists, create it if not
	content, err := os.ReadFile(workPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create a new go.work file with the current Go version
			goVersion := "1.24.4" // Default Go version
			newContent := fmt.Sprintf("go %s\n\nuse (\n\t./%s\n)\n", goVersion, repoName)
			if err := os.WriteFile(workPath, []byte(newContent), 0644); err != nil {
				return fmt.Errorf("failed to create go.work: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to read go.work: %w", err)
	}
	
	// Check if module already exists
	newModulePath := "./" + repoName
	if strings.Contains(string(content), newModulePath) {
		// Module already exists, nothing to do
		return nil
	}
	
	// Parse go.work to find the use directives
	lines := strings.Split(string(content), "\n")
	var newLines []string
	inUseBlock := false
	useDirectives := []string{}
	var beforeUse, afterUse []string
	useBlockStart := -1
	
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		if trimmed == "use (" {
			inUseBlock = true
			useBlockStart = i
			beforeUse = lines[:i+1]
			continue
		}
		
		if inUseBlock {
			if trimmed == ")" {
				inUseBlock = false
				afterUse = lines[i:]
				break
			}
			if trimmed != "" {
				useDirectives = append(useDirectives, trimmed)
			}
		}
	}
	
	// If we didn't find a use block, look for single use directives
	if useBlockStart == -1 {
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "use ") {
				// Convert to block format
				beforeUse = lines[:i]
				afterUse = lines[i+1:]
				useDirectives = []string{strings.TrimPrefix(strings.TrimSpace(line), "use ")}
				break
			}
		}
		
		// If still no use directives found, create a new block after "go" directive
		if len(useDirectives) == 0 {
			for i, line := range lines {
				if strings.HasPrefix(strings.TrimSpace(line), "go ") {
					beforeUse = lines[:i+1]
					beforeUse = append(beforeUse, "")
					afterUse = lines[i+1:]
					break
				}
			}
		}
	}
	
	// Add the new module
	useDirectives = append(useDirectives, newModulePath)
	
	// Sort the directives for consistency
	sort.Strings(useDirectives)
	
	// Rebuild the file
	newLines = append(newLines, beforeUse...)
	if len(beforeUse) > 0 && !strings.HasSuffix(beforeUse[len(beforeUse)-1], "use (") {
		newLines = append(newLines, "use (")
	}
	
	for _, directive := range useDirectives {
		newLines = append(newLines, fmt.Sprintf("\t%s", directive))
	}
	
	if len(afterUse) > 0 && !strings.HasPrefix(afterUse[0], ")") {
		newLines = append(newLines, ")")
	}
	newLines = append(newLines, afterUse...)
	
	// Write the updated go.work file
	updatedContent := strings.Join(newLines, "\n")
	if err := os.WriteFile(workPath, []byte(updatedContent), 0644); err != nil {
		return fmt.Errorf("failed to write go.work: %w\nThe file may be read-only or you may lack permissions", err)
	}
	
	return nil
}

// validateGroveYML checks if grove.yml exists and has the expected structure
func validateGroveYML() error {
	// Try to find the grove root
	_, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("not in a grove workspace: %w", err)
	}
	return nil
}

// isValidRepoName checks if the repository name follows Grove conventions
func isValidRepoName(name string) bool {
	// Must only contain lowercase letters, numbers, and hyphens
	validName := regexp.MustCompile(`^[a-z0-9-]+$`)
	return validName.MatchString(name)
}

// deriveAliasFromRepoName generates a binary alias from the repository name
// e.g., grove-context -> ct, grove-tend -> td
func deriveAliasFromRepoName(repoName string) string {
	parts := strings.Split(repoName, "-")
	if len(parts) < 2 {
		return ""
	}
	
	var alias strings.Builder
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 {
			alias.WriteByte(parts[i][0])
		}
	}
	
	return alias.String()
}

// checkBinaryAliasConflict checks if the binary alias is already in use
func checkBinaryAliasConflict(alias string) error {
	// Find grove.yml root
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find grove root: %w", err)
	}
	
	// Discover all workspaces
	workspaces, err := workspace.Discover(rootDir)
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}
	
	// Check each workspace's grove.yml for binary aliases
	for _, ws := range workspaces {
		configPath := filepath.Join(ws, "grove.yml")
		cfg, err := config.Load(configPath)
		if err != nil {
			continue // Skip workspaces with invalid configs
		}
		
		// Check if this workspace defines a binary with the same alias
		// Binary info is stored in Extensions as grove.yml allows custom fields
		if binaryRaw, ok := cfg.Extensions["binary"]; ok {
			if binaryMap, ok := binaryRaw.(map[string]interface{}); ok {
				if aliasVal, ok := binaryMap["alias"].(string); ok && aliasVal == alias {
					return fmt.Errorf("binary alias '%s' is already in use by %s", alias, filepath.Base(ws))
				}
			}
		}
	}
	
	return nil
}

// getEcosystemRoot finds the grove-ecosystem root directory
func getEcosystemRoot() (string, error) {
	// Start from current directory and walk up
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	
	for {
		// Check if grove.yml exists in this directory
		if _, err := os.Stat(filepath.Join(dir, "grove.yml")); err == nil {
			// Also check if it's the ecosystem root by looking for go.work
			if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
				return dir, nil
			}
		}
		
		// Move up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the root
			break
		}
		dir = parent
	}
	
	return "", fmt.Errorf("grove-ecosystem root not found")
}

// extractMakefileList extracts a space-separated list of items from Makefile lines
// starting from the given index. It handles multi-line lists with backslashes.
func extractMakefileList(lines []string, startIdx int) []string {
	if startIdx >= len(lines) {
		return []string{}
	}
	
	// Extract the value after the equals sign
	firstLine := lines[startIdx]
	parts := strings.SplitN(firstLine, "=", 2)
	if len(parts) < 2 {
		return []string{}
	}
	
	var items []string
	currentLine := strings.TrimSpace(parts[1])
	
	// Check if line ends with backslash (continues on next line)
	for i := startIdx; i < len(lines); i++ {
		if i == startIdx {
			// First line already processed
			if strings.HasSuffix(currentLine, "\\") {
				currentLine = strings.TrimSuffix(currentLine, "\\")
			}
		} else {
			// Subsequent lines
			currentLine = strings.TrimSpace(lines[i])
			if strings.HasSuffix(currentLine, "\\") {
				currentLine = strings.TrimSuffix(currentLine, "\\")
			}
		}
		
		// Split by whitespace and add non-empty items
		for _, item := range strings.Fields(currentLine) {
			item = strings.TrimSpace(item)
			if item != "" && item != "\\" {
				items = append(items, item)
			}
		}
		
		// Check if this line continues to the next
		if i < len(lines)-1 && strings.HasSuffix(strings.TrimSpace(lines[i]), "\\") {
			continue
		} else {
			break
		}
	}
	
	// Remove duplicates while preserving order
	seen := make(map[string]bool)
	var unique []string
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			unique = append(unique, item)
		}
	}
	
	// Sort for consistent output
	sort.Strings(unique)
	
	return unique
}

// updateRootMakefile updates the root Makefile to include a new repository
func updateRootMakefile(repoName, binaryAlias string) error {
	// Find the grove root
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find grove root: %w", err)
	}
	
	makefilePath := filepath.Join(rootDir, "Makefile")
	
	// Read the current Makefile
	content, err := os.ReadFile(makefilePath)
	if err != nil {
		return fmt.Errorf("failed to read Makefile: %w", err)
	}
	
	lines := strings.Split(string(content), "\n")
	
	// Look for the package and binary hooks
	packageHookFound := false
	binaryHookFound := false
	packageHookIdx := -1
	binaryHookIdx := -1
	
	for i, line := range lines {
		if strings.Contains(line, "# GROVE-META:ADD-REPO:PACKAGES") {
			packageHookFound = true
			packageHookIdx = i
		}
		if strings.Contains(line, "# GROVE-META:ADD-REPO:BINARIES") {
			binaryHookFound = true
			binaryHookIdx = i
		}
	}
	
	if !packageHookFound {
		return fmt.Errorf("PACKAGES hook not found in Makefile")
	}
	if !binaryHookFound {
		return fmt.Errorf("BINARIES hook not found in Makefile")
	}
	
	// Find the PACKAGES line before the hook
	packagesIdx := -1
	for i := packageHookIdx - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "PACKAGES") {
			packagesIdx = i
			break
		}
	}
	
	if packagesIdx == -1 {
		return fmt.Errorf("PACKAGES variable not found before hook")
	}
	
	// Extract current packages
	packages := extractMakefileList(lines, packagesIdx)
	
	// Check if package already exists
	alreadyExists := false
	for _, pkg := range packages {
		if pkg == repoName {
			alreadyExists = true
			break
		}
	}
	
	if !alreadyExists {
		// Add the new package
		packages = append(packages, repoName)
		sort.Strings(packages)
		
		// Update the PACKAGES line
		lines[packagesIdx] = fmt.Sprintf("PACKAGES = %s", strings.Join(packages, " "))
	}
	
	// Find the BINARIES line before the hook
	binariesIdx := -1
	for i := binaryHookIdx - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "BINARIES") {
			binariesIdx = i
			break
		}
	}
	
	if binariesIdx == -1 {
		return fmt.Errorf("BINARIES variable not found before hook")
	}
	
	// Extract current binaries
	binaries := extractMakefileList(lines, binariesIdx)
	
	// Check if binary already exists
	binaryExists := false
	for _, bin := range binaries {
		if bin == binaryAlias {
			binaryExists = true
			break
		}
	}
	
	if !binaryExists && binaryAlias != "" {
		// Add the new binary
		binaries = append(binaries, binaryAlias)
		sort.Strings(binaries)
		
		// Update the BINARIES line
		lines[binariesIdx] = fmt.Sprintf("BINARIES = %s", strings.Join(binaries, " "))
	}
	
	// Write the updated Makefile
	updatedContent := strings.Join(lines, "\n")
	if err := os.WriteFile(makefilePath, []byte(updatedContent), 0644); err != nil {
		return fmt.Errorf("failed to write Makefile: %w", err)
	}
	
	return nil
}