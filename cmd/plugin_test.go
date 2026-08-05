package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/grovetools/grove/pkg/plugin"
)

func facts() plugin.ConsentFacts {
	return plugin.ConsentFacts{
		Name:        "hello",
		Description: "A hello-world sidecar panel",
		Source:      "github.com/user/grove-panel-hello@v1.2.0",
		Commit:      "0123456789abcdef0123456789abcdef01234567",
		Build:       []string{"go", "build", "-o", "bin/grove-panel-hello", "."},
		Run:         []string{"/home/u/.local/share/grove/bin/grove-panel-hello", "--wide"},
		Env:         []string{"HELLO_MODE=demo"},
		Protocol:    plugin.ProtocolEmbedV1,
		Keys:        []string{"ctrl+f — jump to the notebook"},
	}
}

// The consent screen is the whole security story of an install: if it does not
// show what will run, approving it means nothing.
func TestConsentScreenShowsWhatWillRun(t *testing.T) {
	var buf bytes.Buffer
	printConsent(&plugin.ConsentRequest{
		Facts:        facts(),
		FragmentPath: "/home/u/.config/grove/plugins/hello.toml",
		BinaryPath:   "/home/u/.local/share/grove/bin/grove-panel-hello",
	}, &buf)

	out := buf.String()
	for _, want := range []string{
		"go build -o bin/grove-panel-hello .",              // the build command
		"/home/u/.local/share/grove/bin/grove-panel-hello", // the run command
		"HELLO_MODE=demo",                          // the environment
		"embed/v1",                                 // the control plane it gets
		"ctrl+f — jump to the notebook",            // the key it claims
		"/home/u/.config/grove/plugins/hello.toml", // what it writes
		"0123456789abcdef",                         // the pin
		"grove config trust --list",                // where the approval lands
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the consent screen never mentions %q:\n%s", want, out)
		}
	}
}

// An update must show what changed, not just re-print the new state: the user
// already approved something, and the question is what is different.
func TestConsentScreenDiffsAnUpdate(t *testing.T) {
	previous := facts()
	previous.Commit = "aaaaaaaaaaaaaaaa"
	previous.Source = "github.com/user/grove-panel-hello@v1.1.0"
	previous.Keys = nil

	next := facts()
	var buf bytes.Buffer
	printConsent(&plugin.ConsentRequest{
		Facts:        next,
		Previous:     &previous,
		Changes:      plugin.Diff(previous, next),
		FragmentPath: "/home/u/.config/grove/plugins/hello.toml",
		BinaryPath:   "/home/u/.local/share/grove/bin/grove-panel-hello",
	}, &buf)

	out := buf.String()
	if !strings.Contains(out, "Changed since you approved github.com/user/grove-panel-hello@v1.1.0") {
		t.Errorf("an update must say what it is replacing:\n%s", out)
	}
	if !strings.Contains(out, "keys") || !strings.Contains(out, "(none)") {
		t.Errorf("a newly claimed key must show up in the diff as an addition:\n%s", out)
	}
	if !strings.Contains(out, "- github.com/user/grove-panel-hello@v1.1.0") {
		t.Errorf("the diff must show the old value it is moving from:\n%s", out)
	}
}

// A manifest key this grove does not understand is reported, not swallowed:
// the user is approving what grove will actually honor.
func TestConsentScreenReportsUnknownManifestKeys(t *testing.T) {
	var buf bytes.Buffer
	printConsent(&plugin.ConsentRequest{
		Facts:   facts(),
		Unknown: []string{"panel.sandbox"},
	}, &buf)

	if !strings.Contains(buf.String(), "panel.sandbox") {
		t.Errorf("unknown manifest keys must be shown:\n%s", buf.String())
	}
}

// The notebook declaration is a claim about the user's own data, so the
// screen renders it as a sentence naming the subtree and what lands there —
// a disclosure, not a mount: the host never resolves or enforces the path.
func TestConsentScreenNamesTheNotebookSubtree(t *testing.T) {
	f := facts()
	f.NotebookSubtree = "hn/clippings"
	f.NotebookDescription = "stories you clip from the feed"
	var buf bytes.Buffer
	printConsent(&plugin.ConsentRequest{Facts: f}, &buf)

	if !strings.Contains(buf.String(), "notebook  writes under hn/clippings/ in your notebook — stories you clip from the feed") {
		t.Errorf("the consent screen never states the notebook subtree:\n%s", buf.String())
	}
}

// A panel with no build step is a legitimate shape (a shell panel), and the
// screen has to say so rather than showing an empty command.
func TestConsentScreenNamesTheAbsenceOfABuildStep(t *testing.T) {
	f := facts()
	f.Build = nil
	var buf bytes.Buffer
	printConsent(&plugin.ConsentRequest{Facts: f}, &buf)

	if !strings.Contains(buf.String(), "no build step") {
		t.Errorf("expected the screen to say there is no build step:\n%s", buf.String())
	}
}

// --ref moves ONE pin. Applying it to every installed plugin would pin
// unrelated panels to a ref that means nothing in their repositories.
func TestUpdateRefusesARefAcrossManyPlugins(t *testing.T) {
	cmd := newPluginUpdateCmd()
	if err := cmd.Flags().Set("ref", "v2.0.0"); err != nil {
		t.Fatalf("set --ref: %v", err)
	}
	err := runPluginUpdate(cmd, []string{"one", "two"})
	if err == nil || !strings.Contains(err.Error(), "--ref moves one plugin") {
		t.Errorf("err = %v, want a refusal naming the ambiguity", err)
	}
}
