package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/spf13/cobra"
)

type piCodexStatus struct {
	Provider        string `json:"provider"`
	Present         bool   `json:"present"`
	Type            string `json:"type,omitempty"`
	Usable          *bool  `json:"usable,omitempty"`
	AccountID       string `json:"account_id,omitempty"`
	UpstreamRevoked *bool  `json:"upstream_revoked,omitempty"`
}

func newSatelliteAuthCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("auth", "Manage guest-local satellite authentication")
	cmd.AddCommand(newSatellitePiCodexAuthCmd())
	return cmd
}

func newSatellitePiCodexAuthCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("pi-codex", "Manage Pi openai-codex OAuth inside a full satellite")
	cmd.Long = `Manage Pi's guest-local openai-codex OAuth record through Pi 0.80.10's
native auth API and device flow. OAuth values remain in ~/.pi/agent/auth.json
inside the guest and are never copied to Grove config, argv, environment files,
or the laptop. This is independent of Codex CLI's auth store.`
	cmd.AddCommand(newSatellitePiCodexLoginCmd(), newSatellitePiCodexStatusCmd(), newSatellitePiCodexLogoutCmd())
	return cmd
}

func newSatellitePiCodexLoginCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("login <name>", "Authorize Pi's openai-codex provider with a device code")
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		ssh, cleanup, err := piCodexSSH(name)
		if err != nil {
			return err
		}
		defer cleanup()
		fmt.Fprintln(cmd.OutOrStdout(), "Starting guest-local Pi openai-codex device authorization. OAuth values will not be displayed.")
		if err := ssh.execCommand(piCodexRemoteCommand("login", false), false); err != nil {
			return fmt.Errorf("Pi Codex login failed; no partial credential is retained: %w", sanitizeRemoteAuthError(err))
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Login complete. The credential survives stop/start and reboot; down/reset/reclone removes it and requires a new login.")
		return nil
	}
	return cmd
}

func newSatellitePiCodexStatusCmd() *cobra.Command {
	var usable, jsonOutput bool
	cmd := cli.NewStandardCommand("status <name>", "Report guest-local Pi Codex credential metadata")
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	cmd.Flags().BoolVar(&usable, "usable", false, "exercise Pi's non-billable auth resolution/refresh path (may rotate credentials)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "emit bounded metadata JSON")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ssh, cleanup, err := piCodexSSH(args[0])
		if err != nil {
			return err
		}
		defer cleanup()
		out, err := ssh.outputScript(piCodexRemoteCommand("status", usable))
		if err != nil {
			return fmt.Errorf("Pi Codex status failed: %w", sanitizeRemoteAuthError(err))
		}
		status, err := parsePiCodexStatus(out)
		if err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "provider: %s\npresent: %t\n", status.Provider, status.Present)
		if status.Type != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "type: %s\n", status.Type)
		}
		if status.Usable != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "usable: %t\n", *status.Usable)
		}
		if !status.Present {
			fmt.Fprintf(cmd.OutOrStdout(), "login: grove satellite auth pi-codex login %s\n", args[0])
		}
		return nil
	}
	return cmd
}

func newSatellitePiCodexLogoutCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("logout-local <name>", "Delete only Pi's guest-local openai-codex credential")
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ssh, cleanup, err := piCodexSSH(args[0])
		if err != nil {
			return err
		}
		defer cleanup()
		out, err := ssh.outputScript(piCodexRemoteCommand("logout-local", false))
		if err != nil {
			return fmt.Errorf("Pi Codex local logout failed: %w", sanitizeRemoteAuthError(err))
		}
		if _, err := parsePiCodexStatus(out); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Guest-local Pi credential deleted. This does not revoke upstream OAuth authority or guarantee secure erasure.")
		return nil
	}
	return cmd
}

func piCodexSSH(name string) (*satelliteSSH, func(), error) {
	entry, ok := loadMergedSatellites()[name]
	if !ok {
		return nil, func() {}, fmt.Errorf("satellite %q not found", name)
	}
	if entry.Kind == satelliteKindExec {
		return nil, func() {}, fmt.Errorf("satellite %q is exec-only; Pi auth requires a full satellite", name)
	}
	if satelliteEntryIsPartial(entry) {
		return nil, func() {}, fmt.Errorf("satellite %q is partially provisioned", name)
	}
	dir, err := os.MkdirTemp("", "grove-satellite-pi-auth-")
	if err != nil {
		return nil, func() {}, err
	}
	ssh, err := newSatelliteSSH(entry, dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, func() {}, err
	}
	return ssh, func() { _ = os.RemoveAll(dir) }, nil
}

