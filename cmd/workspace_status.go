package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/config"
	"github.com/mattsolo1/grove-core/git"
	"github.com/mattsolo1/grove-meta/pkg/gh"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

// Workspace status specific color styles
var (
	cleanStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00")).Bold(true)
	dirtyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Bold(true)
	untrackedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff4444")).Bold(true)
	modifiedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Bold(true)
	stagedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ecdc4")).Bold(true)
	aheadStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#95e1d3")).Bold(true)
	behindStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#f38181")).Bold(true)
	grayStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
	// errorStyle is defined in styles.go
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

	cmd.Flags().String("cols", "git,main-ci,my-prs,cx,release", "Comma-separated columns to display (e.g., git,main-ci,my-prs,cx,release)")

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
		TotalFiles  int   `json:"total_files"`
		TotalTokens int   `json:"total_tokens"`
		TotalSize   int64 `json:"total_size"`
	}

	// ReleaseInfo represents release information for a workspace
	type ReleaseInfo struct {
		LatestTag    string
		CommitsAhead int
	}

	// Collect status for each workspace
	type workspaceStatusInfo struct {
		Name    string
		Type    string // Project type (go, maturin, etc.)
		Git     *git.StatusInfo
		Context *ContextStats
		Release *ReleaseInfo
		MainCI  string
		MyPRs   string
		GitErr  error
		CxErr   error
		RelErr  error
		CIErr   error
	}

	// Process workspaces concurrently
	statusChannel := make(chan workspaceStatusInfo, len(workspaces))
	var wg sync.WaitGroup

	for _, ws := range workspaces {
		wg.Add(1)
		go func(wsPath string) {
			defer wg.Done()
			wsName := workspace.GetWorkspaceName(wsPath, rootDir)

			statusInfo := workspaceStatusInfo{
				Name: wsName,
			}

			// Determine project type
			projectType := "go" // default
			groveYmlPath := filepath.Join(wsPath, "grove.yml")
			if cfg, err := config.Load(groveYmlPath); err == nil {
				var typeStr string
				if err := cfg.UnmarshalExtension("type", &typeStr); err == nil && typeStr != "" {
					projectType = typeStr
				}
			}
			statusInfo.Type = projectType

			// Get git status if requested
			if colMap["git"] {
				gitStatus, gitErr := git.GetStatus(wsPath)
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
				cmd.Dir = wsPath
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

			// Get release info if requested
			if colMap["release"] {
				var releaseInfo *ReleaseInfo
				var relErr error

				// Get the latest tag
				cmd := exec.Command("git", "describe", "--tags", "--abbrev=0")
				cmd.Dir = wsPath
				tagOutput, err := cmd.Output()

				if err != nil {
					// No tags found
					releaseInfo = &ReleaseInfo{
						LatestTag:    "none",
						CommitsAhead: -1,
					}
				} else {
					latestTag := strings.TrimSpace(string(tagOutput))

					// Count commits between latest tag and HEAD
					cmd = exec.Command("git", "rev-list", "--count", latestTag+"..HEAD")
					cmd.Dir = wsPath
					countOutput, err := cmd.Output()

					commitsAhead := 0
					if err == nil {
						fmt.Sscanf(strings.TrimSpace(string(countOutput)), "%d", &commitsAhead)
					}

					releaseInfo = &ReleaseInfo{
						LatestTag:    latestTag,
						CommitsAhead: commitsAhead,
					}
				}

				statusInfo.Release = releaseInfo
				statusInfo.RelErr = relErr
			}

			// Get CI/CD status if requested
			if colMap["main-ci"] || colMap["my-prs"] {
				var mainCI, myPRs string
				var err1, err2 error

				if colMap["main-ci"] {
					mainCI, err1 = gh.GetCurrentBranchCIStatus(wsPath)
					statusInfo.MainCI = mainCI
				}

				if colMap["my-prs"] {
					myPRs, err2 = gh.GetMyPRsStatus(wsPath)
					statusInfo.MyPRs = myPRs
				}

				if err1 != nil || err2 != nil {
					statusInfo.CIErr = fmt.Errorf("main: %v, prs: %v", err1, err2)
				}
			}

			statusChannel <- statusInfo
		}(ws)
	}

	// Wait for all goroutines to complete and close the channel
	go func() {
		wg.Wait()
		close(statusChannel)
	}()

	// Collect results from channel
	var statuses []workspaceStatusInfo
	for status := range statusChannel {
		statuses = append(statuses, status)
	}

	// Sort by workspace name for consistent output
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Name < statuses[j].Name
	})

	// Build table headers dynamically
	headers := []string{"WORKSPACE"}
	if colMap["type"] {
		headers = append(headers, "TYPE")
	}
	if colMap["git"] {
		headers = append(headers, "BRANCH", "GIT STATUS", "CHANGES")
	}
	if colMap["main-ci"] {
		headers = append(headers, "CI STATUS")
	}
	if colMap["my-prs"] {
		headers = append(headers, "MY PRS")
	}
	if colMap["cx"] {
		headers = append(headers, "CX FILES", "CX TOKENS", "CX SIZE")
	}
	if colMap["release"] {
		headers = append(headers, "RELEASE")
	}
	headers = append(headers, "LINK")

	// Collect all rows
	var rows [][]string

	for _, ws := range statuses {
		row := []string{ws.Name}

		// Add type column if requested
		if colMap["type"] {
			row = append(row, ws.Type)
		}

		// Add git columns if requested
		if colMap["git"] {
			// Handle git error case
			if ws.GitErr != nil {
				row = append(row, "-", errorStyle.Render("ERROR"), ws.GitErr.Error())
			} else if ws.Git == nil {
				row = append(row, "-", "-", "-")
			} else {
				status := ws.Git

				// Format status column with pre-styling
				statusStr := "✓ Clean"
				styledStatus := cleanStyle.Render(statusStr)
				if status.IsDirty {
					statusStr = "● Dirty"
					styledStatus = dirtyStyle.Render(statusStr)
				}

				// Format changes column
				var changes []string

				// Add upstream status
				if status.HasUpstream {
					if status.AheadCount > 0 || status.BehindCount > 0 {
						aheadStr := ""
						behindStr := ""
						if status.AheadCount > 0 {
							aheadStr = aheadStyle.Render(fmt.Sprintf("↑%d", status.AheadCount))
						}
						if status.BehindCount > 0 {
							behindStr = behindStyle.Render(fmt.Sprintf("↓%d", status.BehindCount))
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
					changes = append(changes, modifiedStyle.Render(fmt.Sprintf("M:%d", status.ModifiedCount)))
				}
				if status.StagedCount > 0 {
					changes = append(changes, stagedStyle.Render(fmt.Sprintf("S:%d", status.StagedCount)))
				}
				if status.UntrackedCount > 0 {
					changes = append(changes, untrackedStyle.Render(fmt.Sprintf("?:%d", status.UntrackedCount)))
				}

				changesStr := strings.Join(changes, " ")
				if changesStr == "" && status.HasUpstream {
					changesStr = "up to date"
				}

				row = append(row, status.Branch, styledStatus, changesStr)
			}
		}

		// Add CI/CD columns if requested
		if colMap["main-ci"] {
			if ws.CIErr != nil {
				row = append(row, "? Unknown")
			} else {
				row = append(row, ws.MainCI)
			}
		}

		if colMap["my-prs"] {
			if ws.CIErr != nil {
				row = append(row, "? Unknown")
			} else {
				row = append(row, ws.MyPRs)
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

		// Add release column if requested
		if colMap["release"] {
			if ws.Release != nil && ws.RelErr == nil {
				releaseStr := ws.Release.LatestTag
				if ws.Release.CommitsAhead > 0 {
					releaseStr = fmt.Sprintf("%s (↑%d)", ws.Release.LatestTag, ws.Release.CommitsAhead)
					// Style based on how many commits ahead
					if ws.Release.CommitsAhead > 20 {
						releaseStr = errorStyle.Render(releaseStr)
					} else if ws.Release.CommitsAhead > 10 {
						releaseStr = dirtyStyle.Render(releaseStr)
					} else {
						releaseStr = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffff00")).Render(releaseStr)
					}
				}
				row = append(row, releaseStr)
			} else if ws.RelErr != nil {
				row = append(row, "error")
			} else {
				row = append(row, "-")
			}
		}

		// Add GitHub link column
		githubURL := fmt.Sprintf("https://github.com/mattsolo1/%s", ws.Name)
		row = append(row, githubURL)

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

	// Apply styling - only for headers since content is pre-styled
	t.StyleFunc(func(row, col int) lipgloss.Style {
		if row == 0 {
			return headerStyle
		}
		// Return minimal style to preserve pre-styled content
		return lipgloss.NewStyle().Padding(0, 1)
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
