package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// The reach-the-guest verbs.
//
// An exec satellite IS an sshd endpoint — that is the whole of what `up`
// wires for it — yet until now nothing in the noun connected to one, so the
// endpoint had to be reassembled by hand from the state file plus the
// provider's key-path convention. That is worst for the docker target, whose
// sshd is published on a random loopback high port.
//
// Both verbs run over the same pinned transport every other satellite verb
// uses (newSatelliteSSH: the registry's host_key in a generated known_hosts
// with StrictHostKeyChecking=yes). An entry with no pinned key is refused
// rather than trusted on first use.

func newSatelliteExecCmd() *cobra.Command {
	var remoteDir string
	cmd := cli.NewStandardCommand("exec <name> -- <command>...", "Run a command on a satellite over its pinned SSH connection")
	cmd.Long = `Run one command on a satellite and exit with the command's own exit status.

Everything after '--' is the remote command; its words are quoted individually,
so they reach the remote shell as written. stdin, stdout and stderr are the
caller's, making this usable in a pipeline:

  grove satellite exec mysat -- grove version
  echo hi | grove satellite exec mysat -- cat
  grove satellite exec mysat --dir '~/code/grovetools' -- git -C grove log -1

The connection pins the registry's host key (never TOFU). Secrets belong on
stdin, never in the command words — argv is visible in the guest's process
list.`
	// One arg is accepted so the missing-command case gets the explanatory
	// error below rather than Cobra's "requires at least 2 arg(s)".
	cmd.Args = cobra.MinimumNArgs(1)
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&remoteDir, "dir", "", "Working directory on the satellite the command runs in (default: the login shell's)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		remote := args[1:]
		if len(remote) == 0 {
			return fmt.Errorf("no command given — everything after `--` is run on the satellite, e.g. `grove satellite exec %s -- grove version`", name)
		}
		return runSatelliteRemote(name, buildSatelliteRemoteCommand(remoteDir, remote), false)
	}
	return cmd
}

func newSatelliteSSHCmd() *cobra.Command {
	var remoteDir string
	cmd := cli.NewStandardCommand("ssh <name> [-- <command>...]", "Open a shell on a satellite over its pinned SSH connection")
	cmd.Long = `Open an interactive login shell on a satellite.

With a command after '--' this behaves exactly like 'grove satellite exec' — the
two names exist because 'ssh' is what people reach for and 'exec' is what a
script means. A pty is requested only when this command's stdin is a terminal,
so piping still works.

'grove satellite status' prints the equivalent raw ssh invocation for every
satellite, and 'status --json' carries it as ssh_command.`
	cmd.Args = cobra.MinimumNArgs(1)
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&remoteDir, "dir", "", "Working directory on the satellite the shell starts in (default: the login shell's)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		remote := buildSatelliteRemoteCommand(remoteDir, args[1:])
		if remote == "" && remoteDir != "" {
			remote = remoteChdir(remoteDir) + " && exec ${SHELL:-/bin/sh} -l"
		}
		return runSatelliteRemote(name, remote, isatty.IsTerminal(os.Stdin.Fd()))
	}
	return cmd
}

// runSatelliteRemote resolves the satellite's registry entry, builds the
// pinned transport, and runs command (empty = an interactive login shell) with
// the caller's stdio attached. The remote exit status becomes this process's:
// Cobra would otherwise rewrite every failure to 1, which is useless to a
// script branching on what the remote command actually returned (same
// propagation the tool-delegation path in root.go does).
func runSatelliteRemote(name, command string, tty bool) error {
	entry, ok := loadMergedSatellites()[name]
	if !ok {
		return fmt.Errorf("satellite %q not found in the registry (config or state) — run `grove satellite up %s` first", name, name)
	}
	if satelliteEntryIsPartial(entry) {
		return fmt.Errorf("satellite %q is only partially provisioned (no pinned endpoint): %s", name, satellitePartialUpRemediation(name))
	}
	tmpDir, err := os.MkdirTemp("", "grove-satellite-exec-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	ssh, err := newSatelliteSSH(entry, tmpDir)
	if err != nil {
		return fmt.Errorf("satellite %q: %w", name, err)
	}
	if err := ssh.execCommand(command, tty); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}

// buildSatelliteRemoteCommand renders the caller's argv as one remote shell
// command, quoting each word so the guest's shell sees exactly what was typed
// locally. An empty argv yields an empty command (a login shell).
func buildSatelliteRemoteCommand(dir string, args []string) string {
	if len(args) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		quoted = append(quoted, shellQuote(a))
	}
	command := strings.Join(quoted, " ")
	if dir != "" {
		command = remoteChdir(dir) + " && " + command
	}
	return command
}

// remoteChdir renders the cd that prefixes a remote command. The path is
// quoted (so a directory with a space works), which is also why a leading ~
// is rewritten explicitly: it names a directory on the SATELLITE, so the local
// shell never expands it and a quoted "~" would be taken literally there.
func remoteChdir(dir string) string {
	switch {
	case dir == "~":
		return `cd "$HOME"`
	case strings.HasPrefix(dir, "~/"):
		return `cd "$HOME"/` + shellQuote(strings.TrimPrefix(dir, "~/"))
	default:
		return "cd " + shellQuote(dir)
	}
}

// shellQuote wraps s in single quotes for a POSIX shell, escaping embedded
// single quotes the usual '\” way.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
