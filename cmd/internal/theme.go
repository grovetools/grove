package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	coredaemon "github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/tui/theme"
	"github.com/spf13/cobra"
)

// newThemeCmd is the plumbing verb that hands grove's resolved theme to
// non-Go consumers. It is a byte-for-byte twin of `grove-nvim internal theme`
// (grove.nvim/cmd/internal_theme.go): same resolution order, same fallback,
// same encoder.
//
// It exists separately because the consumers are different. grove.nvim's copy
// serves the Lua side of the Neovim plugin, and a consumer that is not Neovim
// — the grove-pi theme extension, which shells out to `grove internal theme`
// at session start — should not have to find and depend on the Neovim helper
// binary just to learn what color the background is. Neither copy is the
// source of truth: coredaemon.BuildThemePayload is, and both are thin wrappers
// over it, so the two stay in step by construction rather than by discipline.
//
// Contract (machine-readable):
//   - stdout: exactly one JSON object, newline-terminated, nothing else;
//   - the shape is the daemon's theme_changed SSE payload, so a consumer that
//     later grows a live-update path parses ONE shape for both;
//   - an unknown/renamed theme in config still yields a payload (the default
//     theme's), matching core's resolveThemeColors behavior — a stale config
//     value must not leave a caller themeless;
//   - errors exit nonzero with nothing on stdout.
//
// With --watch the same first object is followed by one more per theme CHANGE,
// newline-delimited, for as long as the process lives. The consumer is a
// long-running agent session, which shapes the whole contract: a missing or
// restarting daemon is retried rather than reported, repeats are suppressed so
// a reconnect's opening snapshot does not repaint anything, and exit 0 means
// "no further changes are possible" (GROVE_THEME pins this process tree) rather
// than "try again" — so a supervisor can tell a finished watcher from a crashed
// one by its status alone.
func newThemeCmd() *cobra.Command {
	var watch bool

	cmd := &cobra.Command{
		Use:   "theme",
		Short: "Print the resolved current theme as JSON (both appearances)",
		Long: `Resolves the current grove theme (GROVE_THEME env var, then config
tui.theme, then the default) and prints its fully derived palettes for both
appearances as a single JSON object. The shape is identical to the daemon's
theme_changed SSE payload so consumers parse one shape everywhere.

With --watch the process stays open after that first object and prints one
further object per theme change, so a long-lived consumer can restyle itself
without polling.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload, err := resolveThemePayload()
			if err != nil {
				return err
			}
			// OutOrStdout defaults to os.Stdout, so the bytes on the wire are
			// the same as grove.nvim's direct os.Stdout encode; the indirection
			// exists only so the test can capture them.
			enc := json.NewEncoder(cmd.OutOrStdout())
			if err := enc.Encode(payload); err != nil {
				return err
			}
			if !watch {
				return nil
			}
			// An explicit GROVE_THEME pins this process tree to one theme, so
			// there is nothing a config change could legitimately move. Exiting
			// zero — rather than idling on a stream whose events must all be
			// ignored — is also what tells a consumer not to respawn us.
			if strings.TrimSpace(os.Getenv("GROVE_THEME")) != "" {
				return nil
			}
			return watchTheme(cmd.Context(), enc, cmd.ErrOrStderr(), payload)
		},
	}

	cmd.Flags().BoolVar(&watch, "watch", false,
		"stay open and print one JSON object per theme change (exits 0 when GROVE_THEME pins the theme)")

	return cmd
}

// resolveThemePayload applies core's precedence chain and never returns a
// themeless answer for a merely stale config value.
func resolveThemePayload() (*coredaemon.ThemeChangedPayload, error) {
	// theme.CurrentName wraps the whole precedence chain: GROVE_THEME env var,
	// then the resolved config's tui.theme, then the default.
	name := theme.CurrentName()
	payload, ok := coredaemon.BuildThemePayload(name)
	if !ok {
		// Unknown/renamed theme in config: fall back to the default rather than
		// erroring, matching core's resolveThemeColors.
		payload, ok = coredaemon.BuildThemePayload(theme.DefaultThemeName)
	}
	if !ok {
		return nil, fmt.Errorf("theme registry has no palette for %q", name)
	}
	return payload, nil
}

// Reconnect backoff. The daemon going away is routine — it restarts on upgrade,
// on config reload, on a `groved restart` — and the consumer on the other end of
// this pipe is a coding session that may outlive several of those. So a closed
// stream is a reconnect, never an exit.
const (
	themeWatchBackoffMin = time.Second
	themeWatchBackoffMax = 30 * time.Second
)

// watchTheme follows the daemon's theme_changed broadcasts and writes one JSON
// object per CHANGE, forever, until ctx is canceled or stdout goes away.
//
// It reconnects rather than returning when the daemon is absent or restarts:
// requiring a live daemon at spawn time would make a cosmetic feature fail in
// exactly the situation where it is least affordable to fail loudly (a session
// starting while groved is mid-restart). Nothing here starts a daemon (see
// subscribeTheme): a theme watcher has no business booting one.
func watchTheme(ctx context.Context, enc *json.Encoder, logw io.Writer, current *coredaemon.ThemeChangedPayload) error {
	last, err := json.Marshal(current)
	if err != nil {
		return err
	}
	backoff := themeWatchBackoffMin
	// Only state CHANGES are logged, starting from "nothing reported yet" so the
	// first outcome always is. A watcher outlives whole working days, so a line
	// per retry would turn a missing daemon into an unbounded log — but a
	// watcher that has never said anything is indistinguishable from one that is
	// working, which is the state this feature is hardest to debug in.
	// Diagnostics go to stderr in every case: stdout is a parsed contract.
	var wasConnected *bool
	for {
		updates, connected := subscribeTheme(ctx)
		if wasConnected == nil || connected != *wasConnected {
			if connected {
				fmt.Fprintln(logw, "grove internal theme --watch: following daemon theme events")
			} else {
				fmt.Fprintf(logw, "grove internal theme --watch: daemon unavailable, retrying (up to every %s)\n", themeWatchBackoffMax)
			}
			wasConnected = &connected
		}
		if connected {
			backoff = themeWatchBackoffMin
			last, err = emitThemeChanges(ctx, updates, enc, last)
			if err != nil {
				// A write failure is the consumer having gone away (EPIPE) or a
				// broken stdout; either way there is nobody left to inform.
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > themeWatchBackoffMax {
			backoff = themeWatchBackoffMax
		}
	}
}

// subscribeTheme opens one subscription, reporting whether it connected at all
// so the caller can tell "daemon absent" from "stream ended".
//
// It asks the caller's scoped daemon first and the host's unscoped one second.
// The fallback is not a nicety: an agent session runs with GROVE_SCOPE pointing
// at its worktree, and a worktree does not always have a daemon of its own —
// while the theme it wants is GLOBAL config, which the unscoped daemon watches
// just as well. Without the second hop the common case (pi in a worktree, one
// host-wide groved) is silently unwatchable.
//
// Nothing here starts a daemon. Both hops are connect-only, which is why the
// socket is dialed directly rather than through the auto-starting factories.
//
// The server-side type filter is a bandwidth courtesy, not a guarantee: an
// older daemon ignores ?types= and sends the whole firehose (see
// StreamCapabilities.TypeFilter), which is why every frame still goes through
// ParseThemeChanged rather than being trusted for its type.
func subscribeTheme(ctx context.Context) (<-chan coredaemon.StateUpdate, bool) {
	for _, socket := range themeSockets() {
		client, err := coredaemon.NewRemoteClient(socket)
		if err != nil {
			continue
		}
		if !client.IsRunning() {
			_ = client.Close()
			continue
		}
		updates, _, err := client.StreamStateWithOptions(ctx, coredaemon.StreamOptions{
			Types: []string{coredaemon.UpdateTypeThemeChanged, "initial"},
		})
		if err != nil {
			_ = client.Close()
			continue
		}
		// The client owns the live stream; closing it here would cancel it.
		return updates, true
	}
	return nil, false
}

// themeSockets lists the daemons worth asking, nearest first and without
// repeating the same socket when no scope is set.
func themeSockets() []string {
	global := paths.SocketPath()
	scoped := paths.SocketPath(coredaemon.ResolveClientScope())
	if scoped == global {
		return []string{global}
	}
	return []string{scoped, global}
}

// emitThemeChanges drains one subscription, writing a line per payload that
// differs from the last one written, and returns the new last-written bytes.
//
// Deduplicating on the encoded payload rather than on arrival is what makes the
// reconnect loop safe: every subscription opens with an "initial" snapshot
// carrying the current theme, so an unconditional write would repaint the
// consumer on every daemon restart — and on a filterless old daemon, on every
// snapshot it sends.
func emitThemeChanges(ctx context.Context, updates <-chan coredaemon.StateUpdate, enc *json.Encoder, last []byte) ([]byte, error) {
	for {
		select {
		case <-ctx.Done():
			return last, nil
		case update, ok := <-updates:
			if !ok {
				return last, nil
			}
			payload, ok := coredaemon.ParseThemeChanged(update)
			if !ok {
				continue
			}
			next, err := json.Marshal(payload)
			if err != nil {
				continue
			}
			if bytes.Equal(next, last) {
				continue
			}
			if err := enc.Encode(payload); err != nil {
				return last, err
			}
			last = next
		}
	}
}
