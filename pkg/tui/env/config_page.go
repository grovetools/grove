package env

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
)

// configPage renders the merged environment config for the currently
// highlighted profile, annotating each leaf with the layer that produced
// it and listing keys dropped via `_delete = true`. The data is pushed by
// the parent Model via updatePages().
type configPage struct {
	profile    string
	resolved   *config.EnvironmentConfig
	provenance map[string]string
	deleted    map[string]string
	err        error

	width  int
	height int
}

func newConfigPage() *configPage { return &configPage{} }

func (p *configPage) Name() string  { return "Config" }
func (p *configPage) TabID() string { return "config" }

func (p *configPage) Init() tea.Cmd                           { return nil }
func (p *configPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) { return p, nil }
func (p *configPage) Focus() tea.Cmd                           { return nil }
func (p *configPage) Blur()                                    {}
func (p *configPage) SetSize(width, height int)                { p.width = width; p.height = height }

func (p *configPage) View() string {
	th := theme.DefaultTheme
	if p.err != nil {
		return th.Error.Render(fmt.Sprintf("  Error: %s", p.err))
	}
	if p.resolved == nil {
		return th.Muted.Render("  Select a profile on the Overview tab to view its resolved config.")
	}

	var b strings.Builder
	header := "Config & Provenance"
	if p.profile != "" {
		header = fmt.Sprintf("Config & Provenance — %s", p.profile)
	}
	b.WriteString(th.Bold.Render("  "+header) + "\n")
	b.WriteString(th.Muted.Render("  "+strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")

	// Leading scalars live at dotted paths "provider" / "command".
	if p.resolved.Provider != "" {
		p.renderLeaf(&b, 1, "provider", p.resolved.Provider, p.provenance["provider"])
	}
	if p.resolved.Command != "" {
		p.renderLeaf(&b, 1, "command", p.resolved.Command, p.provenance["command"])
	}

	if len(p.resolved.Commands) > 0 {
		p.renderHeader(&b, 1, "commands:", "")
		keys := sortedKeys(p.resolved.Commands)
		for _, k := range keys {
			src := p.provenance["commands."+k]
			p.renderLeaf(&b, 2, k, p.resolved.Commands[k], src)
		}
	}

	if len(p.resolved.Config) > 0 {
		p.renderHeader(&b, 1, "config:", "")
		p.renderMap(&b, 2, "config", p.resolved.Config)
	}

	if len(p.deleted) > 0 {
		b.WriteString("\n" + th.Bold.Render("  Deleted keys") + "\n")
		b.WriteString(th.Muted.Render("  "+strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")
		strike := lipgloss.NewStyle().Strikethrough(true).Foreground(th.Muted.GetForeground())
		keys := sortedKeys(p.deleted)
		for _, k := range keys {
			line := fmt.Sprintf("  %s", strike.Render(k))
			p.appendAnnotated(&b, line, p.deleted[k])
		}
	}

	return b.String()
}

// renderMap walks a decoded TOML/YAML map and emits each leaf with the
// provenance label stored under "<prefix>.<path>".
func (p *configPage) renderMap(b *strings.Builder, indent int, prefix string, m map[string]interface{}) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		path := prefix + "." + k
		val := m[k]
		switch v := val.(type) {
		case map[string]interface{}:
			p.renderHeader(b, indent, k+":", p.provenance[path])
			p.renderMap(b, indent+1, path, v)
		case []interface{}:
			// Sequences are replaced wholesale by deepMergeMaps; their
			// provenance is recorded against the parent path.
			p.renderHeader(b, indent, k+":", p.provenance[path])
			for i, item := range v {
				if sub, ok := item.(map[string]interface{}); ok {
					p.renderHeader(b, indent+1, fmt.Sprintf("- [%d]", i), "")
					p.renderMap(b, indent+2, fmt.Sprintf("%s.%d", path, i), sub)
				} else {
					p.renderLeaf(b, indent+1, fmt.Sprintf("- [%d]", i), fmt.Sprint(item), "")
				}
			}
		default:
			p.renderLeaf(b, indent, k, fmt.Sprint(v), p.provenance[path])
		}
	}
}

// renderLeaf writes `<indent>key: value    [layer]` with the layer label
// right-aligned against the panel width.
func (p *configPage) renderLeaf(b *strings.Builder, indent int, key, value, source string) {
	th := theme.DefaultTheme
	pad := strings.Repeat("  ", indent)
	body := fmt.Sprintf("  %s%s %s", pad, th.Bold.Render(key+":"), th.Muted.Render(value))
	p.appendAnnotated(b, body, source)
}

// renderHeader writes `<indent>key:` (no value), optionally with an
// annotation when the key itself has provenance recorded.
func (p *configPage) renderHeader(b *strings.Builder, indent int, key, source string) {
	th := theme.DefaultTheme
	pad := strings.Repeat("  ", indent)
	body := fmt.Sprintf("  %s%s", pad, th.Bold.Render(key))
	p.appendAnnotated(b, body, source)
}

// appendAnnotated writes body to b, right-aligning the optional annotation
// against the panel width (or dropping to a single-space separator when
// the body has already filled the row).
func (p *configPage) appendAnnotated(b *strings.Builder, body, annotation string) {
	th := theme.DefaultTheme
	if annotation == "" {
		b.WriteString(body + "\n")
		return
	}
	tag := "[" + annotation + "]"
	bodyW := lipgloss.Width(body)
	tagW := lipgloss.Width(tag)
	available := p.width - bodyW - tagW - 2
	if available < 2 {
		available = 2
	}
	b.WriteString(body + strings.Repeat(" ", available) + th.Muted.Render(tag) + "\n")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
