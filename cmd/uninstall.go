package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/grovetools/core/pkg/paths"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newUninstallCmd())
}

// uninstallTarget is a directory grove uninstall would remove.
type uninstallTarget struct {
	label  string
	path   string
	exists bool
}

// grovedProcess describes a daemon discovered via a pidfile in StateDir.
type grovedProcess struct {
	pidPath string
	pid     int
	command string // resolved process command, "" if the process is gone
	matches bool   // command looks like a groved binary
}

func newUninstallCmd() *cobra.Command {
	var (
		yes          bool
		removeConfig bool
	)

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Grove state, data, cache, and installed binaries from this machine",
		Long: `Remove Grove from this machine: state, data (including installed
binaries), and cache directories, and stop any running grove daemons
discovered via their pidfiles.

By default this is a DRY RUN: it only prints what would be removed and
which daemons would be stopped. Nothing is deleted or signaled until you
pass --yes.

The config directory is always kept unless you also pass --config.

Examples:
  grove uninstall                 # dry run: show what would happen
  grove uninstall --yes           # remove state/data/cache, stop daemons
  grove uninstall --yes --config  # additionally remove the config dir`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(yes, removeConfig)
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "Actually remove files and stop daemons (default is a dry run)")
	cmd.Flags().BoolVar(&removeConfig, "config", false, "Also remove the Grove config directory")

	return cmd
}

func runUninstall(yes, removeConfig bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	removable, configTarget := gatherUninstallTargets()

	// Sanity guard: refuse to operate on any path that resolves outside the
	// user's home directory (protects against env-var weirdness like
	// XDG_DATA_HOME=/usr or GROVE_HOME=/).
	all := append(append([]uninstallTarget{}, removable...), configTarget)
	for _, t := range all {
		if t.path == "" {
			return fmt.Errorf("could not resolve the %s directory; refusing to continue", t.label)
		}
		if !pathWithinHome(t.path, home) {
			return fmt.Errorf("%s directory %s resolves outside your home directory (%s); refusing to continue", t.label, t.path, home)
		}
	}

	daemons, err := discoverGrovedProcesses(paths.StateDir())
	if err != nil {
		return fmt.Errorf("failed to scan for running daemons: %w", err)
	}

	if !yes {
		printUninstallDryRun(removable, configTarget, daemons, removeConfig)
		return nil
	}

	return performUninstall(removable, configTarget, daemons, removeConfig)
}

// gatherUninstallTargets returns the directories removed by default and the
// config directory (only removed with --config).
func gatherUninstallTargets() ([]uninstallTarget, uninstallTarget) {
	mk := func(label, path string) uninstallTarget {
		t := uninstallTarget{label: label, path: path}
		if path != "" {
			if _, err := os.Stat(path); err == nil {
				t.exists = true
			}
		}
		return t
	}

	removable := []uninstallTarget{
		mk("state", paths.StateDir()),
		mk("data", paths.DataDir()),
		mk("binaries", paths.BinDir()),
		mk("cache", paths.CacheDir()),
	}
	return removable, mk("config", paths.ConfigDir())
}

// pathWithinHome reports whether p is strictly inside home (lexically, after
// cleaning). p == home itself is rejected.
func pathWithinHome(p, home string) bool {
	if p == "" || home == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(home), filepath.Clean(p))
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// discoverGrovedProcesses scans stateDir for groved*.pid files and resolves
// each PID to its process command. Discovery only; nothing is signaled.
func discoverGrovedProcesses(stateDir string) ([]grovedProcess, error) {
	if stateDir == "" {
		return nil, nil
	}
	matches, err := filepath.Glob(filepath.Join(stateDir, "groved*.pid"))
	if err != nil {
		return nil, err
	}

	var procs []grovedProcess
	for _, pidPath := range matches {
		content, err := os.ReadFile(pidPath) //nolint:gosec // G304: path comes from our own state dir glob
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
		if err != nil || pid <= 0 {
			continue
		}

		command := processCommand(pid)
		procs = append(procs, grovedProcess{
			pidPath: pidPath,
			pid:     pid,
			command: command,
			matches: commandLooksLikeGroved(command),
		})
	}
	return procs, nil
}

