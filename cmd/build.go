package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/config"
	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/mattsolo1/grove-meta/pkg/build"
	"github.com/mattsolo1/grove-meta/pkg/discovery"
	"github.com/spf13/cobra"
)

var (
	buildVerbose     bool
	buildJobs        int
	buildFilter      string
	buildExclude     string
	buildFailFast    bool
	buildDryRun      bool
	buildInteractive bool
)

// Workspace color management - uses theme's AccentColors palette
var (
	workspaceColorMap   = make(map[string]lipgloss.Style)
	workspaceColorIndex = 0
)

func getWorkspaceStyle(workspace string) lipgloss.Style {
	if style, ok := workspaceColorMap[workspace]; ok {
		return style
	}

	color := theme.DefaultTheme.AccentColors[workspaceColorIndex%len(theme.DefaultTheme.AccentColors)]
	style := lipgloss.NewStyle().Foreground(color).Bold(true)
	workspaceColorMap[workspace] = style
	workspaceColorIndex++

	return style
}

func newBuildCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("build", "Build all Grove packages in parallel")
	cmd.Long = `Builds Grove packages in parallel with a real-time status UI.

The build scope is context-aware based on your current directory:
- **Ecosystem root:** Builds all sub-projects within the ecosystem.
- **Sub-project or Standalone project:** Builds only the current project.

By default, all builds continue even if one fails. Use --fail-fast for CI environments
where you want to stop immediately on the first failure.

This command replaces the root 'make build' for a faster and more informative build experience.`

	cmd.RunE = runBuild
	cmd.SilenceUsage = true

	cmd.Flags().BoolVarP(&buildVerbose, "verbose", "v", false, "Stream raw build output instead of using the TUI")
	cmd.Flags().IntVarP(&buildJobs, "jobs", "j", runtime.NumCPU(), "Number of parallel builds")
	cmd.Flags().StringVar(&buildFilter, "filter", "", "Glob pattern to include only matching projects")
	cmd.Flags().StringVar(&buildExclude, "exclude", "", "Comma-separated glob patterns to exclude projects")
	cmd.Flags().BoolVar(&buildFailFast, "fail-fast", false, "Stop all builds immediately when one fails (useful for CI)")
	cmd.Flags().BoolVar(&buildDryRun, "dry-run", false, "Show what would be built without actually building")
	cmd.Flags().BoolVarP(&buildInteractive, "interactive", "i", false, "Keep TUI open after builds complete for inspection")

	return cmd
}

