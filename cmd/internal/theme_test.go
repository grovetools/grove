package internal

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	coredaemon "github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/tui/theme"
)

// runTheme executes the plumbing verb and returns its raw stdout (the
// machine-readable contract: one JSON object, newline, nothing else).
func runTheme(t *testing.T) (string, error) {
	t.Helper()
	cmd := newThemeCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(nil)
	err := cmd.Execute()
	return buf.String(), err
}

// decodeTheme runs the verb and asserts stdout is a single well-formed
// payload, returning it for further assertions.
func decodeTheme(t *testing.T) *coredaemon.ThemeChangedPayload {
	t.Helper()
	out, err := runTheme(t)
	if err != nil {
		t.Fatalf("theme cmd: %v", err)
	}
	var payload coredaemon.ThemeChangedPayload
	dec := json.NewDecoder(bytes.NewReader([]byte(out)))
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("stdout is not valid JSON (%v): %q", err, out)
	}
	// Nothing but the one object: a consumer piping this into a JSON parser
	// must not have to strip banner lines or trailing chatter.
	if rest, err := io.ReadAll(dec.Buffered()); err != nil || len(bytes.TrimSpace(rest)) != 0 {
		t.Errorf("stdout carried more than one JSON object: %q", out)
	}
	return &payload
}

// TestThemeCmd pins the plumbing verb's contract for its non-Go consumers
// (the grove-pi theme extension shells out to it at session start).
func TestThemeCmd(t *testing.T) {
	// An explicit GROVE_THEME wins over whatever config the test machine has,
	// which is also what makes this test deterministic.
	t.Setenv("GROVE_THEME", "gruvbox")
	payload := decodeTheme(t)
	if payload.Family != "gruvbox" {
		t.Errorf("GROVE_THEME=gruvbox: family = %q, want gruvbox", payload.Family)
	}
	if payload.Mode != "hex" {
		t.Errorf("gruvbox mode = %q, want hex", payload.Mode)
	}
	if payload.Dark == nil || payload.Dark.Bg == "" || payload.Dark.Fg == "" {
		t.Errorf("gruvbox dark slot is unpopulated: %+v", payload.Dark)
	}

	// A different override moves the answer, proving the env var is actually
	// consulted per-invocation rather than baked in at package init.
	t.Setenv("GROVE_THEME", "tokyonight-moon")
	payload = decodeTheme(t)
	if payload.Name != "tokyonight-moon" {
		t.Errorf("GROVE_THEME=tokyonight-moon: name = %q", payload.Name)
	}
	if payload.Dark == nil || payload.Dark.Cyan == "" || payload.Dark.Green == "" {
		t.Errorf("tokyonight-moon dark accents unpopulated: %+v", payload.Dark)
	}

	// A stale/renamed theme name must still produce a usable payload — the
	// default's — rather than an error. A consumer with a typo in config gets
	// grove's colors, not no colors.
	t.Setenv("GROVE_THEME", "not-a-real-theme-xyz")
	payload = decodeTheme(t)
	if payload.Family == "" || payload.Dark == nil {
		t.Fatalf("unknown theme did not fall back to a payload: %+v", payload)
	}
	fallback, ok := coredaemon.BuildThemePayload(theme.DefaultThemeName)
	if !ok {
		t.Fatal("default theme has no payload")
	}
	if payload.Family != fallback.Family {
		t.Errorf("unknown theme: family = %q, want default %q", payload.Family, fallback.Family)
	}
}
