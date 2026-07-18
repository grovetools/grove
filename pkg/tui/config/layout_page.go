package config

import (
	"strconv"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/embed"
)

// Shipped layout defaults, mirroring the jsonschema defaults in
// core/config/types.go TUIConfig: when a key is unset in every layer the
// page displays — and toggling/cycling starts from — the value the app
// actually uses.
const defaultDrawerOrientation = "right"

// layoutTUI returns the merged [tui] section, or nil when absent.
func layoutTUI(lc *config.LayeredConfig) *config.TUIConfig {
	if lc != nil && lc.Final != nil {
		return lc.Final.TUI
	}
	return nil
}

// LayoutSettings returns the Layout page's setting descriptors: the active
// sessions drawer (orientation + expanded-on-start), the icon rail
// expanded-on-start, and Home-on-startup. The first three hot-apply through
// the treemux SettingAppliedMsg handler; Home-on-startup is startup-only
// (no apply domain).
//
// Home-on-startup is the INVERTED presentation of tui.hide_splash_on_startup
// — the same key the Home panel's own h toggle writes via treemux's
// setHideSplash closure (treemux/cmd/start.go). The row negates on read AND
// write (WriteTransform), so what lands in TOML is always the plain
// hide_splash_on_startup Go bool.
//
// Staleness note (recorded decision): treemux caches the startup value in
// its hideSplashPref closure, which only setHideSplash updates. A write from
// this page therefore leaves a later-reopened Home panel's toggle showing
// the pre-write state until restart. We deliberately do NOT wire a refresh:
// it would need a new apply domain plus a model→cmd-closure seam for a
// purely cosmetic, self-correcting mismatch (toggling from the stale panel
// rewrites the key with a typed bool — no corruption, worst case one extra
// toggle), and spec 23's Welcome work owns that surface next.
func LayoutSettings() []Setting {
	return []Setting{
		{
			ID:          "drawer_orientation",
			Label:       "Drawer position",
			Description: "Where the active sessions drawer lives: a vertical sidebar (right) or a horizontal bar (bottom)",
			Path:        []string{"tui", "drawer_orientation"},
			Control:     ControlSelect,
			Options:     []string{"right", "bottom"},
			Read: func(lc *config.LayeredConfig) string {
				if t := layoutTUI(lc); t != nil && t.DrawerOrientation != "" {
					return t.DrawerOrientation
				}
				return defaultDrawerOrientation
			},
			ApplyDomain: embed.SettingDomainDrawerOrientation,
		},
		{
			ID:          "drawer_expanded",
			Label:       "Drawer expanded on start",
			Description: "Start the active sessions drawer expanded (full list) instead of collapsed (mini icons)",
			Path:        []string{"tui", "drawer_expanded"},
			Control:     ControlBool,
			Read: func(lc *config.LayeredConfig) string {
				t := layoutTUI(lc)
				return strconv.FormatBool(t != nil && t.DrawerExpanded)
			},
			ApplyDomain: embed.SettingDomainDrawerExpanded,
		},
		{
			ID:          "sidebar_expanded",
			Label:       "Rail expanded on start",
			Description: "Start the icon rail expanded (icon + label) instead of icon-only",
			Path:        []string{"tui", "sidebar_expanded"},
			Control:     ControlBool,
			Read: func(lc *config.LayeredConfig) string {
				t := layoutTUI(lc)
				return strconv.FormatBool(t != nil && t.SidebarExpanded)
			},
			ApplyDomain: embed.SettingDomainSidebarExpanded,
		},
		{
			ID:          "show_home_on_startup",
			Label:       "Show Home on startup",
			Description: "Open the Home panel when treemux starts (writes tui.hide_splash_on_startup, inverted)",
			Path:        []string{"tui", "hide_splash_on_startup"},
			Control:     ControlBool,
			Read: func(lc *config.LayeredConfig) string {
				t := layoutTUI(lc)
				hide := t != nil && t.HideSplashOnStartup
				return strconv.FormatBool(!hide)
			},
			WriteTransform: negateBool,
			// Startup-only: no live-apply seam (Phase 2 decision).
			ApplyDomain: "",
		},
	}
}

// negateBool inverts a typed bool value on its way to disk — the write-side
// half of an inverted-presentation row (Read shows the negation, so the
// persisted key keeps its schema meaning). Non-bool values pass through.
func negateBool(v interface{}) interface{} {
	if b, ok := v.(bool); ok {
		return !b
	}
	return v
}