func runBuild(cmd *cobra.Command, args []string) error {
	opts := cli.GetOptions(cmd)

	// Discover projects using context-aware discovery
	projects, _, err := DiscoverTargetProjects()
	if err != nil {
		return fmt.Errorf("failed to discover projects: %w", err)
	}

	var workspaces []string
	for _, p := range projects {
		workspaces = append(workspaces, p.Path)
	}

	// Apply filters
	if buildFilter != "" {
		workspaces = discovery.FilterWorkspaces(workspaces, buildFilter)
	}
	if buildExclude != "" {
		workspaces = applyExcludeFilter(workspaces, buildExclude)
	}

	if len(workspaces) == 0 {
		fmt.Println("No projects to build after filtering.")
		return nil
	}

	// Create build jobs
	var jobs []build.BuildJob
	for _, wsPath := range workspaces {
		cfg, err := config.LoadFrom(wsPath)
		var buildCmd []string
		if err == nil && cfg.BuildCmd != "" {
			buildCmd = strings.Fields(cfg.BuildCmd)
		} else {
			buildCmd = []string{"make", "build"}
		}

		jobs = append(jobs, build.BuildJob{
			Name:    filepath.Base(wsPath),
			Path:    wsPath,
			Command: buildCmd,
		})
	}

	// Handle dry-run mode
	if buildDryRun {
		if opts.JSONOutput {
			result := map[string]interface{}{
				"mode": "dry-run",
				"projects": jobs,
				"total": len(jobs),
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		}

		fmt.Println("Projects that would be built:")
		for i, job := range jobs {
			fmt.Printf("  %d. %s (%s)\n", i+1, job.Name, job.Path)
		}
		fmt.Printf("\nTotal: %d projects\n", len(jobs))
		return nil
	}

	if opts.JSONOutput {
		return runJSONBuild(jobs)
	}

	if buildVerbose {
		return runVerboseBuild(jobs)
	}

	return runTuiBuild(jobs)
}

func runJSONBuild(jobs []build.BuildJob) error {
	ctx := context.Background()
	continueOnError := !buildFailFast
	resultsChan := build.Run(ctx, jobs, buildJobs, continueOnError)

	type BuildResult struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		Success  bool   `json:"success"`
		Duration string `json:"duration"`
		Error    string `json:"error,omitempty"`
		Output   string `json:"output,omitempty"`
	}

	results := make([]BuildResult, 0, len(jobs))
	var successCount, failCount int

	for result := range resultsChan {
		br := BuildResult{
			Name:     result.Job.Name,
			Path:     result.Job.Path,
			Duration: result.Duration.Round(time.Millisecond).String(),
		}

		if result.Err != nil {
			failCount++
			br.Success = false
			br.Error = result.Err.Error()
			// Always include output for failed builds
			br.Output = string(result.Output)
		} else {
			successCount++
			br.Success = true
		}
		results = append(results, br)
	}

	output := map[string]interface{}{
		"mode":      "build",
		"jobs":      buildJobs,
		"results":   results,
		"summary": map[string]int{
			"total":   len(results),
			"success": successCount,
			"failed":  failCount,
		},
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(output); err != nil {
		return err
	}

	if failCount > 0 {
		return fmt.Errorf("%d builds failed", failCount)
	}
	return nil
}

func runVerboseBuild(jobs []build.BuildJob) error {
	ctx := context.Background()
	continueOnError := !buildFailFast
	resultsChan := build.Run(ctx, jobs, buildJobs, continueOnError)

	var successCount, failCount int
	var printMutex sync.Mutex
	totalJobs := len(jobs)
	pretty := logging.NewPrettyLogger()

	pretty.Progress(fmt.Sprintf("Starting parallel build of %d projects (using %d workers)", totalJobs, buildJobs))
	pretty.Blank()

	for result := range resultsChan {
		printMutex.Lock()
		completed := successCount + failCount + 1
		progress := fmt.Sprintf("[%d/%d]", completed, totalJobs)

		fmt.Printf("\n%s Building %s...\n", progress, result.Job.Name)
		pretty.Divider()

		if len(result.Output) > 0 {
			os.Stdout.Write(result.Output)
		}

		if result.Err != nil {
			failCount++
			pretty.Status("error", fmt.Sprintf("Failed (%v)", result.Duration.Round(time.Millisecond)))
			if result.Err.Error() != "exit status 1" && result.Err.Error() != "exit status 2" {
				pretty.ErrorPretty("Error", result.Err)
			}
		} else {
			successCount++
			pretty.Status("success", fmt.Sprintf("Success (%v)", result.Duration.Round(time.Millisecond)))
		}
		printMutex.Unlock()
	}

	pretty.Blank()
	pretty.Divider()
	pretty.InfoPretty(fmt.Sprintf("Build finished. Success: %d, Failed: %d", successCount, failCount))
	if failCount > 0 {
		return fmt.Errorf("%d builds failed", failCount)
	}
	return nil
}


// TUI types and functions

type projectStatus struct {
	name     string
	status   string // "pending", "running", "success", "failed"
	output   string
	duration time.Duration
}

func (p projectStatus) Title() string       { return p.name }
func (p projectStatus) Description() string { return p.status }
func (p projectStatus) FilterValue() string { return p.name }

type tuiModel struct {
	projects      []projectStatus
	jobs          []build.BuildJob
	list          list.Model
	spinner       spinner.Model
	logViewport   viewport.Model
	logLines      []string
	maxLogLines   int
	viewMode      string // "list" or "logs"
	width, height int
	err           error
	finished      bool
	interactive   bool
	successCount  int
	failCount     int
	runningCount  int
	eventsChan    <-chan build.BuildEvent
	jobIndexMap   map[string]int
	viewport      viewport.Model // For full-screen log inspection
}

type buildsStartedMsg struct {
	eventsChan  <-chan build.BuildEvent
	jobIndexMap map[string]int
}

func runTuiBuild(jobs []build.BuildJob) error {
	var projects []projectStatus
	for _, job := range jobs {
		projects = append(projects, projectStatus{name: job.Name, status: "pending"})
	}

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = theme.DefaultTheme.Highlight

	items := make([]list.Item, len(projects))
	for i, p := range projects {
		items[i] = p
	}

	l := list.New(items, list.NewDefaultDelegate(), 0, len(projects)+4)
	l.Title = ""
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetShowHelp(false)

	logViewport := viewport.New(0, 0)
	logViewport.SetContent("Waiting for build output...")

	m := tuiModel{
		projects:    projects,
		jobs:        jobs,
		list:        l,
		spinner:     s,
		logViewport: logViewport,
		maxLogLines: 200, // Keep last 200 lines
		viewMode:    "list",
		interactive: buildInteractive,
	}

	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return err
	}

	// After TUI exits, check for errors and print failures if not in interactive mode
	if fm, ok := finalModel.(tuiModel); ok && fm.failCount > 0 {
		if !buildInteractive {
			// Print failure details using pretty logging
			pretty := logging.NewPrettyLogger()

			// Print summary with visual distinction using equals dividers
			pretty.Blank()
			fmt.Println(strings.Repeat("=", 60))
			pretty.ErrorPretty(fmt.Sprintf("Build failed: %d/%d projects failed", fm.failCount, len(fm.projects)), nil)
			fmt.Println(strings.Repeat("=", 60))

			// For single project builds, skip showing output again (already shown in streaming logs)
			if len(fm.projects) > 1 {
				for _, p := range fm.projects {
					if p.status == "failed" {
						pretty.Blank()
						pretty.Status("error", fmt.Sprintf("%s (failed in %v)", p.name, p.duration.Round(time.Millisecond)))
						pretty.Divider()
						if len(p.output) > 0 {
							pretty.Code(p.output)
						}
					}
				}
			}
		}
		return fmt.Errorf("%d builds failed", fm.failCount)
	}

	return nil
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.startBuildsCmd())
}

