package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/git"
	"github.com/mattsolo1/grove-meta/pkg/aggregator"
	"github.com/mattsolo1/grove-meta/pkg/discovery"
	"github.com/spf13/cobra"
)

// WorkspaceInfo holds information about a workspace and its worktrees
type WorkspaceInfo struct {
	Name      string         `json:"name"`
	Path      string         `json:"path"`
	Worktrees []WorktreeInfo `json:"worktrees"`
}

// WorktreeInfo holds information about a single worktree
type WorktreeInfo struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	IsMain bool   `json:"is_main"`
}

func newWorkspaceListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List workspaces and their git worktrees",
		RunE:  runWorkspaceList,
	}

	return cmd
}

func runWorkspaceList(cmd *cobra.Command, args []string) error {
	opts := cli.GetOptions(cmd)

	// Collector function to get worktrees for each workspace
	collector := func(workspacePath string, workspaceName string) (WorkspaceInfo, error) {
		info := WorkspaceInfo{
			Name:      workspaceName,
			Path:      workspacePath,
			Worktrees: []WorktreeInfo{},
		}

		// Get list of worktrees
		worktrees, err := git.ListWorktreesWithStatus(workspacePath)
		if err != nil {
			// If not a git repo or no worktrees, return empty list
			return info, nil
		}

		// Convert worktrees to our format
		for _, wt := range worktrees {
			wtInfo := WorktreeInfo{
				Path:   wt.Path,
				Branch: wt.Branch,
				IsMain: wt.Path == workspacePath,
			}
			info.Worktrees = append(info.Worktrees, wtInfo)
		}

		return info, nil
	}

	// Renderer function to display the results
	renderer := func(results map[string]WorkspaceInfo) error {
		if len(results) == 0 {
			fmt.Println("No workspaces found.")
			return nil
		}

		// Convert map to slice and sort by name
		var workspaces []WorkspaceInfo
		for _, ws := range results {
			workspaces = append(workspaces, ws)
		}
		sort.Slice(workspaces, func(i, j int) bool {
			return workspaces[i].Name < workspaces[j].Name
		})

		if opts.JSONOutput {
			// Output as JSON
			jsonData, err := json.MarshalIndent(workspaces, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal JSON: %w", err)
			}
			fmt.Println(string(jsonData))
		} else {
			// Output as text
			for _, ws := range workspaces {
				fmt.Println(ws.Name)
				if len(ws.Worktrees) == 0 {
					fmt.Println("  (no git worktrees)")
				} else {
					for _, wt := range ws.Worktrees {
						// Make path relative if possible
						relPath, err := filepath.Rel(filepath.Dir(ws.Path), wt.Path)
						if err != nil {
							relPath = wt.Path
						}
						
						// Format branch info
						branchInfo := fmt.Sprintf("(%s)", wt.Branch)
						if wt.Branch == "" {
							branchInfo = "(no branch)"
						}
						
						// Add [main] marker for main worktree
						mainMarker := ""
						if wt.IsMain {
							mainMarker = " [main]"
						}
						
						fmt.Printf("  - %s %s%s\n", relPath, branchInfo, mainMarker)
					}
				}
				fmt.Println()
			}
		}

		return nil
	}

	// Discover all projects to get their paths
	projects, err := discovery.DiscoverProjects()
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}

	// Extract paths
	var workspacePaths []string
	for _, p := range projects {
		workspacePaths = append(workspacePaths, p.Path)
	}

	return aggregator.Run(collector, renderer, workspacePaths)
}