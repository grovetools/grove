package env

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/tui/theme"
)

// worktreeItem adapts a WorktreeState into an OverlayItem so the reusable
// overlay component can render the W-key worktree picker. The row is cheap
// to recompute on every View call, so we don't cache any rendered strings.
type worktreeItem struct {
	name     string
	path     string
	profile  string
	state    string
	provider string
	glyph    string
	glyphKey string // one of: running, cloud, drift, inactive
}

func newWorktreeItem(state WorktreeState, deployedProfile string) worktreeItem {
	it := worktreeItem{
		name:    state.Workspace.Name,
		path:    state.Workspace.Path,
		profile: deployedProfile,
	}
	if state.EnvState != nil {
		if state.EnvState.Provider != "" {
			it.provider = state.EnvState.Provider
		}
		it.state = worktreeStateSummary(state)
	}
	it.glyph, it.glyphKey = worktreeGlyph(state)
	if it.state == "" {
		it.state = "inactive"
	}
	return it
}

// worktreeStateSummary produces the short "● running" / "☁ applied" /
// "◯ inactive" label shown in the worktree picker subtitle and the
// Deployments state column. state.json's presence is the strongest signal
// for "local running"; terraform-backed profiles with persisted state but
// no local services still count as "☁ applied".
func worktreeStateSummary(state WorktreeState) string {
	if state.EnvState == nil {
		return "inactive"
	}
	running := len(state.EnvState.Services) > 0 || len(state.EnvState.Endpoints) > 0
	cloud := state.EnvState.Provider == "terraform"
	switch {
	case running && cloud:
		return "● running · ☁ applied"
	case running:
		return "● running"
	case cloud:
		return "☁ applied"
	default:
		return "inactive"
	}
}

func (w worktreeItem) Key() string   { return w.name }
func (w worktreeItem) Label() string { return w.name }
func (w worktreeItem) Glyph() string { return w.glyph }
func (w worktreeItem) GlyphStyle() lipgloss.Style {
	th := theme.DefaultTheme
	switch w.glyphKey {
	case "running":
		return th.Success
	case "cloud":
		return th.Info
	case "drift":
		return th.Warning
	case "inactive":
		return th.Muted
	default:
		return th.Muted
	}
}

func (w worktreeItem) Subtitle() string {
	if w.profile == "" {
		return w.state
	}
	return w.profile + " · " + w.state
}

func (w worktreeItem) Provider() string {
	if w.provider == "" {
		return "—"
	}
	return w.provider
}

// rowActionItem is the OverlayItem shape used by the Enter-on-a-row overlay
// on the Deployments page. Unlike worktreeItem, it represents a single
// action the user can take against a worktree (jump, drift, open endpoint,
// diff), not the worktree itself — and so its Provider() slot shows the
// numeric shortcut ("1".."4") instead of a provider name.
type rowActionItem struct {
	key   string
	label string
	desc  string
	num   int
}

func newRowActionItem(key, label, desc string, num int) rowActionItem {
	return rowActionItem{key: key, label: label, desc: desc, num: num}
}

func (r rowActionItem) Key() string                { return r.key }
func (r rowActionItem) Glyph() string              { return "" }
func (r rowActionItem) GlyphStyle() lipgloss.Style { return theme.DefaultTheme.Muted }
func (r rowActionItem) Label() string              { return r.label }
func (r rowActionItem) Subtitle() string           { return r.desc }
func (r rowActionItem) Provider() string           { return fmt.Sprintf("%d", r.num) }

// worktreeGlyph picks the single-cell glyph + color key for a worktree row,
// matching the mockup's conventions:
//
//	⚡ running  — local env up and terraform/cloud applied
//	☁ cloud    — cloud state only (no local process)
//	⚠ drift    — drift detected (or orphaned state)
//	◯ inactive — nothing running, no cloud state
//
// The mapping prefers the live state.json over the drift cache so a running
// env with pending drift still reads as ⚡ rather than ⚠ (matching the
// Deployments mockup where the drift column carries that signal).
func worktreeGlyph(state WorktreeState) (string, string) {
	running := state.EnvState != nil && len(state.EnvState.State) > 0
	cloudState := state.EnvState != nil && state.EnvState.Provider == "terraform"
	switch {
	case running && cloudState:
		return "⚡", "running"
	case running:
		return "⚡", "running"
	case cloudState:
		return "☁", "cloud"
	case state.Drift != nil && state.Drift.HasDrift:
		return "⚠", "drift"
	default:
		return "◯", "inactive"
	}
}
