package cmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/mattsolo1/grove-meta/pkg/aggregator"
	"github.com/spf13/cobra"
)

var (
	// Styles for chats display
	chatsHeaderStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("219")).
		MarginTop(1).
		MarginBottom(0)

	chatTitleStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("255"))

	chatModelStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("219")).
		Bold(true)

	chatMetaStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("245"))

	chatsBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("219")).
		Padding(0, 1).
		MarginLeft(2).
		MarginBottom(1)

	noChatsStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("242")).
		Italic(true)

	chatActiveStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("46")).
		Bold(true)
)

// Chat represents a simplified version of a flow chat
type Chat struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	Model       string    `json:"model"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	MessageCount int      `json:"message_count"`
}

var (
	chatsTableView bool
)

func NewWorkspaceChatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chats",
		Short: "Show flow chats for all workspaces",
		Long:  "Display chats (from flow chats) for each workspace in the monorepo",
		RunE:  runWorkspaceChats,
	}

	cmd.Flags().BoolVar(&chatsTableView, "table", false, "Show all chats in a single table ordered by date")

	return cmd
}

func runWorkspaceChats(cmd *cobra.Command, args []string) error {
	// Collector function to get chats for each workspace
	collector := func(workspacePath string, workspaceName string) ([]Chat, error) {
		// Run flow chat list --json
		flowCmd := exec.Command("flow", "chat", "list", "--json")
		flowCmd.Dir = workspacePath
		
		output, err := flowCmd.Output()
		if err != nil {
			// flow might not be available in this workspace
			return []Chat{}, nil
		}

		// Try to parse as JSON first
		var chats []Chat
		if err := json.Unmarshal(output, &chats); err != nil {
			// Fallback to text parsing if JSON fails
			lines := strings.Split(string(output), "\n")
			
			// Skip header line
			for i := 1; i < len(lines); i++ {
				line := strings.TrimSpace(lines[i])
				if line == "" {
					continue
				}
				
				// Parse columns: TITLE STATUS MODEL FILE
				fields := strings.Fields(line)
				if len(fields) >= 3 {
					idStr := fields[0]
					if len(idStr) > 8 {
						idStr = idStr[:8]
					}
					chat := Chat{
						ID:    idStr,
						Title: fields[0],
						Model: fields[2],
					}
					
					// Simple status parsing
					if len(fields) >= 2 {
						chat.Title = fields[0]
						// Model is at index 2 if status exists
						if fields[1] != "" && fields[1] != "." {
							chat.Model = fields[2]
						}
					}
					
					chats = append(chats, chat)
				}
			}
		}

		return chats, nil
	}

	// Renderer function to display the results
	renderer := func(results map[string][]Chat) error {
		if len(results) == 0 {
			fmt.Println("No workspaces found.")
			return nil
		}

		// Check if table view is requested
		if chatsTableView {
			return renderChatsTable(results)
		}

		// Default grouped view
		// Sort workspace names for consistent output
		var workspaceNames []string
		hasAnyChats := false
		for name, chats := range results {
			workspaceNames = append(workspaceNames, name)
			if len(chats) > 0 {
				hasAnyChats = true
			}
		}
		sortStrings(workspaceNames)

		if !hasAnyChats {
			fmt.Println(noChatsStyle.Render("No chats found in any workspace."))
			return nil
		}

		for _, wsName := range workspaceNames {
			chats := results[wsName]
			
			// Skip workspaces with no chats
			if len(chats) == 0 {
				continue
			}

			// Print workspace header
			header := chatsHeaderStyle.Render(fmt.Sprintf("💬 %s (%d chats)", wsName, len(chats)))
			fmt.Println(header)

			// Build content for this workspace
			var lines []string
			for i, chat := range chats {
				// Show only recent chats (last 5)
				if i >= 5 {
					remaining := len(chats) - 5
					lines = append(lines, chatMetaStyle.Render(fmt.Sprintf("... and %d more chats", remaining)))
					break
				}
				line := formatChat(chat)
				lines = append(lines, line)
			}

			// Join and box the content
			content := strings.Join(lines, "\n\n")
			boxed := chatsBoxStyle.Render(content)
			fmt.Println(boxed)
		}

		return nil
	}

	return aggregator.Run(collector, renderer)
}

func formatChat(chat Chat) string {
	// Format title
	title := chatTitleStyle.Render(chat.Title)
	if title == "" {
		title = chatTitleStyle.Render("(untitled chat)")
	}

	// Format model
	model := formatModel(chat.Model)

	// Format metadata
	idStr := chat.ID
	if len(idStr) > 8 {
		idStr = idStr[:8]
	}
	
	// Include date if available
	if !chat.UpdatedAt.IsZero() {
		timeAgo := formatTimeAgo(chat.UpdatedAt)
		
		// Check if recently active (within last hour)
		if time.Since(chat.UpdatedAt) < time.Hour {
			timeAgo = chatActiveStyle.Render("● " + timeAgo)
		}
		
		meta := chatMetaStyle.Render(fmt.Sprintf("%s • %s", idStr, timeAgo))
		return fmt.Sprintf("%s %s\n%s", title, model, meta)
	}
	
	meta := chatMetaStyle.Render(idStr)
	return fmt.Sprintf("%s %s\n%s", title, model, meta)
}

func formatModel(model string) string {
	// Simplify model names for display
	switch {
	case strings.Contains(model, "gpt-4"):
		return chatModelStyle.Render("[GPT-4]")
	case strings.Contains(model, "gpt-3.5"):
		return chatModelStyle.Render("[GPT-3.5]")
	case strings.Contains(model, "claude"):
		return chatModelStyle.Render("[Claude]")
	case strings.Contains(model, "gemini"):
		return chatModelStyle.Render("[Gemini]")
	default:
		return chatModelStyle.Render(fmt.Sprintf("[%s]", model))
	}
}

// renderChatsTable renders all chats in a single table sorted by date
func renderChatsTable(results map[string][]Chat) error {
	// Collect all chats with workspace info
	type chatWithWorkspace struct {
		Chat      Chat
		Workspace string
	}
	
	var allChats []chatWithWorkspace
	for wsName, chats := range results {
		for _, chat := range chats {
			allChats = append(allChats, chatWithWorkspace{
				Chat:      chat,
				Workspace: wsName,
			})
		}
	}
	
	if len(allChats) == 0 {
		fmt.Println(noChatsStyle.Render("No chats found in any workspace."))
		return nil
	}
	
	// Sort by UpdatedAt descending (most recent first)
	sort.Slice(allChats, func(i, j int) bool {
		// If one has zero time, put it at the end
		if allChats[i].Chat.UpdatedAt.IsZero() && !allChats[j].Chat.UpdatedAt.IsZero() {
			return false
		}
		if !allChats[i].Chat.UpdatedAt.IsZero() && allChats[j].Chat.UpdatedAt.IsZero() {
			return true
		}
		return allChats[i].Chat.UpdatedAt.After(allChats[j].Chat.UpdatedAt)
	})
	
	// Create table
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Headers("WORKSPACE", "TITLE", "STATUS", "MODEL", "UPDATED").
		Width(120). // Set a reasonable total width
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == 0 {
				return lipgloss.NewStyle().
					Foreground(lipgloss.Color("255")).
					Bold(true).
					Padding(0, 1)
			}
			// Add minimum widths for columns
			style := lipgloss.NewStyle().Padding(0, 1)
			switch col {
			case 2: // STATUS column
				style = style.Width(15) // Ensure status column is wide enough
			}
			return style
		})
	
	// Add rows
	for _, cw := range allChats {
		chat := cw.Chat
		
		// Format time
		timeStr := ""
		if !chat.UpdatedAt.IsZero() {
			timeStr = formatTimeAgo(chat.UpdatedAt)
			// Highlight recently active
			if time.Since(chat.UpdatedAt) < time.Hour {
				timeStr = "● " + timeStr
			}
		}
		
		// Format model
		modelStr := chat.Model
		if modelStr == "" {
			modelStr = "-"
		} else if strings.Contains(modelStr, "gemini") {
			modelStr = "Gemini"
		} else if strings.Contains(modelStr, "gpt") {
			modelStr = "GPT"
		} else if strings.Contains(modelStr, "claude") {
			modelStr = "Claude"
		}
		
		// Format status with color
		statusStr := string(chat.Status)
		switch chat.Status {
		case "pending_user":
			statusStr = lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Render("pending_user")
		case "completed":
			statusStr = lipgloss.NewStyle().Foreground(lipgloss.Color("34")).Render("completed")
		case "failed":
			statusStr = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("failed")
		case "running":
			statusStr = lipgloss.NewStyle().Foreground(lipgloss.Color("33")).Render("running")
		}
		
		t.Row(
			cw.Workspace,
			chat.Title,
			statusStr,
			modelStr,
			timeStr,
		)
	}
	
	fmt.Println(t)
	return nil
}