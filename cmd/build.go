package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/logging"
	grovelogging "github.com/grovetools/core/logging"
	"github.com/grovetools/core/tui/theme"
	"github.com/spf13/cobra"

	orch "github.com/grovetools/grove/pkg/orchestrator"
)

var buildUlog = grovelogging.NewUnifiedLogger("grove-meta.build")

// Package-level flag state shared between task_runner.go and build TUI.
var (
	buildJobs        int
	buildFailFast    bool
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

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return executeTask(cmd, "build", orch.StrategyWaveSorted)
	}
	cmd.SilenceUsage = true

	addTaskFlags(cmd)

	return cmd
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
	verb          string
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
	skipCount     int
	runningCount  int
	eventsChan    <-chan orch.TaskEvent
	jobIndexMap   map[string]int
	viewport      viewport.Model
}

type buildsStartedMsg struct {
	eventsChan  <-chan orch.TaskEvent
	jobIndexMap map[string]int
}

func runTuiBuild(o *orch.Orchestrator, verb string, jobs []orch.TaskJob) error {
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
		verb:         verb,
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
			label := strings.ToUpper(fm.verb[:1]) + fm.verb[1:]

			pretty.Blank()
			buildUlog.Info("Build summary separator").
				Pretty(strings.Repeat("=", 60)).
				PrettyOnly().
				Emit()
			pretty.ErrorPretty(fmt.Sprintf("%s failed: %d/%d projects failed", label, fm.failCount, len(fm.projects)), nil)
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
		return fmt.Errorf("%d %s tasks failed", fm.failCount, fm.verb)
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

				if m.successCount+m.failCount+m.skipCount == len(m.projects) {
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
				if result.Skipped {
					m.projects[index].status = "skipped"
					m.skipCount++
				} else if result.Err != nil {
					m.projects[index].status = "failed"
					m.failCount++
				} else {
					m.projects[index].status = "success"
					m.successCount++
				}
				items := m.list.Items()
				items[index] = m.projects[index]
				cmds = append(cmds, m.list.SetItems(items))

				if m.successCount+m.failCount+m.skipCount == len(m.projects) {
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

	label := strings.ToUpper(m.verb[:1]) + m.verb[1:]
	header := fmt.Sprintf("Running %s on %d projects... Running: %d, Success: %d, Skipped: %d, Failed: %d",
		m.verb, len(m.projects), m.runningCount, m.successCount, m.skipCount, m.failCount)
	if m.finished {
		if m.interactive {
			header = fmt.Sprintf("%s finished! Success: %d, Skipped: %d, Failed: %d (Press 'q' to quit, 'enter' to view logs)", label, m.successCount, m.skipCount, m.failCount)
		} else {
			header = fmt.Sprintf("%s finished! Success: %d, Skipped: %d, Failed: %d", label, m.successCount, m.skipCount, m.failCount)
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
	case "skipped":
		statusIcon = theme.DefaultTheme.Warning.Render("⊘")
		durationStr = theme.DefaultTheme.Warning.Render("(skipped)")
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
