package internal

import (
	"encoding/json"
	"fmt"

	coredaemon "github.com/grovetools/core/pkg/daemon"
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
func newThemeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "theme",
		Short: "Print the resolved current theme as JSON (both appearances)",
		Long: `Resolves the current grove theme (GROVE_THEME env var, then config
tui.theme, then the default) and prints its fully derived palettes for both
appearances as a single JSON object. The shape is identical to the daemon's
theme_changed SSE payload so consumers parse one shape everywhere.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// theme.CurrentName wraps the whole precedence chain: GROVE_THEME
			// env var, then the resolved config's tui.theme, then the default.
			name := theme.CurrentName()
			payload, ok := coredaemon.BuildThemePayload(name)
			if !ok {
				// Unknown/renamed theme in config: fall back to the default
				// rather than erroring, matching core's resolveThemeColors.
				payload, ok = coredaemon.BuildThemePayload(theme.DefaultThemeName)
			}
			if !ok {
				return fmt.Errorf("theme registry has no palette for %q", name)
			}
			// OutOrStdout defaults to os.Stdout, so the bytes on the wire are
			// the same as grove.nvim's direct os.Stdout encode; the indirection
			// exists only so the test can capture them.
			return json.NewEncoder(cmd.OutOrStdout()).Encode(payload)
		},
	}
}
