package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/mux"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/spf13/cobra"
)

type recordFlags struct {
	pane    string
	output  string
	at      string
	detach  bool
	v2      bool
	idleCap float64
	noHist  bool
	title   string
}

func newRecordCmd() *cobra.Command {
	f := &recordFlags{}

	cmd := cli.NewStandardCommand("record", "Record a live tuimux pane to an asciicast (.cast)")
	cmd.Long = `Record a live tuimux pane to an asciicast (.cast) file, server-side.

The recording is driven by the tuimux daemon that owns the pane, so it survives
client detach/restart and needs no mirror client. The default output lands in the
current repo's notebook under docgen/asciicasts/<name>.cast, exactly where docgen
picks it up for the website.

Two-terminal workflow: stage your demo in a treemux session (terminal 1), then
run 'grove record <name>' from another terminal (terminal 2) — the scope-keyed
socket and the focused pane are resolved automatically.`
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runRecord(args[0], f)
	}

	cmd.Flags().StringVar(&f.pane, "pane", "", "Target pane by pty id or tag/label (default: sole pane, else focused pane)")
	cmd.Flags().StringVarP(&f.output, "output", "o", "", "Output dir or file.cast (default: <repo-notebook>/docgen/asciicasts/<name>.cast)")
	cmd.Flags().StringVar(&f.at, "at", "", "Scope/directory whose tuimux daemon to target (default: current scope)")
	cmd.Flags().BoolVar(&f.detach, "detach", false, "Start recording and return immediately (stop later with 'grove record stop')")
	cmd.Flags().BoolVar(&f.v2, "v2", false, "Write asciicast v2 instead of the default v3")
	cmd.Flags().Float64Var(&f.idleCap, "idle-cap", 0, "Cap idle gaps to this many seconds (0 disables)")
	cmd.Flags().BoolVar(&f.noHist, "no-history", false, "Do not seed the cast with the pane's scrollback history")
	cmd.Flags().StringVar(&f.title, "title", "", "Asciicast title stored in the cast header")

	cmd.AddCommand(newRecordStopCmd())
	return cmd
}

func newRecordStopCmd() *cobra.Command {
	var atFlag, paneFlag string
	cmd := cli.NewStandardCommand("stop", "Stop the active recording in the current scope")
	cmd.Args = cobra.NoArgs
	cmd.SilenceUsage = true
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runRecordStop(atFlag, paneFlag)
	}
	cmd.Flags().StringVar(&atFlag, "at", "", "Scope/directory whose tuimux daemon to target (default: current scope)")
	cmd.Flags().StringVar(&paneFlag, "pane", "", "Disambiguate by pty id or tag when multiple recordings are active")
	return cmd
}

func runRecord(name string, f *recordFlags) error {
	socket := mux.ResolveRecordSocket(f.at)
	client := mux.NewRecordClient(socket)
	if err := client.Ping(); err != nil {
		return fmt.Errorf("no tuimux daemon reachable at %s (is a session running in this scope?): %w", socket, err)
	}

	target, err := selectRecordTarget(client, f.pane)
	if err != nil {
		return err
	}

	absOut, underAsciicasts, err := resolveRecordOutput(name, f.output, f.at)
	if err != nil {
		return err
	}

	req := mux.RecordStartRequest{Path: absOut, Title: f.title}
	if f.noHist {
		no := false
		req.IncludeHistory = &no
	}
	if f.v2 {
		req.Version = 2
	} else {
		req.Version = 3
	}
	if f.idleCap > 0 {
		req.IdleCap = f.idleCap
	}

	if err := client.StartRecording(target.ID, req); err != nil {
		return mapRecordStartErr(err, target)
	}

	fmt.Printf("Recording pane %s → %s\n", describePane(target), absOut)

	if f.detach {
		fmt.Print("Detached. Stop with: grove record stop")
		if f.at != "" {
			fmt.Printf(" --at %s", f.at)
		}
		fmt.Println()
		return nil
	}

	fmt.Println("Recording… press Enter to stop.")
	waitForStopSignal()

	info, err := client.StopRecording(target.ID)
	if err != nil {
		return mapRecordStopErr(err)
	}
	printStopSummary(info, underAsciicasts)
	return nil
}

