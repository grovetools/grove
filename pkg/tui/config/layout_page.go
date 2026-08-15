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
const (
	defaultDrawerOrientation = "right"
	defaultRailShortcuts     = "auto"
)

// layoutTUI returns the merged [tui] section, or nil when absent.
func layoutTUI(lc *config.LayeredConfig) *config.TUIConfig {
	if lc != nil && lc.Final != nil {
		return lc.Final.TUI
	}
	return nil
}

// drawerViews returns the merged [tui.drawer] block, or nil when absent. The
// three page-set-wide booleans are read back through the config package's own
// accessors rather than by dereferencing, so an unset key shows the value the
// drawer actually uses — responsive is on by default, the other two off — and
// the default lives in exactly one place.
func drawerViews(lc *config.LayeredConfig) *config.DrawerViewsConfig {
	if t := layoutTUI(lc); t != nil {
		return t.Drawer
	}
	return nil
}

// LayoutSettings returns the Layout page's setting descriptors: the active
// sessions drawer (orientation, expanded-on-start, and the three page-set-wide
// [tui.drawer] booleans), the icon rail (expanded-on-start plus its shortcut
// footer), and Home-on-startup. Everything but Home-on-startup hot-applies
// through the treemux SettingAppliedMsg handler; that one is startup-only (no
// apply domain).
//
// The [tui.drawer] booleans are here rather than only in the Data page's raw
// key tree because they are the drawer's SHAPE — how its tab bar reads and
// which pages it lists — which is the question a user arrives at this page
// with. They were reachable only by hand-editing TOML, which is how a user ends
// up staring at a strip of glyphs not knowing the labelled form exists.
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
			ID:          "drawer_page_map_long_form",
			Label:       "Drawer tabs show names",
			Description: "Label every drawer page in the tab bar with its name and jump key, wrapping over more rows, instead of the compact strip of icons",
			Path:        []string{"tui", "drawer", "page_map_long_form"},
			Control:     ControlBool,
			Read: func(lc *config.LayeredConfig) string {
				return strconv.FormatBool(drawerViews(lc).LongFormPageMap())
			},
			ApplyDomain: embed.SettingDomainDrawerViews,
		},
		{
			ID:          "drawer_hide_inapplicable_pages",
			Label:       "Hide unusable drawer pages",
			Description: "Drop a drawer page whose subject is absent (an agent page with no agent focused) from the tab bar instead of dimming it; the page you are on is never hidden",
			Path:        []string{"tui", "drawer", "hide_inapplicable_pages"},
			Control:     ControlBool,
			Read: func(lc *config.LayeredConfig) string {
				return strconv.FormatBool(drawerViews(lc).HideInapplicableDrawerPages())
			},
			ApplyDomain: embed.SettingDomainDrawerViews,
		},
		{
			ID:          "drawer_responsive",
			Label:       "Drawer panes yield empty rows",
			Description: "Let a drawer pane with nothing to show shrink to its heading and hand the rows it cannot use to a pane on the same page that has content",
			Path:        []string{"tui", "drawer", "responsive"},
			Control:     ControlBool,
			Read: func(lc *config.LayeredConfig) string {
				return strconv.FormatBool(drawerViews(lc).ResponsiveDrawer())
			},
			ApplyDomain: embed.SettingDomainDrawerViews,
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
			ID:          "rail_shortcuts",
			Label:       "Rail workspace shortcuts",
			Description: "When the expanded rail pins its workspace-shortcut footer: auto (yield rows to the pane list on a short rail), always, or never",
			Path:        []string{"tui", "rail", "shortcuts"},
			Control:     ControlSelect,
			Options:     []string{"auto", "always", "never"},
			Read: func(lc *config.LayeredConfig) string {
				if t := layoutTUI(lc); t != nil && t.Rail != nil && t.Rail.Shortcuts != "" {
					return t.Rail.Shortcuts
				}
				return defaultRailShortcuts
			},
			ApplyDomain: embed.SettingDomainRail,
		},
		{
			ID:          "rail_max_shortcuts",
			Label:       "Rail shortcut limit",
			Description: "Maximum workspace shortcuts the rail footer lists (0 = all); the remainder shows as a +N count on the divider",
			Path:        []string{"tui", "rail", "max_shortcuts"},
			Control:     ControlInt,
			Read: func(lc *config.LayeredConfig) string {
				if t := layoutTUI(lc); t != nil && t.Rail != nil {
					return strconv.Itoa(t.Rail.MaxShortcuts)
				}
				return "0"
			},
			ApplyDomain: embed.SettingDomainRail,
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