func piCodexRemoteCommand(operation string, usable bool) string {
	flag := ""
	if usable {
		flag = " --usable"
	}
	postCheck := ""
	if operation == "login" {
		postCheck = `pi --list-models openai-codex | awk 'NR>1 {print $1}' | grep -qx openai-codex || { echo 'Pi openai-codex models are unavailable after login' >&2; exit 1; }`
	}
	return fmt.Sprintf(`set -euo pipefail
umask 077
runtime="$HOME/.config/grove/pi-runtime/metadata.json"
[ -f "$runtime" ] && [ ! -L "$runtime" ] || { echo 'managed Pi runtime is missing' >&2; exit 1; }
helper=$(node -e 'const m=require(process.argv[1]); if(m.version!=="0.80.10" || typeof m.auth_helper!=="string") process.exit(2); process.stdout.write(m.auth_helper)' "$runtime")
case "$helper" in "$HOME/.local/share/grove/pi-packages/sha256/"*/bin/pi-codex-auth.mjs) ;; *) echo 'managed Pi auth helper path is invalid' >&2; exit 1;; esac
[ -f "$helper" ] && [ ! -L "$helper" ] || { echo 'managed Pi auth helper is missing' >&2; exit 1; }
authdir="$HOME/.pi/agent"; auth="$authdir/auth.json"
[ ! -L "$HOME/.pi" ] && [ ! -L "$authdir" ] || { echo 'Pi auth parent is a symlink' >&2; exit 1; }
mkdir -p "$authdir"; chmod 700 "$HOME/.pi" "$authdir"
if [ -e "$auth" ] || [ -L "$auth" ]; then
  [ -f "$auth" ] && [ ! -L "$auth" ] || { echo 'Pi auth destination is not a regular file' >&2; exit 1; }
  [ "$(stat -c '%%u' "$auth")" = "$(id -u)" ] && [ "$(stat -c '%%a' "$auth")" = 600 ] || { echo 'Pi auth file owner/mode is unsafe' >&2; exit 1; }
fi
node "$helper" %s --auth-path "$auth"%s
%s
if [ -e "$auth" ]; then
  [ -f "$auth" ] && [ ! -L "$auth" ] && [ "$(stat -c '%%u' "$auth")" = "$(id -u)" ] && [ "$(stat -c '%%a' "$auth")" = 600 ] || { echo 'Pi auth file failed post-operation safety checks' >&2; exit 1; }
fi
`, operation, flag, postCheck)
}

func parsePiCodexStatus(output string) (piCodexStatus, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 1 || len(lines[0]) > 1024 {
		return piCodexStatus{}, fmt.Errorf("Pi auth helper returned an invalid bounded response")
	}
	dec := json.NewDecoder(strings.NewReader(lines[0]))
	dec.DisallowUnknownFields()
	var status piCodexStatus
	if err := dec.Decode(&status); err != nil {
		return piCodexStatus{}, fmt.Errorf("Pi auth helper returned invalid metadata")
	}
	if status.Provider != "openai-codex" || (status.Type != "" && status.Type != "oauth") {
		return piCodexStatus{}, fmt.Errorf("Pi auth helper returned invalid provider metadata")
	}
	return status, nil
}

func sanitizeRemoteAuthError(err error) error {
	if err == nil {
		return nil
	}
	// ssh may include remote stderr. Keep only known operational guidance;
	// provider/HTTP bodies and accidental credential values never cross here.
	text := err.Error()
	for _, allowed := range []string{"managed Pi runtime is missing", "managed Pi auth helper path is invalid", "managed Pi auth helper is missing", "Pi auth parent is a symlink", "Pi auth destination is not a regular file", "Pi auth file owner/mode is unsafe", "Pi auth file failed post-operation safety checks"} {
		if strings.Contains(text, allowed) {
			return fmt.Errorf("%s", allowed)
		}
	}
	return fmt.Errorf("remote authentication operation failed")
}
