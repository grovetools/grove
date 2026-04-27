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
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/logging"
	grovelogging "github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/tui/theme"
	"github.com/spf13/cobra"

	orch "github.com/grovetools/grove/pkg/orchestrator"

	"github.com/grovetools/grove/pkg/discovery"
)

var buildUlog = grovelogging.NewUnifiedLogger("grove-meta.build")

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

	projects, _, err := DiscoverTargetProjects()
	if err != nil {
		return fmt.Errorf("failed to discover projects: %w", err)
	}

	var workspaces []string
	for _, p := range projects {
		workspaces = append(workspaces, p.Path)
	}

	if buildFilter != "" {
		workspaces = discovery.FilterWorkspaces(workspaces, buildFilter)
	}
	if buildExclude != "" {
		workspaces = applyExcludeFilter(workspaces, buildExclude)
	}

	if len(workspaces) == 0 {
		buildUlog.Info("No projects to build").
			Field("filter", buildFilter).
			Field("exclude", buildExclude).
			Pretty("No projects to build after filtering.").
			Emit()
		return nil
	}

	var jobs []orch.TaskJob
	configMap := make(map[string]*config.Config)

	for _, wsPath := range workspaces {
		cfg, loadErr := config.LoadFrom(wsPath)
		name := filepath.Base(wsPath)
		buildCmd := orch.ResolveCommand(cfg, "build")

		jobs = append(jobs, orch.TaskJob{
			Name:    name,
			Path:    wsPath,
			Command: buildCmd,
		})

		if loadErr == nil {
			configMap[name] = cfg
		}
	}

	waves := orch.SortIntoWaves(jobs, configMap)
	hasWaves := len(waves) > 1

	var binDirs []string
	for _, wsPath := range workspaces {
		binDirs = append(binDirs, filepath.Join(wsPath, "bin"))
	}
	runOpts := &orch.RunOptions{ExtraPathDirs: binDirs}

	// Initialize daemon client and state provider
	client := daemon.New()
	var stateProvider orch.StateProvider
	if client.IsRunning() {
		stateProvider = &orch.DaemonStateProvider{Client: client}
	} else {
		stateProvider = &orch.LocalStateProvider{}
	}

	o := &orch.Orchestrator{
		Options: orch.OrchestratorOptions{
			Verb:     "build",
			Strategy: orch.StrategyWaveSorted,
			Jobs:     buildJobs,
		},
		RunOpts:       runOpts,
		StateProvider: stateProvider,
		DaemonClient:  client,
		Configs:       configMap,
	}

	if buildDryRun {
		return runDryRun(opts, jobs, waves, configMap, hasWaves)
	}

	if opts.JSONOutput {
		return runJSONBuildWaves(o, waves)
	}

	if buildVerbose || hasWaves {
		if hasWaves && !buildVerbose {
			buildUlog.Info("Using verbose mode for wave-based build").
				Pretty("Building in waves due to build_after dependencies...").
				Emit()
		}
		return runVerboseBuildWaves(o, waves)
	}

	return runTuiBuild(o, jobs)
}