// processCommand returns the command of a running process, or "" if the
// process does not exist or cannot be inspected.
func processCommand(pid int) string {
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// commandLooksLikeGroved guards against stale or hijacked pidfiles: we only
// ever signal PIDs whose command is a groved binary.
func commandLooksLikeGroved(command string) bool {
	if command == "" {
		return false
	}
	base := filepath.Base(strings.Fields(command)[0])
	return base == "groved" || strings.HasPrefix(base, "groved-")
}

func printUninstallDryRun(removable []uninstallTarget, configTarget uninstallTarget, daemons []grovedProcess, removeConfig bool) {
	fmt.Println("grove uninstall — DRY RUN (nothing will be removed or signaled)")
	fmt.Println()

	fmt.Println("Daemons that would be stopped (SIGTERM):")
	any := false
	for _, d := range daemons {
		if d.matches {
			fmt.Printf("  pid %d  %s  (pidfile: %s)\n", d.pid, d.command, d.pidPath)
			any = true
		} else if d.command == "" {
			fmt.Printf("  (stale pidfile, process gone: %s)\n", d.pidPath)
		} else {
			fmt.Printf("  (skipped: pid %d is %q, not groved: %s)\n", d.pid, d.command, d.pidPath)
		}
	}
	if !any && len(daemons) == 0 {
		fmt.Println("  none found")
	}
	fmt.Println()

	fmt.Println("Directories that would be removed:")
	for _, t := range removable {
		fmt.Printf("  %-9s %s%s\n", t.label+":", t.path, presenceNote(t))
	}
	fmt.Println()

	if removeConfig {
		fmt.Println("Config directory (will be REMOVED because --config was given):")
	} else {
		fmt.Println("Config directory (kept; pass --config to remove it too):")
	}
	fmt.Printf("  %-9s %s%s\n", configTarget.label+":", configTarget.path, presenceNote(configTarget))
	fmt.Println()

	fmt.Println("Dry run complete. Nothing was removed.")
	fmt.Println("Run again with --yes to remove.")
}

func presenceNote(t uninstallTarget) string {
	if !t.exists {
		return "  (not present)"
	}
	return ""
}

func performUninstall(removable []uninstallTarget, configTarget uninstallTarget, daemons []grovedProcess, removeConfig bool) error {
	// Stop daemons first so they do not recreate state while we remove it.
	for _, d := range daemons {
		switch {
		case d.matches:
			if err := syscall.Kill(d.pid, syscall.SIGTERM); err != nil {
				fmt.Printf("warning: failed to stop pid %d (%s): %v\n", d.pid, d.command, err)
			} else {
				fmt.Printf("stopped: pid %d (%s) via SIGTERM (pidfile: %s)\n", d.pid, d.command, d.pidPath)
			}
		case d.command == "":
			fmt.Printf("skipped: stale pidfile %s (process %d already gone)\n", d.pidPath, d.pid)
		default:
			fmt.Printf("skipped: pid %d is %q, not groved (pidfile: %s)\n", d.pid, d.command, d.pidPath)
		}
	}

	targets := append([]uninstallTarget{}, removable...)
	if removeConfig {
		targets = append(targets, configTarget)
	}

	var firstErr error
	for _, t := range targets {
		if !t.exists {
			fmt.Printf("skipped: %s %s (not present)\n", t.label, t.path)
			continue
		}
		if err := os.RemoveAll(t.path); err != nil {
			fmt.Printf("error: failed to remove %s %s: %v\n", t.label, t.path, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		fmt.Printf("removed: %s %s\n", t.label, t.path)
	}

	if !removeConfig {
		fmt.Printf("kept:    config %s (pass --config to remove it)\n", configTarget.path)
	}

	return firstErr
}
