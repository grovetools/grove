package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/grove/pkg/discovery"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// newSchemaCmd creates the `schema` command and its subcommands.
func newSchemaCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("schema", "Manage and compose local JSON schemas")
	cmd.Long = `Tools for working with Grove JSON schemas.

The schema command provides utilities for generating unified schemas
from ecosystem workspaces for local development.`

	// Add subcommands
	cmd.AddCommand(newSchemaGenerateCmd())

	return cmd
}

// newSchemaGenerateCmd creates the `schema generate` subcommand.
func newSchemaGenerateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a unified local schema for the current ecosystem",
		Long: `Scans all workspaces in the current ecosystem for locally generated schema files
and composes them into a single 'grove.schema.json' at the ecosystem root.

This enables IDE autocompletion and validation during local development.

The command will:
1. Find the ecosystem root (directory containing workspaces in grove.yml)
2. Locate grove-core's base schema
3. Discover all workspace projects and look for their schema files
4. Compose them into a unified schema at .grove/grove.schema.json

Example usage:
  grove schema generate`,
		RunE: runSchemaGenerate,
	}

	return cmd
}

func runSchemaGenerate(cmd *cobra.Command, args []string) error {
	fmt.Println("Generating unified local schema...")

	// 1. Find ecosystem root by looking for a grove.yml with workspaces
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("could not get current directory: %w", err)
	}

	ecosystemRoot, err := findEcosystemRoot(cwd)
	if err != nil {
		return fmt.Errorf("could not find ecosystem root: %w", err)
	}

	fmt.Printf("Found ecosystem root: %s\n", ecosystemRoot)

	// 2. Find grove-core's base schema
	baseSchemaPath := filepath.Join(ecosystemRoot, "grove-core", "schema", "definitions", "base.schema.json")
	if _, err := os.Stat(baseSchemaPath); err != nil {
		return fmt.Errorf("base schema not found at %s. Run 'make schema' in grove-core first", baseSchemaPath)
	}

	baseBytes, err := os.ReadFile(baseSchemaPath)
	if err != nil {
		return fmt.Errorf("could not read base schema: %w", err)
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(baseBytes, &schema); err != nil {
		return fmt.Errorf("could not parse base schema: %w", err)
	}

	// Ensure properties map exists
	if _, ok := schema["properties"]; !ok {
		schema["properties"] = make(map[string]interface{})
	}
	properties := schema["properties"].(map[string]interface{})

	// 3. Discover all projects and look for their schemas
	// Use DiscoverAllProjects to include worktrees when developing in a worktree context
	projects, err := discovery.DiscoverAllProjects()
	if err != nil {
		return fmt.Errorf("failed to discover projects: %w", err)
	}

	fmt.Printf("Discovered %d projects\n", len(projects))

	foundCount := 0
	for _, proj := range projects {
		// Scan for any *.schema.json files in the project root
		files, err := filepath.Glob(filepath.Join(proj.Path, "*.schema.json"))
		if err != nil {
			continue
		}

		for _, localSchemaPath := range files {
			schemaFileName := filepath.Base(localSchemaPath)
			extKey := strings.TrimSuffix(schemaFileName, ".schema.json")

			relPath, err := filepath.Rel(ecosystemRoot, localSchemaPath)
			if err != nil {
				return err
			}

			fmt.Printf("  -> Found schema for '%s' at %s\n", extKey, relPath)
			properties[extKey] = map[string]interface{}{
				"$ref": "file://" + localSchemaPath,
			}
			foundCount++
		}
	}

	if foundCount == 0 {
		fmt.Println("  -> No extension schemas found in workspaces")
	}

	// 4. Finalize and write the composed schema
	schema["additionalProperties"] = true
	schema["title"] = "Grove Local Development Schema"
	schema["description"] = "Composed schema for the local ecosystem."

	outputDir := filepath.Join(ecosystemRoot, ".grove")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}
	outputPath := filepath.Join(outputDir, "grove.schema.json")

	outputBytes, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(outputPath, outputBytes, 0644); err != nil {
		return err
	}

	fmt.Printf("\n Unified local schema generated at: %s\n", outputPath)

	// Also write to global data dir for global config file support
	globalSchemaDir := filepath.Join(paths.DataDir(), "schemas")
	if err := os.MkdirAll(globalSchemaDir, 0755); err == nil {
		globalSchemaPath := filepath.Join(globalSchemaDir, "grove.schema.json")
		if err := os.WriteFile(globalSchemaPath, outputBytes, 0644); err == nil {
			fmt.Printf(" Global schema generated at: %s\n", globalSchemaPath)
		}
	}

	fmt.Println("\nThe grove-nvim plugin will automatically detect and use this schema.")
	fmt.Println("No manual configuration needed for grove.yml files!")

	return nil
}

// findEcosystemRoot walks up from the current directory looking for a grove.yml
// with a 'workspaces' field, indicating it's an ecosystem root.
func findEcosystemRoot(startDir string) (string, error) {
	dir := startDir

	for {
		groveYmlPath := filepath.Join(dir, "grove.yml")
		if _, err := os.Stat(groveYmlPath); err == nil {
			// Read and parse the YAML directly without validation
			// We only need to check for the presence of workspaces
			data, err := os.ReadFile(groveYmlPath)
			if err != nil {
				// If we can't read it, keep searching
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
				dir = parent
				continue
			}

			// Parse YAML to check for workspaces field
			var raw map[string]interface{}
			if err := yaml.Unmarshal(data, &raw); err != nil {
				// If we can't parse it, keep searching
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
				dir = parent
				continue
			}

			// Check if workspaces field exists and is non-empty
			if workspaces, ok := raw["workspaces"]; ok {
				if ws, ok := workspaces.([]interface{}); ok && len(ws) > 0 {
					return dir, nil
				}
			}
		}

		// Move up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// We've reached the root
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("no ecosystem root found (looking for grove.yml with workspaces)")
}
