package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mattsolo1/grove-core/git"
	"github.com/mattsolo1/grove-meta/pkg/depsgraph"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

func newDepsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deps",
		Short: "Manage dependencies across the Grove ecosystem",
		Long:  "The deps command provides tools for managing Go module dependencies across all Grove submodules.",
	}

	cmd.AddCommand(newDepsBumpCmd())
	cmd.AddCommand(newDepsSyncCmd())
	cmd.AddCommand(newDepsTreeCmd())
	return cmd
}

func newDepsBumpCmd() *cobra.Command {
	var commitFlag bool
	var pushFlag bool

	cmd := &cobra.Command{
		Use:   "bump <module_path>[@version]",
		Short: "Bump a dependency version across all submodules",
		Long: `Bump a Go module dependency across all Grove submodules.

This command finds all submodules that depend on the specified module and updates
them to the specified version. If no version is specified or @latest is used,
it will fetch the latest available version.

Examples:
  grove deps bump github.com/mattsolo1/grove-core@v0.2.0
  grove deps bump github.com/mattsolo1/grove-core@latest
  grove deps bump github.com/mattsolo1/grove-core`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// If push is set, commit must also be set
			if pushFlag {
				commitFlag = true
			}
			return runDepsBump(args[0], commitFlag, pushFlag)
		},
	}

	cmd.Flags().BoolVar(&commitFlag, "commit", false, "Create a git commit in each updated submodule")
	cmd.Flags().BoolVar(&pushFlag, "push", false, "Push the commit to origin (implies --commit)")

	return cmd
}

func newDepsSyncCmd() *cobra.Command {
	var commitFlag bool
	var pushFlag bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Update all Grove dependencies to their latest versions",
		Long: `Synchronize all Grove dependencies across all submodules.

This command automatically discovers all Grove dependencies (github.com/mattsolo1/*)
in each submodule and updates them to their latest versions. This is useful for
keeping the entire ecosystem in sync after multiple tools have been released.

Examples:
  grove deps sync
  grove deps sync --commit
  grove deps sync --push`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// If push is set, commit must also be set
			if pushFlag {
				commitFlag = true
			}
			return runDepsSync(commitFlag, pushFlag)
		},
	}

	cmd.Flags().BoolVar(&commitFlag, "commit", false, "Create a git commit in each updated submodule")
	cmd.Flags().BoolVar(&pushFlag, "push", false, "Push the commit to origin (implies --commit)")

	return cmd
}

