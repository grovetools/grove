package env

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
)

// profilesPage renders the ecosystem's profile catalog: every profile
// defined in grove.toml, annotated with scope (shared/per-worktree),
// provider, description, and usage count. Mirrors the "Profiles" tab of
// the mockup.
type profilesPage struct {
	cfg       *config.Config
	worktrees []WorktreeState

	width  int
	height int
}

func newProfilesPage() *profilesPage { return &profilesPage{} }

func (p *profilesPage) Name() string                            { return "Profiles" }
func (p *profilesPage) TabID() string                           { return "catalog" }
func (p *profilesPage) Init() tea.Cmd                           { return nil }
func (p *profilesPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) { return p, nil }
func (p *profilesPage) Focus() tea.Cmd                           { return nil }
func (p *profilesPage) Blur()                                    {}
func (p *profilesPage) SetSize(width, height int)                { p.width = width; p.height = height }

func (p *profilesPage) View() string {
	th := theme.DefaultTheme
	if p.cfg == nil {
		return "  " + th.Muted.Render("No grove.toml config loaded.")
	}

	names := collectProfileNames(p.cfg)
	if len(names) == 0 {
		return "  " + th.Muted.Render("No environment profiles defined in grove.toml.")
	}

	var b strings.Builder
	b.WriteString("  " + th.Muted.Render(
		"Profiles defined in grove.toml. shared profiles apply once per ecosystem; "+
			"per-worktree profiles deploy from each worktree.") + "\n\n")

	// Header row
	b.WriteString(fmt.Sprintf("  %s %s %s %s %s\n",
		th.Muted.Render(padRight("SCOPE", 14)),
		th.Muted.Render(padRight("NAME", 22)),
		th.Muted.Render(padRight("PROVIDER", 12)),
		th.Muted.Render(padRight("DESCRIPTION", 44)),
		th.Muted.Render(padRight("USAGE", 14)),
	))
	b.WriteString("  " + th.Muted.Render(strings.Repeat("─", maxInt(p.width-4, 40))) + "\n")

	for _, name := range names {
		ec := p.resolvedFor(name)
		scope := "per-worktree"
		scopeStyle := th.Success
		if config.IsSharedProfile(p.cfg, name) {
			scope = "shared"
			scopeStyle = th.Info
		}
		provider := "—"
		if ec != nil && ec.Provider != "" {
			provider = ec.Provider
		}
		desc := profileDescription(ec)
		usage := p.usageFor(name)

		b.WriteString(fmt.Sprintf("  %s %s %s %s %s\n",
			scopeStyle.Render(padRight(scope, 14)),
			th.Bold.Render(padRight(name, 22)),
			th.Muted.Render(padRight(provider, 12)),
			padRight(truncate(desc, 44), 44),
			th.Muted.Render(padRight(usage, 14)),
		))

		// "· reads <name>" sub-label when this profile consumes a shared
		// profile via `shared_env`. Rendered as its own muted line under
		// the row so the main columns stay aligned.
		if ec != nil {
			if shared, ok := ec.Config["shared_env"].(string); ok && shared != "" {
				b.WriteString("  " +
					padRight("", 14) + " " +
					padRight("", 22) + " " +
					th.Muted.Render("· reads "+shared) + "\n")
			}
		}
	}

	b.WriteString("\n  " + th.Muted.Render(
		"Heuristic: shared=true marker or shared_env pointers from other profiles. "+
			"Future: make this fully explicit in grove.toml.") + "\n")
	return b.String()
}

func (p *profilesPage) resolvedFor(name string) *config.EnvironmentConfig {
	if p.cfg == nil {
		return nil
	}
	if name == "default" {
		return p.cfg.Environment
	}
	if p.cfg.Environments != nil {
		return p.cfg.Environments[name]
	}
	return nil
}

// usageFor counts how many worktrees report this profile as the one they
// last deployed. "applied once" is the convention for shared profiles to
// mirror the mockup, which doesn't count the ecosystem-root itself as a
// separate worktree.
func (p *profilesPage) usageFor(name string) string {
	if config.IsSharedProfile(p.cfg, name) {
		return "applied once"
	}
	count := 0
	for _, w := range p.worktrees {
		if w.EnvState == nil {
			continue
		}
		if w.EnvState.Environment == name {
			count++
		}
	}
	return fmt.Sprintf("%d worktree(s)", count)
}

// collectProfileNames returns every profile defined in cfg — default env
// (if present) + every entry in Environments — sorted alphabetically.
func collectProfileNames(cfg *config.Config) []string {
	var names []string
	if cfg.Environment != nil {
		names = append(names, "default")
	}
	for name := range cfg.Environments {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// profileDescription picks a short human-readable description for a profile.
// Users rarely annotate their profiles, so we fall back to a synthetic
// "<provider> profile" string rather than leave the column blank.
//
// Names are differentiated by what's distinctive: a profile with a running
// local service (`services` map present) reads as "local API + cloud
// backing"; a pure-terraform profile reads as "all cloud"; docker and
// native fall through to compact stack descriptors. The goal is to avoid
// the collision the audit flagged where hybrid-api and terraform both
// rendered as "terraform, reads outputs from kitchen-infra".
func profileDescription(ec *config.EnvironmentConfig) string {
	if ec == nil {
		return "(no configuration)"
	}
	// `description` isn't a formal field on EnvironmentConfig, but some
	// users stash it under Config for documentation purposes.
	if d, ok := ec.Config["description"].(string); ok && d != "" {
		return d
	}
	switch ec.Provider {
	case "terraform":
		_, hasShared := ec.Config["shared_env"].(string)
		_, hasServices := ec.Config["services"].(map[string]interface{})
		switch {
		case hasShared && hasServices:
			return "local services backed by shared cloud state"
		case hasShared:
			return "cloud deployment consuming shared infra"
		case hasServices:
			return "local services with dedicated TF state"
		default:
			return "terraform-managed cloud state"
		}
	case "docker":
		if c, ok := ec.Config["compose_file"].(string); ok && c != "" {
			return "docker compose · " + c
		}
		return "docker compose stack"
	case "native":
		return "native local processes"
	default:
		if ec.Provider == "" {
			return "(no provider)"
		}
		return ec.Provider + " profile"
	}
}
