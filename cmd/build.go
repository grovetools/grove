package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-core/cli"
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
		jobs = append(jobs, build.BuildJob{
			Name: filepath.Base(wsPath),
			Path: wsPath,
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
	viewport      viewport.Model
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
}

type buildStartedMsg struct {
	index int
}

type buildFinishedMsg struct {
	index  int
	result build.BuildResult
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

	m := tuiModel{
		projects:    projects,
		jobs:        jobs,
		list:        l,
		spinner:     s,
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
			pretty.Divider()
			pretty.ErrorPretty(fmt.Sprintf("Build failed: %d/%d projects failed", fm.failCount, len(fm.projects)), nil)
			pretty.Divider()

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

		if index, ok := m.jobIndexMap[event.Job.Name]; ok {
			if event.Type == "start" {
				return buildStartedMsg{index: index}
			} else if event.Type == "finish" && event.Result != nil {
				return buildFinishedMsg{index: index, result: *event.Result}
			}
		}
		return nil
	}
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Only use the height needed for projects + header (title + status line)
		// Each project takes 1 line, plus 4 lines for list chrome (title, etc)
		neededHeight := len(m.projects) + 4
		listHeight := neededHeight
		if listHeight > msg.Height-2 {
			listHeight = msg.Height - 2
		}
		m.list.SetSize(msg.Width, listHeight)
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 2
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

	case buildStartedMsg:
		m.projects[msg.index].status = "running"
		m.runningCount++
		items := m.list.Items()
		items[msg.index] = m.projects[msg.index]

		// Continue listening for events
		return m, tea.Batch(m.list.SetItems(items), m.waitForBuildEventCmd())

	case buildFinishedMsg:
		m.projects[msg.index].duration = msg.result.Duration
		m.projects[msg.index].output = string(msg.result.Output)
		m.runningCount--
		if msg.result.Err != nil {
			m.projects[msg.index].status = "failed"
			m.failCount++
		} else {
			m.projects[msg.index].status = "success"
			m.successCount++
		}
		items := m.list.Items()
		items[msg.index] = m.projects[msg.index]

		if m.successCount+m.failCount == len(m.projects) {
			m.finished = true
			// Auto-quit unless in interactive mode
			if !m.interactive {
				return m, tea.Quit
			}
		}

		// Continue listening if not finished
		var nextCmd tea.Cmd
		if !m.finished {
			nextCmd = m.waitForBuildEventCmd()
		}

		return m, tea.Batch(m.list.SetItems(items), nextCmd)

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
	m.list.SetDelegate(projectDelegate{spinner: m.spinner, totalProjects: len(m.projects)})

	header := fmt.Sprintf("Building %d projects... Running: %d, Success: %d, Failed: %d",
		len(m.projects), m.runningCount, m.successCount, m.failCount)
	if m.finished {
		header = fmt.Sprintf("Build finished! Success: %d, Failed: %d (Press q to quit)", m.successCount, m.failCount)
	}

	return fmt.Sprintf("  %s\n%s", header, m.list.View())
}

type projectDelegate struct {
	spinner       spinner.Model
	totalProjects int
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
		if index == m.Index() {
			line = "  " + theme.IconArrowRightBold + " " + line
		} else {
			line = "    " + line
		}
	} else {
		line = "  " + line
	}
	fmt.Fprint(w, line)
}