func (m tuiModel) startBuildsCmd() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		continueOnError := !buildFailFast
		eventsChan := build.RunWithEvents(ctx, m.jobs, buildJobs, continueOnError)

		jobIndexMap := make(map[string]int)
		for i, job := range m.jobs {
			jobIndexMap[job.Name] = i
		}

		return buildsStartedMsg{
			eventsChan:  eventsChan,
			jobIndexMap: jobIndexMap,
		}
	}
}

func (m tuiModel) waitForBuildEventCmd() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.eventsChan
		if !ok {
			// Channel closed, all builds done
			return nil
		}
		return event
	}
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		headerHeight := 1
		bottomPadding := 3

		// for full-screen log inspection
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 2

		// Single project: vertical layout
		if len(m.projects) == 1 {
			// List takes minimal space (just the one line + chrome)
			listHeight := 2
			m.list.SetSize(msg.Width, listHeight)

			// Logs use fixed height (about 6 lines)
			logHeight := 6
			m.logViewport.Width = msg.Width
			m.logViewport.Height = logHeight
		} else {
			// Multiple projects: horizontal layout
			// Calculate list height (min 3, max available height)
			listHeight := len(m.projects) + 1
			if listHeight < 3 {
				listHeight = 3
			}
			if listHeight > msg.Height-headerHeight-bottomPadding {
				listHeight = msg.Height - headerHeight - bottomPadding
			}

			// Split width: 50% for list, 50% for logs
			listWidth := msg.Width / 2
			logWidth := msg.Width - listWidth

			m.list.SetSize(listWidth, listHeight)

			// for streaming log view - match list height
			m.logViewport.Width = logWidth - 2 // -2 for border
			m.logViewport.Height = listHeight
		}
		return m, nil

	case tea.KeyMsg:
		if m.viewMode == "logs" {
			switch msg.String() {
			case "q", "esc":
				m.viewMode = "list"
				return m, nil
			}
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			if i := m.list.SelectedItem(); i != nil {
				if p, ok := i.(projectStatus); ok && (p.status == "failed" || p.status == "success") {
					m.viewMode = "logs"
					m.viewport.SetContent(p.output)
					m.viewport.GotoTop()
				}
			}
			return m, nil
		}

	case buildsStartedMsg:
		m.eventsChan = msg.eventsChan
		m.jobIndexMap = msg.jobIndexMap
		// Start listening for events
		return m, m.waitForBuildEventCmd()

	case build.BuildEvent:
		var cmds []tea.Cmd

		switch msg.Type {
		case "start":
			if index, ok := m.jobIndexMap[msg.Job.Name]; ok {
				m.projects[index].status = "running"
				m.runningCount++
				items := m.list.Items()
				items[index] = m.projects[index]
				cmds = append(cmds, m.list.SetItems(items))
			}

		case "finish":
			if index, ok := m.jobIndexMap[msg.Job.Name]; ok {
				result := msg.Result
				m.projects[index].duration = result.Duration
				m.projects[index].output = string(result.Output)
				m.runningCount--
				if result.Err != nil {
					m.projects[index].status = "failed"
					m.failCount++
				} else {
					m.projects[index].status = "success"
					m.successCount++
				}
				items := m.list.Items()
				items[index] = m.projects[index]
				cmds = append(cmds, m.list.SetItems(items))

				if m.successCount+m.failCount == len(m.projects) {
					m.finished = true
					if !m.interactive {
						cmds = append(cmds, tea.Quit)
					}
				}
			}

		case "output":
			if _, ok := m.jobIndexMap[msg.Job.Name]; ok {
				// Find color for workspace name
				wsStyle := getWorkspaceStyle(msg.Job.Name)
				// Format line
				line := fmt.Sprintf("%s %s", wsStyle.Render(fmt.Sprintf("[%s]", msg.Job.Name)), msg.OutputLine)
				m.logLines = append(m.logLines, line)
				if len(m.logLines) > m.maxLogLines {
					m.logLines = m.logLines[len(m.logLines)-m.maxLogLines:]
				}
				m.logViewport.SetContent(strings.Join(m.logLines, "\n"))
				m.logViewport.GotoBottom()
			}
		}

		// Continue listening for events if not finished
		if !m.finished {
			cmds = append(cmds, m.waitForBuildEventCmd())
		}
		return m, tea.Batch(cmds...)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m tuiModel) View() string {
	if m.viewMode == "logs" {
		header := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("Press ESC to return to list")
		return fmt.Sprintf("%s\n%s", header, m.viewport.View())
	}

	// Update list delegate for dynamic rendering
	m.list.SetDelegate(projectDelegate{
		spinner:       m.spinner,
		totalProjects: len(m.projects),
		finished:      m.finished,
		interactive:   m.interactive,
	})

	header := fmt.Sprintf("Building %d projects... Running: %d, Success: %d, Failed: %d",
		len(m.projects), m.runningCount, m.successCount, m.failCount)
	if m.finished {
		if m.interactive {
			header = fmt.Sprintf("Build finished! Success: %d, Failed: %d (Press 'q' to quit, 'enter' to view logs)", m.successCount, m.failCount)
		} else {
			header = fmt.Sprintf("Build finished! Success: %d, Failed: %d", m.successCount, m.failCount)
		}
	}

	var mainContent string

	// For single project, show logs below; for multiple projects, show logs to the right
	if len(m.projects) == 1 {
		// Show all log lines directly without a viewport
		logStyle := lipgloss.NewStyle().
			Foreground(theme.DefaultTheme.Muted.GetForeground())

		logContent := strings.Join(m.logLines, "\n")
		if logContent == "" {
			logContent = "Waiting for build output..."
		}
		logView := logStyle.Render(logContent)

		mainContent = lipgloss.JoinVertical(lipgloss.Left,
			m.list.View(),
			"",
			logView,
		)
	} else {
		logViewStyle := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(theme.DefaultTheme.Muted.GetForeground()).
			Foreground(theme.DefaultTheme.Muted.GetForeground()).
			PaddingTop(1)

		logView := logViewStyle.Render(m.logViewport.View())

		mainContent = lipgloss.JoinHorizontal(lipgloss.Top,
			m.list.View(),
			logView,
		)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		mainContent,
		"",
		"",
		"",
	)
}