func runDepsBump(moduleSpec string, commit, push bool) error {
	// Parse module path and version
	parts := strings.Split(moduleSpec, "@")
	modulePath := parts[0]
	version := ""
	if len(parts) > 1 {
		version = parts[1]
	}

	// Resolve version if needed
	if version == "" || version == "latest" {
		resolvedVersion, err := getLatestModuleVersion(modulePath)
		if err != nil {
			return fmt.Errorf("failed to resolve latest version for %s: %w", modulePath, err)
		}
		version = resolvedVersion
		fmt.Printf("Resolved %s to version %s\n", modulePath, version)
	}

	fmt.Printf("Bumping dependency %s to %s...\n\n", modulePath, version)

	// Find all workspaces
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find workspace root: %w", err)
	}

	workspaces, err := workspace.Discover(rootDir)
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}

	// Track results
	var updated []string
	var skipped []string
	var failed []string

	// Process each workspace
	for _, ws := range workspaces {
		// Skip the root workspace
		if ws == rootDir {
			continue
		}

		// Get workspace name for logging
		wsName := filepath.Base(ws)

		// Check if this workspace has the dependency
		goModPath := filepath.Join(ws, "go.mod")
		goModContent, err := os.ReadFile(goModPath)
		if err != nil {
			fmt.Printf("SKIPPED   %s (no go.mod)\n", wsName)
			skipped = append(skipped, wsName)
			continue
		}

		// Check if this is the module itself
		if strings.Contains(string(goModContent), "module "+modulePath) {
			fmt.Printf("SKIPPED   %s (cannot update self)\n", wsName)
			skipped = append(skipped, wsName)
			continue
		}

		if !strings.Contains(string(goModContent), modulePath) {
			fmt.Printf("SKIPPED   %s (dependency not found)\n", wsName)
			skipped = append(skipped, wsName)
			continue
		}

		// Update the dependency
		fmt.Printf("UPDATING  %s...", wsName)

		// Run go get
		if err := runGoGet(ws, modulePath, version); err != nil {
			fmt.Printf(" FAILED: %v\n", err)
			failed = append(failed, wsName)
			continue
		}

		// Run go mod tidy
		if err := runGoModTidy(ws); err != nil {
			fmt.Printf(" FAILED: %v\n", err)
			failed = append(failed, wsName)
			continue
		}

		fmt.Printf(" done\n")
		updated = append(updated, wsName)

		// Handle git operations if requested
		if commit {
			if err := handleGitOperations(ws, wsName, modulePath, version, push); err != nil {
				fmt.Printf("  WARNING: git operations failed: %v\n", err)
			}
		}
	}

	// Print summary
	fmt.Printf("\n")
	fmt.Printf("Summary:\n")
	fmt.Printf("  Updated: %d modules\n", len(updated))
	if len(updated) > 0 {
		for _, name := range updated {
			fmt.Printf("    - %s\n", name)
		}
	}
	fmt.Printf("  Skipped: %d modules\n", len(skipped))
	fmt.Printf("  Failed:  %d modules\n", len(failed))
	if len(failed) > 0 {
		for _, name := range failed {
			fmt.Printf("    - %s\n", name)
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("some modules failed to update")
	}

	return nil
}

func getLatestModuleVersion(modulePath string) (string, error) {
	// Use go list to get module info
	cmd := exec.Command("go", "list", "-m", "-json", modulePath+"@latest")

	// Set up environment for private modules
	cmd.Env = append(os.Environ(),
		"GOPRIVATE=github.com/mattsolo1/*",
		"GOPROXY=direct",
	)

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go list failed: %w", err)
	}

	// Parse JSON output
	var modInfo struct {
		Version string `json:"Version"`
	}
	if err := json.Unmarshal(output, &modInfo); err != nil {
		return "", fmt.Errorf("failed to parse go list output: %w", err)
	}

	if modInfo.Version == "" {
		return "", fmt.Errorf("no version found")
	}

	return modInfo.Version, nil
}

func runGoGet(workspacePath, modulePath, version string) error {
	cmd := exec.Command("go", "get", modulePath+"@"+version)
	cmd.Dir = workspacePath

	// Set up environment for private modules
	cmd.Env = append(os.Environ(),
		"GOPRIVATE=github.com/mattsolo1/*",
		"GOPROXY=direct",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, string(output))
	}
	return nil
}

