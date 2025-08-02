package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	
	"github.com/grovepm/grove/pkg/workspace"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/git"
	"github.com/spf13/cobra"
)

// ANSI color codes matching GitStatusDisplay.svelte
const (
	colorReset      = "\033[0m"
	colorClean      = "\033[32;1m"    // #00ff00 - bright green
	colorDirty      = "\033[33;1m"    // #ffaa00 - orange/yellow
	colorUntracked  = "\033[31;1m"    // #ff4444 - red
	colorModified   = "\033[33;1m"    // #ffaa00 - orange
	colorStaged     = "\033[36;1m"    // #4ecdc4 - cyan
	colorAhead      = "\033[96;1m"    // #95e1d3 - light cyan
	colorBehind     = "\033[91;1m"    // #f38181 - light red
	colorGray       = "\033[90m"      // #808080 - gray
)

// colorize applies ANSI color codes to text
func colorize(text string, color string) string {
	if os.Getenv("NO_COLOR") != "" {
		return text
	}
	return color + text + colorReset
}

func newGitStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show aggregated git status for all workspaces",
		Long:  "Display a unified table showing the git status of all discovered workspaces",
		Args:  cobra.NoArgs,
		RunE:  runGitStatus,
	}
	
	return cmd
}

func runGitStatus(cmd *cobra.Command, args []string) error {
	logger := cli.GetLogger(cmd)
	
	// Find root directory with workspaces
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find workspace root: %w", err)
	}
	
	// Discover workspaces
	workspaces, err := workspace.Discover(rootDir)
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}
	
	if len(workspaces) == 0 {
		return fmt.Errorf("no workspaces found")
	}
	
	logger.WithField("count", len(workspaces)).Debug("Discovered workspaces")
	
	// Collect status for each workspace
	type workspaceStatus struct {
		Name   string
		Status *git.StatusInfo
		Error  error
	}
	
	var statuses []workspaceStatus
	
	for _, ws := range workspaces {
		wsName := workspace.GetWorkspaceName(ws, rootDir)
		status, err := git.GetStatus(ws)
		
		statuses = append(statuses, workspaceStatus{
			Name:   wsName,
			Status: status,
			Error:  err,
		})
	}
	
	// Output as table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "WORKSPACE\tBRANCH\tSTATUS\tDETAILS")
	fmt.Fprintln(w, "---------\t------\t------\t-------")
	
	for _, ws := range statuses {
		if ws.Error != nil {
			fmt.Fprintf(w, "%s\t-\t%s\t%s\n", 
				ws.Name, 
				colorize("ERROR", colorUntracked),
				colorize(ws.Error.Error(), colorGray))
			continue
		}
		
		status := ws.Status
		
		// Format status column with colors
		statusStr := colorize("✓ Clean", colorClean)
		if status.IsDirty {
			statusStr = colorize("● Dirty", colorDirty)
		}
		
		// Format details column
		var details []string
		
		// Add upstream status
		if status.HasUpstream {
			if status.AheadCount > 0 || status.BehindCount > 0 {
				aheadStr := ""
				behindStr := ""
				if status.AheadCount > 0 {
					aheadStr = colorize(fmt.Sprintf("↑%d", status.AheadCount), colorAhead)
				}
				if status.BehindCount > 0 {
					behindStr = colorize(fmt.Sprintf("↓%d", status.BehindCount), colorBehind)
				}
				if aheadStr != "" && behindStr != "" {
					details = append(details, aheadStr+"/"+behindStr)
				} else if aheadStr != "" {
					details = append(details, aheadStr)
				} else {
					details = append(details, behindStr)
				}
			}
		} else {
			details = append(details, colorize("no upstream", colorGray))
		}
		
		// Add file counts with colors
		if status.ModifiedCount > 0 {
			details = append(details, colorize(fmt.Sprintf("M:%d", status.ModifiedCount), colorModified))
		}
		if status.StagedCount > 0 {
			details = append(details, colorize(fmt.Sprintf("S:%d", status.StagedCount), colorStaged))
		}
		if status.UntrackedCount > 0 {
			details = append(details, colorize(fmt.Sprintf("?:%d", status.UntrackedCount), colorUntracked))
		}
		
		detailsStr := strings.Join(details, " ")
		if detailsStr == "" && status.HasUpstream {
			detailsStr = "up to date"
		}
		
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", 
			ws.Name, 
			status.Branch,
			statusStr,
			detailsStr,
		)
	}
	
	return w.Flush()
}