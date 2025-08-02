package cmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/grovepm/grove/pkg/aggregator"
	"github.com/spf13/cobra"
)

var (
	// Styles for plans display
	plansHeaderStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("81")).
		MarginTop(1).
		MarginBottom(0)

	planTitleStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("255"))

	planStatusPendingStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("226")).
		Bold(true)

	planStatusRunningStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("33")).
		Bold(true)

	planStatusCompletedStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("34")).
		Bold(true)

	planStatusFailedStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")).
		Bold(true)

	planMetaStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("245"))

	plansBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("81")).
		Padding(0, 1).
		MarginLeft(2).
		MarginBottom(1)

	noPlansStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("242")).
		Italic(true)
)

// Plan represents a simplified version of a flow plan
type Plan struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Repository string    `json:"repository,omitempty"`
}

func NewWorkspacePlansCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plans",
		Short: "Show flow plans for all workspaces",
		Long:  "Display plans (from flow plans) for each workspace in the monorepo",
		RunE:  runWorkspacePlans,
	}

	return cmd
}

func runWorkspacePlans(cmd *cobra.Command, args []string) error {
	// Collector function to get plans for each workspace
	collector := func(workspacePath string, workspaceName string) ([]Plan, error) {
		// Run flow plan list --json
		flowCmd := exec.Command("flow", "plan", "list", "--json")
		flowCmd.Dir = workspacePath
		
		output, err := flowCmd.Output()
		if err != nil {
			// flow might not be available in this workspace
			return []Plan{}, nil
		}

		// Try to parse as JSON first
		var plans []Plan
		if err := json.Unmarshal(output, &plans); err != nil {
			// Fallback to text parsing if JSON fails
			lines := strings.Split(string(output), "\n")
			
			// Skip header line
			for i := 1; i < len(lines); i++ {
				line := strings.TrimSpace(lines[i])
				if line == "" {
					continue
				}
				
				// Parse columns: NAME JOBS STATUS
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					plan := Plan{
						ID:     fields[0],
						Title:  fields[0],
						Status: fields[2], // Status is third column
					}
					plans = append(plans, plan)
				}
			}
		}

		return plans, nil
	}

	// Renderer function to display the results
	renderer := func(results map[string][]Plan) error {
		if len(results) == 0 {
			fmt.Println("No workspaces found.")
			return nil
		}

		// Sort workspace names for consistent output
		var workspaceNames []string
		hasAnyPlans := false
		for name, plans := range results {
			workspaceNames = append(workspaceNames, name)
			if len(plans) > 0 {
				hasAnyPlans = true
			}
		}
		sortStrings(workspaceNames)

		if !hasAnyPlans {
			fmt.Println(noPlansStyle.Render("No plans found in any workspace."))
			return nil
		}

		for _, wsName := range workspaceNames {
			plans := results[wsName]
			
			// Skip workspaces with no plans
			if len(plans) == 0 {
				continue
			}

			// Print workspace header
			header := plansHeaderStyle.Render(fmt.Sprintf("📋 %s (%d plans)", wsName, len(plans)))
			fmt.Println(header)

			// Build content for this workspace
			var lines []string
			for _, plan := range plans {
				line := formatPlan(plan)
				lines = append(lines, line)
			}

			// Join and box the content
			content := strings.Join(lines, "\n\n")
			boxed := plansBoxStyle.Render(content)
			fmt.Println(boxed)
		}

		return nil
	}

	return aggregator.Run(collector, renderer)
}

func formatPlan(plan Plan) string {
	// Format title
	title := planTitleStyle.Render(plan.Title)
	if title == "" {
		title = planTitleStyle.Render("(untitled)")
	}

	// Format status with appropriate style
	var statusStr string
	switch strings.ToLower(plan.Status) {
	case "pending", "pending_user":
		statusStr = planStatusPendingStyle.Render("⏳ " + plan.Status)
	case "running", "in_progress":
		statusStr = planStatusRunningStyle.Render("🔄 " + plan.Status)
	case "completed", "done":
		statusStr = planStatusCompletedStyle.Render("✅ " + plan.Status)
	case "failed", "error":
		statusStr = planStatusFailedStyle.Render("❌ " + plan.Status)
	default:
		statusStr = planMetaStyle.Render(plan.Status)
	}

	// Format metadata
	idStr := plan.ID
	if len(idStr) > 8 {
		idStr = idStr[:8]
	}
	
	// Include date if available
	if !plan.UpdatedAt.IsZero() {
		timeAgo := formatTimeAgo(plan.UpdatedAt)
		meta := planMetaStyle.Render(fmt.Sprintf("%s • %s", idStr, timeAgo))
		return fmt.Sprintf("%s %s\n%s", title, statusStr, meta)
	}
	
	meta := planMetaStyle.Render(idStr)
	return fmt.Sprintf("%s %s\n%s", title, statusStr, meta)
}