func runDryRun(opts cli.CommandOptions, jobs []orch.TaskJob, waves [][]orch.TaskJob, configMap map[string]*config.Config, hasWaves bool) error {
	if opts.JSONOutput {
		result := map[string]interface{}{
			"mode":  "dry-run",
			"waves": len(waves),
			"total": len(jobs),
		}
		if hasWaves {
			waveData := make([][]string, len(waves))
			for i, wave := range waves {
				for _, job := range wave {
					waveData[i] = append(waveData[i], job.Name)
				}
			}
			result["build_order"] = waveData
		} else {
			var names []string
			for _, job := range jobs {
				names = append(names, job.Name)
			}
			result["projects"] = names
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	buildUlog.Info("Dry run - projects to build").
		Field("total", len(jobs)).
		Field("waves", len(waves)).
		Pretty("Projects that would be built:").
		Emit()

	if hasWaves {
		for i, wave := range waves {
			buildUlog.Info("Build wave").
				Field("wave", i+1).
				Field("count", len(wave)).
				Pretty(fmt.Sprintf("\nWave %d:", i+1)).
				Emit()
			for _, job := range wave {
				deps := ""
				if cfg, ok := configMap[job.Name]; ok && len(cfg.BuildAfter) > 0 {
					deps = fmt.Sprintf(" (after: %s)", strings.Join(cfg.BuildAfter, ", "))
				}
				buildUlog.Info("Build job").
					Field("name", job.Name).
					Field("path", job.Path).
					Pretty(fmt.Sprintf("  - %s%s", job.Name, deps)).
					Emit()
			}
		}
	} else {
		for i, job := range jobs {
			buildUlog.Info("Build job").
				Field("index", i+1).
				Field("name", job.Name).
				Field("path", job.Path).
				Pretty(fmt.Sprintf("  %d. %s (%s)", i+1, job.Name, job.Path)).
				Emit()
		}
	}
	buildUlog.Info("Dry run summary").
		Field("total", len(jobs)).
		Field("waves", len(waves)).
		Pretty(fmt.Sprintf("\nTotal: %d projects in %d wave(s)", len(jobs), len(waves))).
		Emit()
	return nil
}

func runJSONBuildWaves(o *orch.Orchestrator, waves [][]orch.TaskJob) error {
	type JSONResult struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		Wave     int    `json:"wave"`
		Success  bool   `json:"success"`
		Duration string `json:"duration"`
		Error    string `json:"error,omitempty"`
		Output   string `json:"output,omitempty"`
		Cached   bool   `json:"cached,omitempty"`
	}

	var results []JSONResult
	var successCount, failCount int

	for waveIdx, waveJobs := range waves {
		ctx := context.Background()
		events := o.RunWithEvents(ctx, waveJobs)

		for event := range events {
			if event.Type != "finish" && event.Type != "cached" {
				continue
			}
			if event.Result == nil {
				continue
			}
			r := event.Result
			jr := JSONResult{
				Name:     r.Job.Name,
				Path:     r.Job.Path,
				Wave:     waveIdx + 1,
				Duration: r.Duration.Round(time.Millisecond).String(),
				Cached:   r.Cached,
			}

			if r.Err != nil {
				failCount++
				jr.Success = false
				jr.Error = r.Err.Error()
				jr.Output = string(r.Output)

				if buildFailFast {
					results = append(results, jr)
					return outputJSONResults(results, successCount, failCount, len(waves))
				}
			} else {
				successCount++
				jr.Success = true
			}
			results = append(results, jr)
		}
	}

	return outputJSONResults(results, successCount, failCount, len(waves))
}

func outputJSONResults[T any](results []T, successCount, failCount, totalWaves int) error {
	output := map[string]interface{}{
		"mode":    "build",
		"jobs":    buildJobs,
		"waves":   totalWaves,
		"results": results,
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

func runVerboseBuildWaves(o *orch.Orchestrator, waves [][]orch.TaskJob) error {
	var successCount, failCount int
	totalJobs := 0
	for _, wave := range waves {
		totalJobs += len(wave)
	}

	pretty := logging.NewPrettyLogger()
	pretty.Progress(fmt.Sprintf("Building %d projects in %d wave(s) (using %d workers)", totalJobs, len(waves), buildJobs))
	pretty.Blank()

	completedJobs := 0
	for waveIdx, waveJobs := range waves {
		if len(waves) > 1 {
			pretty.Progress(fmt.Sprintf("Wave %d/%d (%d projects)", waveIdx+1, len(waves), len(waveJobs)))
		}

		ctx := context.Background()
		events := o.RunWithEvents(ctx, waveJobs)

		for event := range events {
			switch event.Type {
			case "cached":
				if event.Result != nil {
					completedJobs++
					successCount++
					progress := fmt.Sprintf("[%d/%d]", completedJobs, totalJobs)
					buildUlog.Progress("Cached project").
						Field("name", event.Result.Job.Name).
						Field("completed", completedJobs).
						Field("total", totalJobs).
						Pretty(fmt.Sprintf("\n%s %s (cached)", progress, event.Result.Job.Name)).
						Emit()
				}
			case "finish":
				if event.Result == nil {
					continue
				}
				result := event.Result
				completedJobs++
				progress := fmt.Sprintf("[%d/%d]", completedJobs, totalJobs)

				buildUlog.Progress("Building project").
					Field("name", result.Job.Name).
					Field("completed", completedJobs).
					Field("total", totalJobs).
					Pretty(fmt.Sprintf("\n%s Building %s...", progress, result.Job.Name)).
					Emit()
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
					if buildFailFast {
						pretty.Blank()
						pretty.Divider()
						pretty.InfoPretty(fmt.Sprintf("Build stopped (fail-fast). Success: %d, Failed: %d", successCount, failCount))
						return fmt.Errorf("%d builds failed", failCount)
					}
				} else {
					successCount++
					pretty.Status("success", fmt.Sprintf("Success (%v)", result.Duration.Round(time.Millisecond)))
				}
			}
		}
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
	status   string // "pending", "running", "success", "failed", "cached"
	output   string
	duration time.Duration
}

func (p projectStatus) Title() string       { return p.name }
func (p projectStatus) Description() string { return p.status }
func (p projectStatus) FilterValue() string { return p.name }

type tuiModel struct {
	projects      []projectStatus
	orchestrator  *orch.Orchestrator
	jobs          []orch.TaskJob
	list          list.Model
	spinner       spinner.Model
	logViewport   viewport.Model
	logLines      []string
	maxLogLines   int
	viewMode      string // "list" or "logs"
	width, height int
	finished      bool
	interactive   bool
	successCount  int
	failCount     int
	runningCount  int
	eventsChan    <-chan orch.TaskEvent
	jobIndexMap   map[string]int
	viewport      viewport.Model
}

type buildsStartedMsg struct {
	eventsChan  <-chan orch.TaskEvent
	jobIndexMap map[string]int
}

func runTuiBuild(o *orch.Orchestrator, jobs []orch.TaskJob) error {
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
		projects:     projects,
		orchestrator: o,
		jobs:         jobs,
		list:         l,
		spinner:      s,
		logViewport:  logViewport,
		maxLogLines:  200,
		viewMode:     "list",
		interactive:  buildInteractive,
	}

	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return err
	}

	if fm, ok := finalModel.(tuiModel); ok && fm.failCount > 0 {
		if !buildInteractive {
			pretty := logging.NewPrettyLogger()

			pretty.Blank()
			buildUlog.Info("Build summary separator").
				Pretty(strings.Repeat("=", 60)).
				PrettyOnly().
				Emit()
			pretty.ErrorPretty(fmt.Sprintf("Build failed: %d/%d projects failed", fm.failCount, len(fm.projects)), nil)
			buildUlog.Info("Build summary separator").
				Pretty(strings.Repeat("=", 60)).
				PrettyOnly().
				Emit()

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
		eventsChan := m.orchestrator.RunWithEvents(ctx, m.jobs)

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

		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 2

		if len(m.projects) == 1 {
			listHeight := 2
			m.list.SetSize(msg.Width, listHeight)

			logHeight := 6
			m.logViewport.Width = msg.Width
			m.logViewport.Height = logHeight
		} else {
			listHeight := len(m.projects) + 1
			if listHeight < 3 {
				listHeight = 3
			}
			if listHeight > msg.Height-headerHeight-bottomPadding {
				listHeight = msg.Height - headerHeight - bottomPadding
			}

			listWidth := msg.Width / 2
			logWidth := msg.Width - listWidth

			m.list.SetSize(listWidth, listHeight)

			m.logViewport.Width = logWidth - 2
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
		return m, m.waitForBuildEventCmd()

	case orch.TaskEvent:
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

		case "cached":
			if index, ok := m.jobIndexMap[msg.Job.Name]; ok {
				m.projects[index].status = "cached"
				m.successCount++
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
				wsStyle := getWorkspaceStyle(msg.Job.Name)
				line := fmt.Sprintf("%s %s", wsStyle.Render(fmt.Sprintf("[%s]", msg.Job.Name)), msg.OutputLine)
				m.logLines = append(m.logLines, line)
				if len(m.logLines) > m.maxLogLines {
					m.logLines = m.logLines[len(m.logLines)-m.maxLogLines:]
				}
				m.logViewport.SetContent(strings.Join(m.logLines, "\n"))
				m.logViewport.GotoBottom()
			}
		}

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

	if len(m.projects) == 1 {
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
	case "cached":
		statusIcon = theme.DefaultTheme.Success.Render(theme.IconSuccess)
		durationStr = theme.DefaultTheme.Muted.Render("(cached)")
	case "failed":
		statusIcon = theme.DefaultTheme.Error.Render(theme.IconError)
		durationStr = theme.DefaultTheme.Muted.Render(fmt.Sprintf("(%v)", p.duration.Round(time.Millisecond)))
	}

	line := fmt.Sprintf("%s %s %s", statusIcon, p.name, durationStr)
	if d.totalProjects > 1 {
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