func runGoModTidy(workspacePath string) error {
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = workspacePath

	// Set up environment for private modules
	cmd.Env = append(os.Environ(),
		"GOPRIVATE=github.com/mattsolo1/*",
		"GOPROXY=direct",
	)

	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func handleGitOperations(workspacePath, wsName, modulePath, version string, push bool) error {
	// Check for changes
	status, err := git.GetStatus(workspacePath)
	if err != nil {
		return fmt.Errorf("failed to get git status: %w", err)
	}

	if !status.IsDirty {
		fmt.Printf("  INFO: No changes to commit in %s\n", wsName)
		return nil
	}

	// Add go.mod and go.sum
	addCmd := exec.Command("git", "add", "go.mod", "go.sum")
	addCmd.Dir = workspacePath
	if err := addCmd.Run(); err != nil {
		return fmt.Errorf("failed to stage changes: %w", err)
	}

	// Create commit
	commitMsg := fmt.Sprintf("chore(deps): bump %s to %s", modulePath, version)
	commitCmd := exec.Command("git", "commit", "-m", commitMsg)
	commitCmd.Dir = workspacePath
	if err := commitCmd.Run(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}
	fmt.Printf("  INFO: Created commit in %s\n", wsName)

	// Push if requested
	if push {
		pushCmd := exec.Command("git", "push", "origin", "HEAD")
		pushCmd.Dir = workspacePath
		if err := pushCmd.Run(); err != nil {
			return fmt.Errorf("failed to push: %w", err)
		}
		fmt.Printf("  INFO: Pushed changes in %s\n", wsName)
	}

	return nil
}

func runDepsSync(commit, push bool) error {
	fmt.Println("Synchronizing all Grove dependencies to latest versions...")
	fmt.Println()

	// Find all workspaces
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find workspace root: %w", err)
	}

	workspaces, err := workspace.Discover(rootDir)
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}

	// Track Grove dependencies by workspace
	type workspaceDeps struct {
		path string
		name string
		deps []string
	}
	var workspaceList []workspaceDeps

	// Track all unique Grove dependencies for version resolution
	uniqueDeps := make(map[string]bool)

	// First pass: discover all Grove dependencies per workspace
	for _, ws := range workspaces {
		// Skip the root workspace
		if ws == rootDir {
			continue
		}

		wsName := filepath.Base(ws)

		// Read go.mod to find Grove dependencies
		goModPath := filepath.Join(ws, "go.mod")
		goModContent, err := os.ReadFile(goModPath)
		if err != nil {
			continue
		}

		// Check if this is a Grove module itself
		var isGroveModule string
		if match := strings.Contains(string(goModContent), "module github.com/mattsolo1/"); match {
			for _, line := range strings.Split(string(goModContent), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "module github.com/mattsolo1/") {
					isGroveModule = strings.Fields(line)[1]
					break
				}
			}
		}

		// Parse dependencies
		var wsDeps []string
		lines := strings.Split(string(goModContent), "\n")
		inRequire := false
		for _, line := range lines {
			line = strings.TrimSpace(line)

			if line == "require (" {
				inRequire = true
				continue
			}
			if inRequire && line == ")" {
				inRequire = false
				continue
			}

			if inRequire || strings.HasPrefix(line, "require ") {
				// Extract Grove dependencies
				if strings.Contains(line, "github.com/mattsolo1/") {
					parts := strings.Fields(line)
					if len(parts) >= 1 {
						dep := parts[0]
						if strings.HasPrefix(dep, "github.com/mattsolo1/") && dep != isGroveModule {
							wsDeps = append(wsDeps, dep)
							uniqueDeps[dep] = true
						}
					}
				}
			}
		}

		if len(wsDeps) > 0 {
			workspaceList = append(workspaceList, workspaceDeps{
				path: ws,
				name: wsName,
				deps: wsDeps,
			})
		}
	}

	if len(uniqueDeps) == 0 {
		fmt.Println("No Grove dependencies found.")
		return nil
	}

	// Resolve all versions upfront
	depVersions := make(map[string]string)
	fmt.Printf("Resolving versions for %d unique Grove dependencies...\n", len(uniqueDeps))
	for dep := range uniqueDeps {
		version, err := getLatestModuleVersion(dep)
		if err != nil {
			fmt.Printf("  WARNING: Failed to resolve %s: %v\n", dep, err)
			continue
		}
		depVersions[dep] = version
		fmt.Printf("  %s -> %s\n", dep, version)
	}
	fmt.Println()

	// Update each workspace
	var updatedWorkspaces []string
	var failedWorkspaces []string

	for _, ws := range workspaceList {
		fmt.Printf("Updating %s...\n", ws.name)

		// Update all dependencies for this workspace
		hasUpdates := false
		for _, dep := range ws.deps {
			version, ok := depVersions[dep]
			if !ok {
				continue
			}

			fmt.Printf("  %s -> %s\n", dep, version)
			if err := runGoGet(ws.path, dep, version); err != nil {
				fmt.Printf("    ERROR: %v\n", err)
				failedWorkspaces = append(failedWorkspaces, ws.name)
				continue
			}
			hasUpdates = true
		}

		if hasUpdates {
			// Run go mod tidy
			if err := runGoModTidy(ws.path); err != nil {
				fmt.Printf("  ERROR running go mod tidy: %v\n", err)
				failedWorkspaces = append(failedWorkspaces, ws.name)
			} else {
				updatedWorkspaces = append(updatedWorkspaces, ws.name)

				// Handle git operations if requested
				if commit {
					commitMsg := "chore(deps): update Grove dependencies to latest versions"
					if err := handleGitOperations(ws.path, ws.name, "multiple dependencies", "latest", push); err != nil {
						// Override the commit message
						addCmd := exec.Command("git", "add", "go.mod", "go.sum")
						addCmd.Dir = ws.path
						if err := addCmd.Run(); err == nil {
							commitCmd := exec.Command("git", "commit", "-m", commitMsg)
							commitCmd.Dir = ws.path
							if err := commitCmd.Run(); err == nil {
								fmt.Printf("  INFO: Created commit in %s\n", ws.name)

								if push {
									pushCmd := exec.Command("git", "push", "origin", "HEAD")
									pushCmd.Dir = ws.path
									if err := pushCmd.Run(); err == nil {
										fmt.Printf("  INFO: Pushed changes in %s\n", ws.name)
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Summary
	fmt.Println("\nSummary:")
	fmt.Printf("  Updated: %d workspaces\n", len(updatedWorkspaces))
	if len(updatedWorkspaces) > 0 {
		for _, name := range updatedWorkspaces {
			fmt.Printf("    - %s\n", name)
		}
	}
	fmt.Printf("  Failed: %d workspaces\n", len(failedWorkspaces))
	if len(failedWorkspaces) > 0 {
		for _, name := range failedWorkspaces {
			fmt.Printf("    - %s\n", name)
		}
	}

	if len(failedWorkspaces) > 0 {
		return fmt.Errorf("failed to update %d workspaces", len(failedWorkspaces))
	}

	fmt.Println("\nAll Grove dependencies synchronized successfully!")
	return nil
}

func newDepsTreeCmd() *cobra.Command {
	var showVersions bool
	var showExternal bool
	var filterRepos []string

	cmd := &cobra.Command{
		Use:   "tree [repo]",
		Short: "Display dependency tree visualization",
		Long: `Display a tree visualization of dependencies in the Grove ecosystem.

Without arguments, shows the complete dependency graph for all repositories.
With a repository name, shows dependencies for that specific repository.

Examples:
  grove deps tree                  # Show complete dependency graph
  grove deps tree grove-meta       # Show dependencies of grove-meta
  grove deps tree --versions       # Include version information
  grove deps tree --external       # Include external (non-Grove) dependencies`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var focusRepo string
			if len(args) > 0 {
				focusRepo = args[0]
			}
			return runDepsTree(focusRepo, showVersions, showExternal, filterRepos)
		},
	}

	cmd.Flags().BoolVar(&showVersions, "versions", false, "Show version information")
	cmd.Flags().BoolVar(&showExternal, "external", false, "Include external dependencies")
	cmd.Flags().StringSliceVar(&filterRepos, "filter", []string{}, "Filter to specific repositories")

	return cmd
}

func runDepsTree(focusRepo string, showVersions, showExternal bool, filterRepos []string) error {
	// Find workspace root
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find workspace root: %w", err)
	}

	// Discover all workspaces
	workspaces, err := workspace.Discover(rootDir)
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}

	// Build dependency graph
	fmt.Println("Building dependency graph...")
	graph, err := depsgraph.BuildGraph(rootDir, workspaces)
	if err != nil {
		return fmt.Errorf("failed to build dependency graph: %w", err)
	}

	// If focusing on a specific repo, ensure it exists
	if focusRepo != "" {
		if _, exists := graph.GetNode(focusRepo); !exists {
			return fmt.Errorf("repository '%s' not found", focusRepo)
		}
	}

	fmt.Println()

	// Display the tree
	if focusRepo != "" {
		// Show dependencies for a specific repository
		displayRepoTree(graph, focusRepo, showVersions, showExternal)
	} else {
		// Show complete dependency graph
		displayFullTree(graph, showVersions, showExternal, filterRepos)
	}

	return nil
}

func displayRepoTree(graph *depsgraph.Graph, repoName string, showVersions, showExternal bool) {
	node, _ := graph.GetNode(repoName)
	fmt.Printf("%s\n", repoName)
	
	deps := graph.GetDependencies(repoName)
	if len(deps) == 0 {
		fmt.Println("└── (no dependencies)")
		return
	}

	// Sort dependencies for consistent output
	sort.Strings(deps)

	// Print each dependency with tree characters
	for i, dep := range deps {
		isLast := i == len(deps)-1
		prefix := "├── "
		if isLast {
			prefix = "└── "
		}

		// Check if it's a Grove dependency
		isGrove := false
		for _, n := range graph.GetAllNodes() {
			if n.Path == dep {
				isGrove = true
				break
			}
		}

		if !isGrove && !showExternal {
			continue
		}

		// Format the dependency name
		depDisplay := dep
		if isGrove {
			// Find the short name
			for name, n := range graph.GetAllNodes() {
				if n.Path == dep {
					depDisplay = name
					break
				}
			}
		}

		fmt.Printf("%s%s", prefix, depDisplay)
		if showVersions && node != nil {
			// Could enhance this to show actual versions from go.mod
			fmt.Printf(" (%s)", node.Version)
		}
		fmt.Println()
	}
}

func displayFullTree(graph *depsgraph.Graph, showVersions, showExternal bool, filterRepos []string) {
	// Get topologically sorted levels
	levels, err := graph.TopologicalSort()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// Create filter map if needed
	filterMap := make(map[string]bool)
	if len(filterRepos) > 0 {
		for _, repo := range filterRepos {
			filterMap[repo] = true
		}
	}

	fmt.Println("Dependency Tree (by levels):")
	fmt.Println("===========================")
	
	for i, level := range levels {
		fmt.Printf("\nLevel %d (no dependencies on later levels):\n", i+1)
		
		// Sort repos in level for consistent output
		sort.Strings(level)
		
		for _, repo := range level {
			// Apply filter if specified
			if len(filterMap) > 0 && !filterMap[repo] {
				continue
			}

			fmt.Printf("  %s\n", repo)
			
			// Show dependencies
			deps := graph.GetDependencies(repo)
			if len(deps) > 0 {
				for j, dep := range deps {
					isLast := j == len(deps)-1
					prefix := "  ├── "
					if isLast {
						prefix = "  └── "
					}

					// Check if it's a Grove dependency
					isGrove := false
					depName := dep
					for name, n := range graph.GetAllNodes() {
						if n.Path == dep {
							isGrove = true
							depName = name
							break
						}
					}

					if !isGrove && !showExternal {
						continue
					}

					fmt.Printf("%s%s", prefix, depName)
					
					// Show which level this dependency is in
					if isGrove {
						for levelIdx, levelRepos := range levels {
							for _, levelRepo := range levelRepos {
								if levelRepo == depName {
									if levelIdx < i {
										fmt.Printf(" (Level %d)", levelIdx+1)
									}
									break
								}
							}
						}
					} else {
						fmt.Printf(" (external)")
					}
					fmt.Println()
				}
			} else {
				fmt.Println("  └── (no dependencies)")
			}
		}
	}
	
	// Show summary
	fmt.Printf("\nTotal repositories: %d\n", len(graph.GetAllNodes()))
	fmt.Printf("Dependency levels: %d\n", len(levels))
}
