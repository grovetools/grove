package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/git"
	"github.com/spf13/cobra"
)

// Color styles using lipgloss
var (
	cleanStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00")).Bold(true)
	dirtyStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Bold(true)
	untrackedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff4444")).Bold(true)
	modifiedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Bold(true)
	stagedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ecdc4")).Bold(true)
	aheadStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#95e1d3")).Bold(true)
	behindStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#f38181")).Bold(true)
	grayStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
	errorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff4444")).Bold(true)
)

// Removed init function - command is now added in workspace.go

// getWorkspaceCmd finds the workspace command in the root command tree
func getWorkspaceCmd() *cobra.Command {
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "workspace" {
			return cmd
		}
	}
	return nil
}

func newWorkspaceStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show aggregated status for all workspaces",
		Long:  "Display a unified dashboard showing the status of all discovered workspaces",
		Args:  cobra.NoArgs,
		RunE:  runWorkspaceStatus,
	}
	
	cmd.Flags().String("cols", "git,cx", "Comma-separated columns to display (e.g., git,cx)")
	
	return cmd
}


func runWorkspaceStatus(cmd *cobra.Command, args []string) error {
	logger := cli.GetLogger(cmd)
	
	// Parse column selection
	colsStr, _ := cmd.Flags().GetString("cols")
	selectedCols := strings.Split(colsStr, ",")
	colMap := make(map[string]bool)
	for _, c := range selectedCols {
		colMap[strings.TrimSpace(c)] = true
	}
	
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
	
	// ContextStats represents the structure from grove-context
	type ContextStats struct {
		TotalFiles   int    `json:"total_files"`
		TotalTokens  int    `json:"total_tokens"`
		TotalSize    int64  `json:"total_size"`
	}
	
	// Collect status for each workspace
	type workspaceStatusInfo struct {
		Name    string
		Git     *git.StatusInfo
		Context *ContextStats
		GitErr  error
		CxErr   error
	}
	
	var statuses []workspaceStatusInfo
	
	for _, ws := range workspaces {
		wsName := workspace.GetWorkspaceName(ws, rootDir)
		
		statusInfo := workspaceStatusInfo{
			Name: wsName,
		}
		
		// Get git status if requested
		if colMap["git"] {
			gitStatus, gitErr := git.GetStatus(ws)
			statusInfo.Git = gitStatus
			statusInfo.GitErr = gitErr
		}
		
		// Get context stats if requested
		if colMap["cx"] {
			var cxStats *ContextStats
			var cxErr error
			
			// Try to run cx stats --json
			// First try grove-context binary, then fall back to cx in PATH
			cxCmd := "cx"
			if gcxPath := filepath.Join(rootDir, "grove-context", "grove-cx"); fileExists(gcxPath) {
				cxCmd = gcxPath
			} else if gcxPath := filepath.Join(rootDir, "grove-context", "cx"); fileExists(gcxPath) {
				cxCmd = gcxPath
			}
			
			cmd := exec.Command(cxCmd, "stats", "--json")
			cmd.Dir = ws
			output, err := cmd.Output()
			
			if err == nil && len(output) > 0 {
				// Check if output starts with JSON array
				trimmed := strings.TrimSpace(string(output))
				if strings.HasPrefix(trimmed, "[") {
					// Parse JSON output - cx returns an array
					var statsArray []ContextStats
					if err := json.Unmarshal([]byte(trimmed), &statsArray); err == nil && len(statsArray) > 0 {
						cxStats = &statsArray[0]
					} else {
						cxErr = fmt.Errorf("failed to parse cx output: %v", err)
					}
				} else {
					// Non-JSON output, cx might not support --json flag properly
					cxErr = fmt.Errorf("cx does not support JSON output")
				}
			} else if err != nil {
				// Check if it's just a missing rules file
				if strings.Contains(err.Error(), "exit status") {
					cxErr = fmt.Errorf("no rules file")
				} else {
					cxErr = err
				}
			}
			
			statusInfo.Context = cxStats
			statusInfo.CxErr = cxErr
		}
		
		statuses = append(statuses, statusInfo)
	}
	
	// Build table headers dynamically
	headers := []string{"WORKSPACE"}
	if colMap["git"] {
		headers = append(headers, "BRANCH", "GIT STATUS", "CHANGES")
	}
	if colMap["cx"] {
		headers = append(headers, "CX FILES", "CX TOKENS", "CX SIZE")
	}
	
	// Collect all rows
	var rows [][]string
	
	for _, ws := range statuses {
		row := []string{ws.Name}
		
		// Add git columns if requested
		if colMap["git"] {
			// Handle git error case
			if ws.GitErr != nil {
				row = append(row, "-", "ERROR", ws.GitErr.Error())
			} else if ws.Git == nil {
				row = append(row, "-", "-", "-")
			} else {
				status := ws.Git
				
				// Format status column - we'll apply colors in the StyleFunc instead
				statusStr := "✓ Clean"
				if status.IsDirty {
					statusStr = "● Dirty"
				}
				
				// Format changes column
				var changes []string
				
				// Add upstream status
				if status.HasUpstream {
					if status.AheadCount > 0 || status.BehindCount > 0 {
						aheadStr := ""
						behindStr := ""
						if status.AheadCount > 0 {
							aheadStr = fmt.Sprintf("↑%d", status.AheadCount)
						}
						if status.BehindCount > 0 {
							behindStr = fmt.Sprintf("↓%d", status.BehindCount)
						}
						if aheadStr != "" && behindStr != "" {
							changes = append(changes, aheadStr+"/"+behindStr)
						} else if aheadStr != "" {
							changes = append(changes, aheadStr)
						} else {
							changes = append(changes, behindStr)
						}
					}
				} else {
					changes = append(changes, "no upstream")
				}
				
				// Add file counts
				if status.ModifiedCount > 0 {
					changes = append(changes, fmt.Sprintf("M:%d", status.ModifiedCount))
				}
				if status.StagedCount > 0 {
					changes = append(changes, fmt.Sprintf("S:%d", status.StagedCount))
				}
				if status.UntrackedCount > 0 {
					changes = append(changes, fmt.Sprintf("?:%d", status.UntrackedCount))
				}
				
				changesStr := strings.Join(changes, " ")
				if changesStr == "" && status.HasUpstream {
					changesStr = "up to date"
				}
				
				row = append(row, status.Branch, statusStr, changesStr)
			}
		}
		
		// Add context columns if requested
		if colMap["cx"] {
			if ws.Context != nil && ws.CxErr == nil {
				row = append(row, 
					fmt.Sprintf("%d", ws.Context.TotalFiles),
					formatTokens(ws.Context.TotalTokens),
					formatBytes(ws.Context.TotalSize))
			} else if ws.CxErr != nil {
				// Show "n/a" for context errors (e.g., no rules file)
				row = append(row, "n/a", "n/a", "n/a")
			} else {
				row = append(row, "-", "-", "-")
			}
		}
		
		rows = append(rows, row)
	}
	
	// Create a simple table style
	re := lipgloss.NewRenderer(os.Stdout)
	
	baseStyle := re.NewStyle().Padding(0, 1)
	headerStyle := baseStyle.Copy().Bold(true).Foreground(lipgloss.Color("255"))
	
	// Create the table
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Headers(headers...).
		Rows(rows...)
	
	// Apply column-specific styling
	t.StyleFunc(func(row, col int) lipgloss.Style {
		if row == 0 {
			return headerStyle
		}
		
		// For data rows, check if we need special styling
		if row > 0 && row-1 < len(rows) {
			rowData := rows[row-1]
			
			// Style git status column
			if colMap["git"] && col == 2 && len(rowData) > 2 {
				status := rowData[2]
				if strings.Contains(status, "✓") {
					return baseStyle.Copy().Foreground(lipgloss.Color("#00ff00")).Bold(true)
				} else if strings.Contains(status, "●") {
					return baseStyle.Copy().Foreground(lipgloss.Color("#ffaa00")).Bold(true)
				} else if status == "ERROR" {
					return baseStyle.Copy().Foreground(lipgloss.Color("#ff4444")).Bold(true)
				}
			}
			
			// Don't override styling for changes column - it has embedded colors
			if colMap["git"] && col == 3 {
				return lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)
			}
		}
		
		return baseStyle
	})
	
	fmt.Println(t)
	return nil
}

// formatTokens formats a token count in a human-readable way
func formatTokens(tokens int) string {
	if tokens < 1000 {
		return fmt.Sprintf("%d", tokens)
	} else if tokens < 1000000 {
		return fmt.Sprintf("~%.1fk", float64(tokens)/1000)
	} else {
		return fmt.Sprintf("~%.1fM", float64(tokens)/1000000)
	}
}

// formatBytes formats a byte count in a human-readable way
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	
	if bytes < KB {
		return fmt.Sprintf("%d B", bytes)
	} else if bytes < MB {
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	} else if bytes < GB {
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	} else {
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	}
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}