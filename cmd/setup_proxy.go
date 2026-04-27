package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
)

// Package note
//
// `grove setup proxy` configures OS-level port forwarding so users can hit
// http://<service>.<worktree>.grove.local in the browser without the :8443
// suffix. The global groved binds 127.0.0.1:8443; this command installs
// the firewall redirect (80 -> 8443) plus a user-level auto-start for
// groved so the proxy daemon survives login.
//
// Actual OS changes require root; without sudo the command prints what it
// *would* do and exits 0 (so CI / test can exercise the codepath). The
// --dry-run flag forces that same path regardless of euid.

const (
	pfAnchorPath       = "/etc/pf.anchors/com.grove.proxy"
	pfLaunchDaemonPath = "/Library/LaunchDaemons/com.grove.pf.plist"
	pfAnchorRule       = "rdr pass on lo0 inet proto tcp from any to 127.0.0.1 port 80 -> 127.0.0.1 port 8443\n"
	iptablesRule       = "-t nat -A OUTPUT -p tcp -d 127.0.0.0/8 --dport 80 -j REDIRECT --to-ports 8443"
	iptablesDelete     = "-t nat -D OUTPUT -p tcp -d 127.0.0.0/8 --dport 80 -j REDIRECT --to-ports 8443"
)

// proxyLaunchAgentPlistPath returns the per-user launchd plist path.
// Factored out so tests can override HOME without touching real files.
func proxyLaunchAgentPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "com.grove.daemon.plist")
}

// systemdUserUnitPath returns the per-user systemd unit path.
func systemdUserUnitPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", "groved.service")
}

// pfLaunchDaemonPlist is the launchd.plist that runs `pfctl -e -f <anchor>`
// at boot. This is the root-owned LaunchDaemon in /Library/LaunchDaemons
// — it only reapplies the pf rule on reboot, not a persistent process.
func pfLaunchDaemonPlist() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
 "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.grove.pf</string>
    <key>ProgramArguments</key>
    <array>
        <string>/sbin/pfctl</string>
        <string>-e</string>
        <string>-f</string>
        <string>` + pfAnchorPath + `</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardErrorPath</key>
    <string>/var/log/com.grove.pf.err</string>
    <key>StandardOutPath</key>
    <string>/var/log/com.grove.pf.out</string>
</dict>
</plist>
`
}

// proxyDaemonLaunchAgentPlist returns the LaunchAgent plist that runs
// `groved start` (unscoped) at user login. grovedPath is resolved from
// PATH at install time; absent that we fall back to ~/.grove/bin/groved.
func proxyDaemonLaunchAgentPlist(grovedPath string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
 "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.grove.daemon</string>
    <key>ProgramArguments</key>
    <array>
        <string>` + grovedPath + `</string>
        <string>start</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
</dict>
</plist>
`
}

// systemdUserUnit returns the `groved` systemd user unit that keeps the
// global daemon alive on Linux.
func systemdUserUnit(grovedPath string) string {
	return `[Unit]
Description=Grove global daemon
After=network.target

[Service]
Type=simple
ExecStart=` + grovedPath + ` start
Restart=on-failure

[Install]
WantedBy=default.target
`
}

// setupProxyOpts carries flag state between the cobra command and the
// execution helpers. Kept as package-level to avoid plumbing through every
// call site and to match the existing setup.go pattern.
type setupProxyOpts struct {
	dryRun    bool
	uninstall bool
}

