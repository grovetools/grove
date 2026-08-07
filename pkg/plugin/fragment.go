package plugin

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// The "Declare" stage of the pipeline. core/config globs
// ~/.config/grove/plugins/*.toml and merges every match into the GLOBAL config
// layer, which is the layer [tui.plugins] is allowed to come from
// (x-layer=global). The installer writes one fragment per plugin there, and
// treemux's config_reload picks it up without a restart.
//
// One plugin, one file: an uninstall is then a file removal, and a user who
// wants to disable a plugin by hand can move one file out of the way.

// RenderFragment builds the [tui.plugins.<name>] fragment for an installed
// plugin. runBinary is the absolute path of the installed binary rather than
// its bare name: treemux spawns it directly, and depending on the user's PATH
// containing grove's bin dir would make "it works in my shell" and "the panel
// starts" two different questions.
func RenderFragment(m *Manifest, runBinary string, pin *Pin) ([]byte, error) {
	panel := map[string]any{"command": runBinary}
	if len(m.Panel.Args) > 0 {
		panel["args"] = m.Panel.Args
	}
	if m.Panel.Icon != "" {
		panel["icon"] = m.Panel.Icon
	}
	if m.Panel.Label != "" {
		panel["label"] = m.Panel.Label
	}
	if len(m.Panel.Env) > 0 {
		panel["env"] = m.Panel.Env
	}
	if m.Panel.Restart {
		panel["restart"] = true
	}
	if m.Panel.Protocol != "" {
		panel["protocol"] = m.Panel.Protocol
	}
	if m.Panel.ProtocolTimeout != "" {
		panel["protocol_timeout"] = m.Panel.ProtocolTimeout
	}
	// The manifest's key declaration travels into the config so the host can
	// compare a running sidecar's claims against the list the user approved,
	// and so `treemux keys` can describe a panel without performing a
	// handshake. It grants nothing — the host arbitrates the real claims.
	if len(m.Panel.Keys) > 0 {
		keys := make([]map[string]any, 0, len(m.Panel.Keys))
		for _, k := range m.Panel.Keys {
			keys = append(keys, map[string]any{"key": k.Key, "description": k.Description})
		}
		panel["keys"] = keys
	}
	// The view declaration travels into the config as an ARRAY, which is where
	// the author's declaration order stops being implicit: `[panel.views.<name>]`
	// is a map once decoded, and the host's whole use of `drawer` is "the FIRST
	// view declared suitable". Freezing the order here is what lets the host read
	// a preference without holding a view vocabulary of its own.
	//
	// `drawer` is written even when false, because false is a statement the
	// author made — this layout is not for a narrow pane — and the host reports
	// on it.
	if names := m.Panel.ViewNames(); len(names) > 0 {
		views := make([]map[string]any, 0, len(names))
		for _, name := range names {
			v := m.Panel.Views[name]
			views = append(views, map[string]any{
				"name":        name,
				"description": v.Description,
				"drawer":      v.Drawer,
			})
		}
		panel["views"] = views
	}
	// The notebook declaration travels into the config for the reason keys do:
	// the file the user edits should repeat what the consent screen said the
	// panel writes into their notebook. It grants nothing and no host reads
	// it — the subtree is never resolved, created or fenced — it is the
	// consent screen's claim, kept where the user can find it again.
	if m.Panel.Notebook != nil {
		panel["notebook"] = map[string]any{
			"subtree":     m.Panel.Notebook.Subtree,
			"description": m.Panel.Notebook.Description,
		}
	}
	// The digest declaration travels for the reason the notebook one does, plus
	// one of its own: this is the file a user edits to point a drawer pane at the
	// panel (`backend = "digest"`), and the sentence they need in order to decide
	// whether that pane is worth a column is the author's, written right here.
	// Nothing reads it — a host draws the LIVE frame — so writing it is the whole
	// of what it does.
	if m.Panel.Digest != nil {
		panel["digest"] = map[string]any{"description": m.Panel.Digest.Description}
	}
	// Settings are the panel's defaults, written out so the file the user edits
	// to retune the panel already shows every knob it has. Editing this table
	// is the supported way to configure an installed panel; the host delivers
	// it verbatim and re-delivers it on config_reload.
	if len(m.Panel.Settings) > 0 {
		panel["settings"] = m.Panel.Settings
	}

	// Marshal the FULLY NESTED document and delete the two empty parent
	// headers afterwards, rather than marshalling the panel table alone under
	// a hand-written "[tui.plugins.<name>]" line.
	//
	// The hand-written prefix worked only while every value was a scalar. A
	// nested table marshals to its own header — `[settings]`, `[[keys]]` —
	// which is relative to the document root, not to whatever header a caller
	// pasted above it. Prefixing produced a file where `settings` and `keys`
	// were siblings of `[tui]` instead of children of the panel: valid TOML
	// that silently meant something else, and the reason a panel's settings
	// arrived at the host empty.
	//
	// A plugin name is constrained to a TOML bare key by Validate, so the
	// headers this produces need no quoting and the two we strip are exact.
	doc := map[string]any{"tui": map[string]any{"plugins": map[string]any{m.Plugin.Name: panel}}}
	body, err := toml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("render the manifest fragment: %w", err)
	}
	body = stripEmptyParentHeaders(body)

	var header strings.Builder
	header.WriteString("# Managed by `grove plugin` — do not edit.\n")
	header.WriteString("#\n")
	fmt.Fprintf(&header, "# plugin:  %s — %s\n", m.Plugin.Name, m.Plugin.Description)
	fmt.Fprintf(&header, "# source:  %s\n", pin.Spec)
	if pin.Dev {
		// Saying "pinned" here would be false, and this header is the only
		// place a user looking at their config finds out why the panel keeps
		// changing under them.
		header.WriteString("# mode:    DEVELOPMENT — built in place from the working tree above.\n")
		header.WriteString("#          Nothing is pinned: `grove plugin update` rebuilds whatever\n")
		header.WriteString("#          that directory contains at the time.\n")
		if pin.Commit != "" {
			fmt.Fprintf(&header, "# head:    %s (at install time, for the record only)\n", pin.Commit)
		}
	} else {
		fmt.Fprintf(&header, "# pinned:  %s\n", pin.Commit)
	}
	if m.Plugin.Homepage != "" {
		fmt.Fprintf(&header, "# home:    %s\n", m.Plugin.Homepage)
	}
	header.WriteString("#\n")
	fmt.Fprintf(&header, "# Change it with `grove plugin update %s`; remove it with `grove plugin remove %s`.\n\n", m.Plugin.Name, m.Plugin.Name)

	return append([]byte(header.String()), body...), nil
}

// stripEmptyParentHeaders removes the contentless "[tui]" and "[tui.plugins]"
// lines the nested encoding emits above the panel's own table.
//
// Purely cosmetic, and only safe because they are contentless: the encoder
// emits a header per level, and these two carry no keys of their own. This
// file is meant to be read by the user who is about to edit its settings, so
// two ceremonial lines at the top are worth removing — but not at the cost of
// hand-assembling the document, which is what broke nesting in the first place.
func stripEmptyParentHeaders(body []byte) []byte {
	for _, header := range [][]byte{[]byte("[tui]\n"), []byte("[tui.plugins]\n")} {
		if bytes.HasPrefix(body, header) {
			body = body[len(header):]
		}
	}
	return body
}

// WriteFragment writes the fragment for a plugin, creating the drop-in
// directory if this is the first install.
func WriteFragment(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
