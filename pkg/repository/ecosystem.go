package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
)

// Ecosystem handles ecosystem-level operations
type Ecosystem struct {
	logger *logrus.Logger
}

// removeFromGoWork removes a repository from the go.work file
func (e *Ecosystem) removeFromGoWork(repoName string) error {
	goWorkPath := "go.work"
	
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

// updateRootMakefile updates the root Makefile to include the new package and binary
func updateRootMakefile(repoName, binaryAlias string) error {
	makefilePath := "Makefile"
	
	// Read the current Makefile
	content, err := os.ReadFile(makefilePath)
	if err != nil {
		return fmt.Errorf("failed to read Makefile: %w\nEnsure you're in the grove-ecosystem root directory", err)
	}

	lines := strings.Split(string(content), "\n")
	
	// Hook markers
	packagesHook := "# GROVE-META:ADD-REPO:PACKAGES"
	binariesHook := "# GROVE-META:ADD-REPO:BINARIES"
	
	packagesUpdated := false
	binariesUpdated := false
	
	for i, line := range lines {
		// Find PACKAGES hook and update the line before it
		if strings.Contains(line, packagesHook) && i > 0 {
			// Parse the PACKAGES line (previous line)
			packages := extractMakefileList(lines, i-1)
			
			// Check if package already exists
			alreadyExists := false
			for _, pkg := range packages {
				if pkg == repoName {
					alreadyExists = true
					break
				}
			}
			
			if !alreadyExists {
				packages = append(packages, repoName)
				sort.Strings(packages)
				lines[i-1] = fmt.Sprintf("PACKAGES = %s", strings.Join(packages, " "))
			}
			packagesUpdated = true
		}
		
		// Find BINARIES hook and update the line before it
		if strings.Contains(line, binariesHook) && i > 0 {
			// Parse the BINARIES line (previous line)
			binaries := extractMakefileList(lines, i-1)
			
			// Check if binary already exists
			alreadyExists := false
			for _, bin := range binaries {
				if bin == binaryAlias {
					alreadyExists = true
					break
				}
			}
			
			if !alreadyExists {
				binaries = append(binaries, binaryAlias)
				sort.Strings(binaries)
				lines[i-1] = fmt.Sprintf("BINARIES = %s", strings.Join(binaries, " "))
			}
			binariesUpdated = true
		}
	}
	
	if !packagesUpdated {
		return fmt.Errorf("could not find PACKAGES hook (%s) in Makefile\nEnsure the Makefile has the required hook comment", packagesHook)
	}
	
	if !binariesUpdated {
		return fmt.Errorf("could not find BINARIES hook (%s) in Makefile\nEnsure the Makefile has the required hook comment", binariesHook)
	}
	
	// Write the updated Makefile
	updatedContent := strings.Join(lines, "\n")
	if err := os.WriteFile(makefilePath, []byte(updatedContent), 0644); err != nil {
		return fmt.Errorf("failed to write Makefile: %w\nThe file may be read-only or you may lack permissions", err)
	}
	
	return nil
}

// extractMakefileList extracts a space-separated list from a Makefile variable
// that might span multiple lines with backslash continuations
func extractMakefileList(lines []string, startIdx int) []string {
	var items []string
	
	// Extract the first line
	line := lines[startIdx]
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return items
	}
	
	value := strings.TrimSpace(parts[1])
	
	// Check for continuation
	for strings.HasSuffix(value, "\\") && startIdx+1 < len(lines) {
		value = strings.TrimSuffix(value, "\\")
		items = append(items, strings.Fields(value)...)
		startIdx++
		value = strings.TrimSpace(lines[startIdx])
	}
	
	// Add the final line's items
	items = append(items, strings.Fields(value)...)
	
	// Remove duplicates
	seen := make(map[string]bool)
	var unique []string
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			unique = append(unique, item)
		}
	}
	
	return unique
}

// updateGoWork updates the go.work file to include the new module
func updateGoWork(repoName string) error {
	workPath := "go.work"
	
	// Read the current go.work file
	content, err := os.ReadFile(workPath)
	if err != nil {
		return fmt.Errorf("failed to read go.work: %w\nEnsure you're in the grove-ecosystem root directory", err)
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
	if _, err := os.Stat("grove.yml"); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("grove.yml not found - must be run from grove-ecosystem root")
		}
		return fmt.Errorf("error accessing grove.yml: %w", err)
	}
	return nil
}

// isValidRepoName checks if the repository name follows Grove conventions
func isValidRepoName(name string) bool {
	// Must start with "grove-"
	if !strings.HasPrefix(name, "grove-") {
		return false
	}
	
	// Must only contain lowercase letters, numbers, and hyphens
	validName := regexp.MustCompile(`^grove-[a-z0-9-]+$`)
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
	// Read Makefile to get current binaries
	content, err := os.ReadFile("Makefile")
	if err != nil {
		return fmt.Errorf("failed to read Makefile: %w", err)
	}
	
	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "BINARIES =") || strings.HasPrefix(line, "BINARIES=") {
			binaries := extractMakefileList(lines, i)
			for _, binary := range binaries {
				if binary == alias {
					return fmt.Errorf("binary alias '%s' is already in use", alias)
				}
			}
			break
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