package plugin

import (
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

	// Marshal the panel's keys and write the table header by hand: encoding
	// the whole nested map would emit empty [tui] and [tui.plugins] headers
	// above it, and this file is meant to be read. A plugin name is
	// constrained to a TOML bare key by Validate, so the header needs no
	// quoting.
	body, err := toml.Marshal(panel)
	if err != nil {
		return nil, fmt.Errorf("render the manifest fragment: %w", err)
	}
	body = append([]byte("[tui.plugins."+m.Plugin.Name+"]\n"), body...)

	var header strings.Builder
	header.WriteString("# Managed by `grove plugin` — do not edit.\n")
	header.WriteString("#\n")
	fmt.Fprintf(&header, "# plugin:  %s — %s\n", m.Plugin.Name, m.Plugin.Description)
	fmt.Fprintf(&header, "# source:  %s\n", pin.Spec)
	fmt.Fprintf(&header, "# pinned:  %s\n", pin.Commit)
	if m.Plugin.Homepage != "" {
		fmt.Fprintf(&header, "# home:    %s\n", m.Plugin.Homepage)
	}
	header.WriteString("#\n")
	fmt.Fprintf(&header, "# Change it with `grove plugin update %s`; remove it with `grove plugin remove %s`.\n\n", m.Plugin.Name, m.Plugin.Name)

	return append([]byte(header.String()), body...), nil
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
