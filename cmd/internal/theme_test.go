package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

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

// errWriter fails every write, standing in for the consumer's pipe closing when
// the agent session it feeds exits.
type errWriter struct{ err error }

func (w errWriter) Write([]byte) (int, error) { return 0, w.err }

// themeUpdate wraps a payload the way the daemon broadcasts a change.
func themeUpdate(t *testing.T, name string) coredaemon.StateUpdate {
	t.Helper()
	payload, ok := coredaemon.BuildThemePayload(name)
	if !ok {
		t.Fatalf("no payload for %q", name)
	}
	return coredaemon.StateUpdate{UpdateType: coredaemon.UpdateTypeThemeChanged, Payload: payload}
}

// initialUpdate wraps a payload the way every subscription opens — which is the
// frame a reconnect must NOT turn into a repaint.
func initialUpdate(t *testing.T, name string) coredaemon.StateUpdate {
	t.Helper()
	payload, ok := coredaemon.BuildThemePayload(name)
	if !ok {
		t.Fatalf("no payload for %q", name)
	}
	return coredaemon.StateUpdate{UpdateType: "initial", Theme: payload}
}

// decodeNames reads the watch stream's stdout as the newline-delimited JSON it
// promises to be, returning the theme name of each object in order.
func decodeNames(t *testing.T, out string) []string {
	t.Helper()
	var names []string
	dec := json.NewDecoder(bytes.NewReader([]byte(out)))
	for {
		var payload coredaemon.ThemeChangedPayload
		err := dec.Decode(&payload)
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatalf("watch stdout is not newline-delimited JSON (%v): %q", err, out)
		}
		names = append(names, payload.Name)
	}
}

// TestEmitThemeChangesOnlyOnChange pins the deduplication that makes the
// reconnect loop safe. Every subscription opens with an "initial" snapshot
// carrying the current theme, and an old daemon ignores the type filter and
// sends everything, so "write what arrives" would repaint a consumer on every
// daemon restart and on every unrelated frame.
func TestEmitThemeChangesOnlyOnChange(t *testing.T) {
	start, ok := coredaemon.BuildThemePayload("tokyonight-moon")
	if !ok {
		t.Fatal("no payload for tokyonight-moon")
	}
	last, err := json.Marshal(start)
	if err != nil {
		t.Fatal(err)
	}

	updates := make(chan coredaemon.StateUpdate, 8)
	// The theme we started on, arriving as a reconnect's opening snapshot.
	updates <- initialUpdate(t, "tokyonight-moon")
	// A frame with no theme in it at all.
	updates <- coredaemon.StateUpdate{UpdateType: "enrichment"}
	updates <- themeUpdate(t, "gruvbox")
	// The same change twice: the daemon rebroadcasts on config reload, and a
	// config file can be written twice with the same value.
	updates <- themeUpdate(t, "gruvbox")
	updates <- themeUpdate(t, "tokyonight-moon")
	close(updates)

	var buf bytes.Buffer
	got, err := emitThemeChanges(context.Background(), updates, json.NewEncoder(&buf), last)
	if err != nil {
		t.Fatalf("emitThemeChanges: %v", err)
	}

	names := decodeNames(t, buf.String())
	want := []string{"gruvbox", "tokyonight-moon"}
	if len(names) != len(want) {
		t.Fatalf("emitted %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("emit %d = %q, want %q", i, names[i], want[i])
		}
	}

	// The returned cursor is what the next subscription dedupes against, so a
	// reconnect right here must stay silent.
	rerun := make(chan coredaemon.StateUpdate, 1)
	rerun <- initialUpdate(t, "tokyonight-moon")
	close(rerun)
	buf.Reset()
	if _, err := emitThemeChanges(context.Background(), rerun, json.NewEncoder(&buf), got); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("reconnect repainted the consumer: %q", buf.String())
	}
}

// TestEmitThemeChangesStopsWhenTheConsumerIsGone: a failed write means the pipe
// is closed, i.e. the session that spawned this watcher is gone. Nothing is
// served by looping on, so the error propagates and the process exits nonzero.
func TestEmitThemeChangesStopsWhenTheConsumerIsGone(t *testing.T) {
	updates := make(chan coredaemon.StateUpdate, 1)
	updates <- themeUpdate(t, "gruvbox")
	close(updates)

	sentinel := errors.New("broken pipe")
	_, err := emitThemeChanges(context.Background(), updates, json.NewEncoder(errWriter{err: sentinel}), nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

// TestEmitThemeChangesHonorsCancellation keeps the watcher from outliving its
// session: pi kills the child on shutdown, and a blocked read must not swallow
// that.
func TestEmitThemeChangesHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buf bytes.Buffer
	// A live channel with a pending change: cancellation must win anyway.
	updates := make(chan coredaemon.StateUpdate, 1)
	updates <- themeUpdate(t, "gruvbox")
	if _, err := emitThemeChanges(ctx, updates, json.NewEncoder(&buf), nil); err != nil {
		t.Fatalf("canceled emit returned %v, want nil", err)
	}
}

// TestThemeWatchPinnedByEnv: GROVE_THEME pins a process tree to one theme, so
// there is nothing for a watcher to follow. It prints the one object and exits
// ZERO — the status a supervisor reads as "done", not "crashed, respawn me".
// Without this the pi extension would restart the watcher forever.
func TestThemeWatchPinnedByEnv(t *testing.T) {
	t.Setenv("GROVE_THEME", "gruvbox")

	cmd := newThemeCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--watch"})

	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("theme --watch with GROVE_THEME set: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("theme --watch with GROVE_THEME set did not exit; it must not idle on a stream it would ignore")
	}

	names := decodeNames(t, out.String())
	if len(names) != 1 || names[0] != "gruvbox" {
		t.Errorf("stdout = %q, want exactly the pinned theme", out.String())
	}
}