func runRecordStop(atFlag, paneFlag string) error {
	socket := mux.ResolveRecordSocket(atFlag)
	client := mux.NewRecordClient(socket)
	if err := client.Ping(); err != nil {
		return fmt.Errorf("no tuimux daemon reachable at %s: %w", socket, err)
	}

	ptys, err := client.ListPTYs()
	if err != nil {
		return err
	}
	var recording []mux.PtyInfo
	for _, p := range ptys {
		if p.Recording {
			if paneFlag != "" && !paneMatches(p, paneFlag) {
				continue
			}
			recording = append(recording, p)
		}
	}
	if len(recording) == 0 {
		if paneFlag != "" {
			return fmt.Errorf("no active recording matches %q in this scope", paneFlag)
		}
		return errors.New("no active recording in this scope")
	}
	if len(recording) > 1 {
		return fmt.Errorf("multiple active recordings; disambiguate with --pane <id|tag>:\n%s", formatPaneList(recording))
	}

	target := recording[0]
	info, err := client.StopRecording(target.ID)
	if err != nil {
		return mapRecordStopErr(err)
	}
	printStopSummary(info, isUnderDocgenAsciicasts(info.Path))
	return nil
}

// selectRecordTarget resolves which PTY to record. An explicit --pane matches by
// pty id or tag/label. Otherwise a sole live pane is used; failing that the
// focused pane is resolved best-effort; failing that the caller is asked to pick.
func selectRecordTarget(client *mux.RecordClient, pane string) (*mux.PtyInfo, error) {
	ptys, err := client.ListPTYs()
	if err != nil {
		return nil, err
	}
	if len(ptys) == 0 {
		return nil, errors.New("no live panes in this scope")
	}

	if pane != "" {
		var matches []mux.PtyInfo
		for _, p := range ptys {
			if paneMatches(p, pane) {
				matches = append(matches, p)
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no pane matches %q:\n%s", pane, formatPaneList(ptys))
		}
		if len(matches) > 1 {
			return nil, fmt.Errorf("%q matches %d panes; use a pty id:\n%s", pane, len(matches), formatPaneList(matches))
		}
		return &matches[0], nil
	}

	if len(ptys) == 1 {
		return &ptys[0], nil
	}

	if focused, ferr := client.FocusedPTY(); ferr == nil && focused != nil {
		return focused, nil
	}

	return nil, fmt.Errorf("%d panes in this scope; pick one with --pane <id|tag>:\n%s", len(ptys), formatPaneList(ptys))
}

// paneMatches reports whether selector identifies pane p — by pty id, by label,
// or by any tag key or value (including key=value form).
func paneMatches(p mux.PtyInfo, selector string) bool {
	if p.ID == selector || p.Label == selector {
		return true
	}
	for k, v := range p.Tags {
		if k == selector || v == selector || k+"="+v == selector {
			return true
		}
	}
	return false
}

func describePane(p *mux.PtyInfo) string {
	parts := []string{p.ID}
	if label := paneLabel(*p); label != "" {
		parts = append(parts, label)
	}
	if p.ForegroundProcess != "" {
		parts = append(parts, p.ForegroundProcess)
	}
	return strings.Join(parts, " · ")
}

func paneLabel(p mux.PtyInfo) string {
	if p.Label != "" {
		return p.Label
	}
	return p.Tags["label"]
}

func formatPaneList(ptys []mux.PtyInfo) string {
	var b strings.Builder
	for _, p := range ptys {
		tags := formatTags(p.Tags)
		fmt.Fprintf(&b, "  %-14s  %-24s  %s\n", p.ID, tags, p.ForegroundProcess)
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatTags(tags map[string]string) string {
	if len(tags) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(tags))
	for k, v := range tags {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

// resolveRecordOutput computes the absolute .cast destination and whether it
// lands under a docgen/**/asciicasts directory (which gates the website snippet).
func resolveRecordOutput(name, output, atDir string) (absPath string, underAsciicasts bool, err error) {
	filename := name
	if !strings.HasSuffix(filename, ".cast") {
		filename += ".cast"
	}

	var path string
	switch {
	case output == "":
		dir, derr := defaultAsciicastsDir(atDir)
		if derr != nil {
			return "", false, derr
		}
		path = filepath.Join(dir, filename)
	case strings.HasSuffix(output, ".cast"):
		path = output
	default:
		path = filepath.Join(output, filename)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false, err
	}
	return abs, isUnderDocgenAsciicasts(abs), nil
}

// defaultAsciicastsDir resolves <repo-notebook>/docgen/asciicasts for the
// workspace containing atDir (or the cwd), via the core NotebookLocator API.
func defaultAsciicastsDir(atDir string) (string, error) {
	dir := atDir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	}
	node, err := workspace.GetProjectByPath(dir)
	if err != nil || node == nil {
		return "", fmt.Errorf("could not resolve workspace for %s: %w", dir, err)
	}
	cfg, err := config.LoadDefault()
	if err != nil {
		return "", fmt.Errorf("load core config: %w", err)
	}
	locator := workspace.NewNotebookLocator(cfg)
	docgenDir, err := locator.GetDocgenDir(node)
	if err != nil {
		return "", fmt.Errorf("resolve docgen dir: %w", err)
	}
	return filepath.Join(docgenDir, "asciicasts"), nil
}

// isUnderDocgenAsciicasts reports whether abs sits in an "asciicasts" directory
// that has a "docgen" ancestor — i.e. a docgen/**/asciicasts destination.
func isUnderDocgenAsciicasts(abs string) bool {
	parts := strings.Split(filepath.ToSlash(abs), "/")
	sawDocgen := false
	for i, part := range parts {
		if part == "docgen" {
			sawDocgen = true
		}
		if part == "asciicasts" && i > 0 && sawDocgen {
			return true
		}
	}
	return false
}

func printStopSummary(info mux.RecordStopResult, underAsciicasts bool) {
	fmt.Printf("Stopped. duration=%.2fs events=%d bytes=%d\n", info.Duration, info.Events, info.Bytes)
	fmt.Printf("Saved: %s\n", info.Path)
	if underAsciicasts {
		fmt.Println("\nWebsite snippet:")
		fmt.Println(websiteSnippet(filepath.Base(info.Path)))
	}
}

// websiteSnippet renders the fenced ```asciinema block the website content
// consumes, referencing the cast by its ./asciicasts/<file> relative form.
func websiteSnippet(castFile string) string {
	return fmt.Sprintf("```asciinema\n{ \"src\": \"./asciicasts/%s\", \"autoPlay\": true, \"loop\": true }\n```", castFile)
}

func mapRecordStartErr(err error, target *mux.PtyInfo) error {
	switch {
	case errors.Is(err, mux.ErrPtyNotFound):
		return fmt.Errorf("pane %s no longer exists", target.ID)
	case errors.Is(err, mux.ErrAlreadyRecording):
		return fmt.Errorf("pane %s is already recording (stop it with 'grove record stop')", target.ID)
	default:
		return fmt.Errorf("start recording: %w", err)
	}
}

func mapRecordStopErr(err error) error {
	switch {
	case errors.Is(err, mux.ErrPtyNotFound):
		return errors.New("pane no longer exists")
	case errors.Is(err, mux.ErrNotRecording):
		return errors.New("pane is not recording")
	default:
		return fmt.Errorf("stop recording: %w", err)
	}
}

// waitForStopSignal blocks until the user presses Enter or sends SIGINT/SIGTERM.
func waitForStopSignal() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	enterCh := make(chan struct{}, 1)
	go func() {
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		enterCh <- struct{}{}
	}()

	select {
	case <-enterCh:
	case <-sigCh:
		fmt.Println()
	}
}
