package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/discovery"
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
	projects, err := discovery.DiscoverProjects()
	if err != nil {
		return fmt.Errorf("failed to discover projects: %w", err)
	}

	foundCount := 0
	for _, proj := range projects {
		// Convention: look for <repo-name>.schema.json in the project root
		schemaFileName := fmt.Sprintf("%s.schema.json", proj.Name)
		localSchemaPath := filepath.Join(proj.Path, schemaFileName)

		if _, err := os.Stat(localSchemaPath); err == nil {
			relPath, err := filepath.Rel(ecosystemRoot, localSchemaPath)
			if err != nil {
				return err
			}

			// The key for the extension is the repo name without 'grove-' prefix, if it exists
			extKey := strings.TrimPrefix(proj.Name, "grove-")

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
	schema["additionalProperties"] = false
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

	fmt.Printf("\n✅ Unified local schema generated at: %s\n", outputPath)
	fmt.Println("\nTo enable IDE support, add this line to your root grove.yml:")
	relSchema, _ := filepath.Rel(ecosystemRoot, outputPath)
	fmt.Printf("# yaml-language-server: $schema=%s\n", relSchema)

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
