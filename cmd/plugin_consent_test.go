package cmd

import (
	"bytes"
	"strings"
	"testing"

	coreplugin "github.com/grovetools/core/pkg/plugin"
	"github.com/grovetools/grove/pkg/plugin"
)

// The consent screen is the product, so its two shapes are pinned here: a
// TOOL request renders the command block and the franker warning and NONE of
// the panel copy, and a PANEL request renders exactly what it always has.

func toolConsentRequest() *plugin.ConsentRequest {
	return &plugin.ConsentRequest{
		Facts: coreplugin.ConsentFacts{
			Name:        "forge",
			Description: "infrastructure tool",
			Source:      "github.com/user/grove-plugin-forge@v1.0.0",
			Commit:      "274ca8258f1149a0d5ca6d5f0f6d3a7b4c8e9f01",
			Build:       []string{"go", "build", "-o", "forge", "."},
			Run:         []string{"/opt/grove/bin/forge"},
			Kind:        "tool",
			Provides:    []string{"grove forge up", "grove forge status"},
		},
		FragmentPath: "/cfg/grove/plugins/forge.toml",
		BinaryPath:   "/opt/grove/bin/forge",
	}
}

func panelConsentRequest() *plugin.ConsentRequest {
	return &plugin.ConsentRequest{
		Facts: coreplugin.ConsentFacts{
			Name:        "demo",
			Description: "a test panel",
			Source:      "github.com/user/grove-panel-demo@v1.0.0",
			Commit:      "274ca8258f1149a0d5ca6d5f0f6d3a7b4c8e9f01",
			Build:       []string{"go", "build", "-o", "demo", "."},
			Run:         []string{"/opt/grove/bin/demo"},
			Protocol:    coreplugin.ProtocolEmbedV1,
			Keys:        []string{"ctrl+f — a claimed chord"},
			Views:       []string{"compact — one line (what a drawer pane gets by default)"},
			Settings:    []string{"work_minutes = 25"},
		},
		FragmentPath: "/cfg/grove/plugins/demo.toml",
		BinaryPath:   "/opt/grove/bin/demo",
	}
}

func TestPrintConsentToolScreen(t *testing.T) {
	var buf bytes.Buffer
	printConsent(toolConsentRequest(), &buf)
	out := buf.String()

	// The command block: each provides line rendered as the command it
	// enables, then the binary they all run.
	for _, want := range []string{
		"installs a COMMAND",
		"grove forge up",
		"grove forge status",
		"/opt/grove/bin/forge",
		// The franker warning, in the register of the dev-install one.
		"⚠",
		"credentials",
		"not sandboxed",
		"change your infrastructure",
		// The shared tail still lands.
		"/cfg/grove/plugins/forge.toml",
		"Approving covers this commit only",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("tool consent screen is missing %q:\n%s", want, out)
		}
	}

	// None of the panel copy: a tool manifest cannot declare any of it.
	for _, banned := range []string{
		"treemux will run this every time it starts",
		"The panel declares:",
		"protocol",
		"key       ",
		"view      ",
		"notebook  ",
		"Its settings start out",
		"declared set of values",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("tool consent screen renders panel copy %q:\n%s", banned, out)
		}
	}
}

func TestPrintConsentPanelScreenIsUnchanged(t *testing.T) {
	var buf bytes.Buffer
	printConsent(panelConsentRequest(), &buf)
	out := buf.String()

	for _, want := range []string{
		"treemux will run this every time it starts, as you:",
		"The panel declares:",
		"protocol  " + coreplugin.ProtocolEmbedV1,
		"ctrl+f — a claimed chord",
		"Its settings start out",
		"work_minutes = 25",
		"Approving covers this commit only",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("panel consent screen is missing %q:\n%s", want, out)
		}
	}
	for _, banned := range []string{"COMMAND", "credentials", "⚠"} {
		if strings.Contains(out, banned) {
			t.Errorf("panel consent screen picked up tool copy %q:\n%s", banned, out)
		}
	}
}

// list gains a kind everywhere a row is rendered: "panel" for every pin
// written before tools existed (their recorded kind is empty), "tool" for a
// tool pin.
func TestPluginListJSONCarriesTheKind(t *testing.T) {
	st := fixtureStatus(t)
	if got := pluginListRows([]coreplugin.Status{st})[0]["kind"]; got != "panel" {
		t.Errorf("kind = %#v, want \"panel\"", got)
	}

	st.Pin.Kind = "tool"
	st.Pin.Consent.Kind = "tool"
	st.Pin.Consent.Provides = []string{"grove forge up"}
	if got := pluginListRows([]coreplugin.Status{st})[0]["kind"]; got != "tool" {
		t.Errorf("kind = %#v, want \"tool\"", got)
	}
}

// A verb installed before a grove release that claimed the same name is
// SHADOWED, and list is where that has to be said.
func TestShadowedVerbsReportsCollisionsWithCurrentNames(t *testing.T) {
	pin := &coreplugin.Pin{
		Kind:    "tool",
		Binary:  "/opt/grove/bin/forge",
		Consent: coreplugin.ConsentFacts{Kind: "tool", Provides: []string{"grove forge up", "grove dns zones"}},
	}
	got := shadowedVerbs(pin, []string{"forge", "status"})
	if len(got) != 1 || got[0] != "forge" {
		t.Errorf("shadowedVerbs = %v, want [forge]", got)
	}
	if got := shadowedVerbs(pin, []string{"nb", "flow"}); got != nil {
		t.Errorf("unshadowed tool reported %v", got)
	}
	panelPin := &coreplugin.Pin{Binary: "/opt/grove/bin/demo"}
	if got := shadowedVerbs(panelPin, []string{"demo"}); got != nil {
		t.Errorf("a panel pin reported shadowed verbs %v", got)
	}
}

// reservedToolVerbs feeds both the installer's refusal and the shadow report,
// so it has to actually contain grove's own surface.
func TestReservedToolVerbsCoversBuiltinsAndRegistry(t *testing.T) {
	reserved := make(map[string]bool)
	for _, v := range reservedToolVerbs() {
		reserved[v] = true
	}
	// A built-in command, a registry repo name, and a registry alias that
	// differs from its repo name.
	for _, want := range []string{"plugin", "status", "daemon", "groved", "nb"} {
		if !reserved[want] {
			t.Errorf("reservedToolVerbs is missing %q", want)
		}
	}
}