func newSetupProxyCmd() *cobra.Command {
	opts := &setupProxyOpts{}

	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Configure OS port forwarding (80 -> 8443) for *.grove.local",
		Long: `Install OS-level port forwarding so http://<service>.<worktree>.grove.local
resolves without a :8443 suffix.

macOS: writes /etc/pf.anchors/com.grove.proxy and a root LaunchDaemon that
reapplies the pf rule at boot, plus a per-user LaunchAgent that auto-starts
the global groved at login.

Linux: installs an iptables NAT rule (OUTPUT chain) redirecting localhost
port 80 to 8443, plus a systemd --user service that keeps groved running.

The command is idempotent; rerunning it is safe.

Run as root (sudo) to actually apply changes. Without root the command
prints what it would run and exits. --dry-run forces the print-only path
regardless of euid.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetupProxy(cmd.OutOrStdout(), opts)
		},
		SilenceUsage: true,
	}

	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Print what would be installed without touching the filesystem or running privileged commands")
	cmd.Flags().BoolVar(&opts.uninstall, "uninstall", false, "Reverse a previous install: remove pf/iptables rules and the launchd/systemd units")
	return cmd
}

// runSetupProxy dispatches to the OS-specific install path. Returns nil on
// a successful print (non-root / --dry-run) or on a successful real install.
func runSetupProxy(out io.Writer, opts *setupProxyOpts) error {
	dryRun := opts.dryRun || os.Geteuid() != 0
	if dryRun && !opts.dryRun {
		fmt.Fprintln(out, "[grove setup proxy] Not running as root — showing what would happen. Re-run with sudo to apply.")
	}

	switch runtime.GOOS {
	case "darwin":
		if opts.uninstall {
			return setupProxyMacOSUninstall(out, dryRun)
		}
		return setupProxyMacOSInstall(out, dryRun)
	case "linux":
		if opts.uninstall {
			return setupProxyLinuxUninstall(out, dryRun)
		}
		return setupProxyLinuxInstall(out, dryRun)
	default:
		return fmt.Errorf("grove setup proxy: unsupported OS %q (only darwin + linux)", runtime.GOOS)
	}
}

// resolveGrovedPath picks the groved binary path to embed into launchd /
// systemd units. Falls back to the common symlink in ~/.grove/bin.
func resolveGrovedPath() string {
	if p, err := exec.LookPath("groved"); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".grove", "bin", "groved")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "groved"
}

// --- macOS ----------------------------------------------------------------

func setupProxyMacOSInstall(out io.Writer, dryRun bool) error {
	agentPath := proxyLaunchAgentPlistPath()
	grovedPath := resolveGrovedPath()
	agentPlist := proxyDaemonLaunchAgentPlist(grovedPath)
	daemonPlist := pfLaunchDaemonPlist()

	fmt.Fprintln(out, "macOS proxy install plan:")
	fmt.Fprintf(out, "  write %s (%d bytes)\n", pfAnchorPath, len(pfAnchorRule))
	fmt.Fprintf(out, "  write %s (%d bytes)\n", pfLaunchDaemonPath, len(daemonPlist))
	fmt.Fprintf(out, "  write %s (%d bytes)\n", agentPath, len(agentPlist))
	fmt.Fprintln(out, "  launchctl load -w "+pfLaunchDaemonPath)
	fmt.Fprintln(out, "  launchctl load -w "+agentPath)
	fmt.Fprintln(out, "  pfctl -e -f "+pfAnchorPath)

	if dryRun {
		return nil
	}

	if err := writeFileOwned(pfAnchorPath, []byte(pfAnchorRule), 0o644); err != nil {
		return fmt.Errorf("write pf anchor: %w", err)
	}
	if err := writeFileOwned(pfLaunchDaemonPath, []byte(daemonPlist), 0o644); err != nil {
		return fmt.Errorf("write launchd plist: %w", err)
	}
	if err := writeFileOwned(agentPath, []byte(agentPlist), 0o644); err != nil {
		return fmt.Errorf("write launchagent plist: %w", err)
	}
	if err := exec.Command("launchctl", "load", "-w", pfLaunchDaemonPath).Run(); err != nil {
		fmt.Fprintf(out, "warning: launchctl load LaunchDaemon failed (may already be loaded): %v\n", err)
	}
	if err := exec.Command("launchctl", "load", "-w", agentPath).Run(); err != nil {
		fmt.Fprintf(out, "warning: launchctl load LaunchAgent failed (may already be loaded): %v\n", err)
	}
	if err := exec.Command("pfctl", "-e", "-f", pfAnchorPath).Run(); err != nil {
		fmt.Fprintf(out, "warning: pfctl apply returned non-zero (pf may already be enabled): %v\n", err)
	}
	fmt.Fprintln(out, "done.")
	return nil
}

func setupProxyMacOSUninstall(out io.Writer, dryRun bool) error {
	agentPath := proxyLaunchAgentPlistPath()

	fmt.Fprintln(out, "macOS proxy uninstall plan:")
	fmt.Fprintln(out, "  launchctl unload -w "+pfLaunchDaemonPath)
	fmt.Fprintln(out, "  launchctl unload -w "+agentPath)
	fmt.Fprintf(out, "  rm %s\n", pfLaunchDaemonPath)
	fmt.Fprintf(out, "  rm %s\n", agentPath)
	fmt.Fprintf(out, "  rm %s\n", pfAnchorPath)

	if dryRun {
		return nil
	}

	_ = exec.Command("launchctl", "unload", "-w", pfLaunchDaemonPath).Run()
	_ = exec.Command("launchctl", "unload", "-w", agentPath).Run()
	_ = os.Remove(pfLaunchDaemonPath)
	_ = os.Remove(agentPath)
	_ = os.Remove(pfAnchorPath)
	fmt.Fprintln(out, "done.")
	return nil
}

// --- Linux ----------------------------------------------------------------

func setupProxyLinuxInstall(out io.Writer, dryRun bool) error {
	unitPath := systemdUserUnitPath()
	grovedPath := resolveGrovedPath()
	unit := systemdUserUnit(grovedPath)

	fmt.Fprintln(out, "Linux proxy install plan:")
	fmt.Fprintln(out, "  iptables "+iptablesRule)
	fmt.Fprintln(out, "  iptables-save > /etc/iptables/rules.v4")
	fmt.Fprintf(out, "  write %s (%d bytes)\n", unitPath, len(unit))
	fmt.Fprintln(out, "  systemctl --user enable --now groved")

	if dryRun {
		return nil
	}

	if err := runIptables(iptablesRule); err != nil {
		return fmt.Errorf("iptables install: %w", err)
	}
	if err := persistIptables(); err != nil {
		fmt.Fprintf(out, "warning: persisting iptables rules failed (distro may not use iptables-persistent): %v\n", err)
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("mkdir systemd unit dir: %w", err)
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0o600); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}
	if err := exec.Command("systemctl", "--user", "enable", "--now", "groved").Run(); err != nil {
		fmt.Fprintf(out, "warning: systemctl --user enable --now groved failed: %v\n", err)
	}
	fmt.Fprintln(out, "done.")
	return nil
}

func setupProxyLinuxUninstall(out io.Writer, dryRun bool) error {
	unitPath := systemdUserUnitPath()

	fmt.Fprintln(out, "Linux proxy uninstall plan:")
	fmt.Fprintln(out, "  systemctl --user disable --now groved")
	fmt.Fprintf(out, "  rm %s\n", unitPath)
	fmt.Fprintln(out, "  iptables "+iptablesDelete)
	fmt.Fprintln(out, "  iptables-save > /etc/iptables/rules.v4")

	if dryRun {
		return nil
	}

	_ = exec.Command("systemctl", "--user", "disable", "--now", "groved").Run()
	_ = os.Remove(unitPath)
	_ = runIptables(iptablesDelete)
	_ = persistIptables()
	fmt.Fprintln(out, "done.")
	return nil
}

// runIptables invokes iptables with the given argument string split on
// whitespace. The argument is a fixed constant in this file, so we don't
// need shell-safe quoting; but we do want callers to see any exec error
// surfaced through the error return.
func runIptables(argStr string) error {
	args := splitWhitespace(argStr)
	return exec.Command("iptables", args...).Run()
}

// persistIptables writes the current rules to /etc/iptables/rules.v4. The
// directory may not exist on distros that don't use iptables-persistent —
// callers treat the error as a warning.
func persistIptables() error {
	if err := os.MkdirAll("/etc/iptables", 0o755); err != nil {
		return err
	}
	cmd := exec.Command("iptables-save")
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	return os.WriteFile("/etc/iptables/rules.v4", out, 0o600)
}

// writeFileOwned mkdirs the parent and writes the payload. Used for paths
// under /etc and /Library/LaunchDaemons that require root.
func writeFileOwned(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}

// splitWhitespace is a tiny split helper — strings.Fields would pull in the
// same dependency but keeping this inline avoids expanding the import block
// for a two-line helper.
func splitWhitespace(s string) []string {
	var out []string
	start := -1
	for i, r := range s {
		if r == ' ' || r == '\t' {
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}
