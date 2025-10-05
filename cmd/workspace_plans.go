package cmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/mattsolo1/grove-meta/pkg/aggregator"
	"github.com/mattsolo1/grove-meta/pkg/discovery"
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

	planStatusReviewStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("12")).
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

	plansTableView bool
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

	cmd.Flags().BoolVar(&plansTableView, "table", false, "Show all plans in a single table ordered by date")

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

		if plansTableView {
			return renderPlansTable(results)
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
		sort.Strings(workspaceNames)

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

func formatPlan(plan Plan) string {
	// Format title
	title := planTitleStyle.Render(plan.Title)
	if title == "" {
		title = planTitleStyle.Render("(untitled)")
	}

	// Format status with grove-flow consistent icons
	var statusStr string
	statusDisplay := plan.Status
	
	switch strings.ToLower(plan.Status) {
	case "pending", "pending_user":
		statusStr = planStatusPendingStyle.Render("⏳ " + statusDisplay)
	case "pending_llm":
		statusStr = planStatusRunningStyle.Render("🤖 " + statusDisplay)
	case "running", "in_progress":
		statusStr = planStatusRunningStyle.Render("⚡ " + statusDisplay)
	case "completed", "done":
		statusStr = planStatusCompletedStyle.Render("✓ " + statusDisplay)
	case "failed", "error", "blocked":
		statusStr = planStatusFailedStyle.Render("✗ " + statusDisplay)
	case "needs_review":
		statusStr = planStatusReviewStyle.Render("👁 " + statusDisplay)
	default:
		// Handle composite statuses (e.g., "1 completed, 2 pending")
		if strings.Contains(plan.Status, "blocked") {
			statusStr = planStatusFailedStyle.Render("🚫 " + statusDisplay)
		} else if strings.Contains(plan.Status, "completed") && strings.Contains(plan.Status, "pending") {
			statusStr = lipgloss.NewStyle().
				Foreground(lipgloss.Color("220")).
				Render("🚧 " + statusDisplay)
		} else if strings.Contains(plan.Status, "running") {
			statusStr = planStatusRunningStyle.Render("⚡ " + statusDisplay)
		} else if strings.Contains(plan.Status, "completed") {
			statusStr = planStatusCompletedStyle.Render("✓ " + statusDisplay)
		} else if strings.Contains(plan.Status, "pending") {
			statusStr = planStatusPendingStyle.Render("⏳ " + statusDisplay)
		} else {
			statusStr = planMetaStyle.Render(statusDisplay)
		}
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

func renderPlansTable(results map[string][]Plan) error {
	// Collect all plans with workspace info
	type planWithWorkspace struct {
		Plan      Plan
		Workspace string
	}

	var allPlans []planWithWorkspace
	for wsName, plans := range results {
		for _, plan := range plans {
			allPlans = append(allPlans, planWithWorkspace{
				Plan:      plan,
				Workspace: wsName,
			})
		}
	}

	if len(allPlans) == 0 {
		fmt.Println(noPlansStyle.Render("No plans found in any workspace."))
		return nil
	}

	// Sort by UpdatedAt descending (most recent first)
	sort.Slice(allPlans, func(i, j int) bool {
		// If one has zero time, put it at the end
		if allPlans[i].Plan.UpdatedAt.IsZero() && !allPlans[j].Plan.UpdatedAt.IsZero() {
			return false
		}
		if !allPlans[i].Plan.UpdatedAt.IsZero() && allPlans[j].Plan.UpdatedAt.IsZero() {
			return true
		}
		return allPlans[i].Plan.UpdatedAt.After(allPlans[j].Plan.UpdatedAt)
	})

	// Define column-specific styles
	workspaceStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("141")).
		Bold(true)
	
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("255"))
	
	timeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("243"))

	// Create table with enhanced styling
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().
			Foreground(lipgloss.Color("81"))).
		Headers("REPOSITORY", "TITLE", "STATUS", "UPDATED").
		Width(160).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == 0 {
				// Header row - left align instead of center
				return lipgloss.NewStyle().
					Foreground(lipgloss.Color("81")).
					Bold(true).
					Align(lipgloss.Left).
					Padding(0, 1)
			}
			
			// Data rows - apply column-specific styles with consistent alignment
			baseStyle := lipgloss.NewStyle().
				Align(lipgloss.Left).
				Padding(0, 1)
			
			switch col {
			case 0: // Repository column
				return workspaceStyle.Copy().
					Align(lipgloss.Left).
					Padding(0, 1)
			case 1: // Title column
				return titleStyle.Copy().
					Align(lipgloss.Left).
					Padding(0, 1)
			case 2: // Status column - already styled
				return baseStyle
			case 3: // Time column
				return timeStyle.Copy().
					Align(lipgloss.Left).
					Padding(0, 1)
			default:
				return baseStyle
			}
		})

	// Add rows
	for _, pw := range allPlans {
		plan := pw.Plan

		// Extract repository name from full workspace path
		repoName := filepath.Base(pw.Workspace)

		// Format time with enhanced styling
		timeStr := ""
		if !plan.UpdatedAt.IsZero() {
			baseTime := formatTimeAgo(plan.UpdatedAt)
			// Highlight recently active plans (within last hour)
			if time.Since(plan.UpdatedAt) < time.Hour {
				timeStr = "● " + baseTime
			} else if time.Since(plan.UpdatedAt) < 24*time.Hour {
				// Highlight plans updated today
				timeStr = "◦ " + baseTime
			} else {
				timeStr = baseTime
			}
		} else {
			timeStr = "-"
		}

		// Format status with grove-flow consistent icons and colors
		statusStr := ""
		statusDisplay := plan.Status

		switch strings.ToLower(plan.Status) {
		case "pending", "pending_user":
			statusStr = planStatusPendingStyle.Render("⏳ " + statusDisplay)
		case "pending_llm":
			statusStr = planStatusRunningStyle.Render("🤖 " + statusDisplay)
		case "running", "in_progress":
			statusStr = planStatusRunningStyle.Render("⚡ " + statusDisplay)
		case "completed", "done":
			statusStr = planStatusCompletedStyle.Render("✓ " + statusDisplay)
		case "failed", "error", "blocked":
			statusStr = planStatusFailedStyle.Render("✗ " + statusDisplay)
		case "needs_review":
			statusStr = planStatusReviewStyle.Render("👁 " + statusDisplay)
		default:
			// Handle composite statuses (e.g., "1 completed, 2 pending")
			if strings.Contains(plan.Status, "blocked") {
				statusStr = planStatusFailedStyle.Render("🚫 " + statusDisplay)
			} else if strings.Contains(plan.Status, "completed") && strings.Contains(plan.Status, "pending") {
				statusStr = lipgloss.NewStyle().
					Foreground(lipgloss.Color("220")).
					Render("🚧 " + statusDisplay)
			} else if strings.Contains(plan.Status, "running") {
				statusStr = planStatusRunningStyle.Render("⚡ " + statusDisplay)
			} else if strings.Contains(plan.Status, "completed") {
				statusStr = planStatusCompletedStyle.Render("✓ " + statusDisplay)
			} else if strings.Contains(plan.Status, "pending") {
				statusStr = planStatusPendingStyle.Render("⏳ " + statusDisplay)
			} else {
				statusStr = planMetaStyle.Render(statusDisplay)
			}
		}

		// Format title - don't truncate, let table handle wrapping
		titleStr := plan.Title
		if titleStr == "" {
			// Use ID as fallback if no title
			titleStr = plan.ID
		}

		// Add the row to the table (trim any extra spaces)
		t.Row(
			strings.TrimSpace(repoName),
			strings.TrimSpace(titleStr),
			statusStr,  // Don't trim statusStr as it has styled content
			strings.TrimSpace(timeStr),
		)
	}

	// Add a title above the table
	tableTitle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("81")).
		MarginBottom(1).
		Render(fmt.Sprintf("📋 Flow Plans Across All Workspaces (%d total)", len(allPlans)))
	
	// Add a legend below the table
	legendStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("243")).
		MarginTop(1)
	
	legend := legendStyle.Render(
		"Legend: ✓ Completed  ⚡ Running  ✗ Failed  🚫 Blocked  👁 Review  ⏳ Pending  🤖 LLM  🚧 Mixed  ● Recent (< 1hr)")
	
	fmt.Println(tableTitle)
	fmt.Println(t)
	fmt.Println(legend)
	return nil
}