type projectDelegate struct {
	spinner       spinner.Model
	totalProjects int
	finished      bool
	interactive   bool
}

func (d projectDelegate) Height() int                               { return 1 }
func (d projectDelegate) Spacing() int                              { return 0 }
func (d projectDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d projectDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
		p := item.(projectStatus)
		var statusIcon, durationStr string

		switch p.status {
	case "pending":
		statusIcon = theme.DefaultTheme.Muted.Render("⋯")
	case "running":
		statusIcon = d.spinner.View()
	case "success":
		statusIcon = theme.DefaultTheme.Success.Render(theme.IconSuccess)
		durationStr = theme.DefaultTheme.Muted.Render(fmt.Sprintf("(%v)", p.duration.Round(time.Millisecond)))
	case "failed":
		statusIcon = theme.DefaultTheme.Error.Render(theme.IconError)
		durationStr = theme.DefaultTheme.Muted.Render(fmt.Sprintf("(%v)", p.duration.Round(time.Millisecond)))
	}

	line := fmt.Sprintf("%s %s %s", statusIcon, p.name, durationStr)
	if d.totalProjects > 1 {
		// Only show arrow if builds are finished and in interactive mode
		if d.finished && d.interactive && index == m.Index() {
			line = "  " + theme.IconArrowRightBold + " " + line
		} else {
			line = "    " + line
		}
	} else {
		line = "  " + line
	}
	fmt.Fprint(w, line)
}