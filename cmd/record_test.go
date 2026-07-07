package cmd

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/pkg/mux"
)

func fakePtyServer(t *testing.T, ptys []mux.PtyInfo) *mux.RecordClient {
	t.Helper()
	// macOS caps unix socket paths at ~104 bytes; t.TempDir() is too long.
	dir, err := os.MkdirTemp("/tmp", "grrec")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	h := http.NewServeMux()
	h.HandleFunc("/api/pty/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ptys)
	})
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return mux.NewRecordClient(sock)
}

func TestSelectRecordTarget_SolePane(t *testing.T) {
	client := fakePtyServer(t, []mux.PtyInfo{{ID: "pty-1", PID: 1}})
	target, err := selectRecordTarget(client, "")
	if err != nil {
		t.Fatalf("selectRecordTarget: %v", err)
	}
	if target.ID != "pty-1" {
		t.Errorf("got %q, want pty-1", target.ID)
	}
}

func TestSelectRecordTarget_ExplicitByTag(t *testing.T) {
	client := fakePtyServer(t, []mux.PtyInfo{
		{ID: "pty-1", PID: 1, Tags: map[string]string{"role": "editor"}},
		{ID: "pty-2", PID: 2, Tags: map[string]string{"role": "shell"}},
	})
	target, err := selectRecordTarget(client, "shell")
	if err != nil {
		t.Fatalf("selectRecordTarget: %v", err)
	}
	if target.ID != "pty-2" {
		t.Errorf("got %q, want pty-2", target.ID)
	}
}

func TestSelectRecordTarget_MultipleNoHintErrors(t *testing.T) {
	client := fakePtyServer(t, []mux.PtyInfo{
		{ID: "pty-1", PID: 1, ForegroundProcess: "nvim"},
		{ID: "pty-2", PID: 2, ForegroundProcess: "bash"},
	})
	// No --pane, and FocusedPTY cannot resolve (no dispatcher model on the fake
	// server), so the caller must be told to pick — with the pane list surfaced.
	_, err := selectRecordTarget(client, "")
	if err == nil {
		t.Fatal("expected error for ambiguous target")
	}
	if !strings.Contains(err.Error(), "pty-1") || !strings.Contains(err.Error(), "--pane") {
		t.Errorf("error should list panes and mention --pane, got: %v", err)
	}
}

func TestSelectRecordTarget_NoMatchErrors(t *testing.T) {
	client := fakePtyServer(t, []mux.PtyInfo{{ID: "pty-1", PID: 1}})
	if _, err := selectRecordTarget(client, "nope"); err == nil {
		t.Fatal("expected error when --pane matches nothing")
	}
}

func TestSelectRecordTarget_NoPanesErrors(t *testing.T) {
	client := fakePtyServer(t, []mux.PtyInfo{})
	if _, err := selectRecordTarget(client, ""); err == nil {
		t.Fatal("expected error when no panes exist")
	}
}

func TestPaneMatches(t *testing.T) {
	p := mux.PtyInfo{ID: "pty-9", Label: "demo", Tags: map[string]string{"role": "editor"}}
	for _, sel := range []string{"pty-9", "demo", "role", "editor", "role=editor"} {
		if !paneMatches(p, sel) {
			t.Errorf("paneMatches(%q) = false, want true", sel)
		}
	}
	for _, sel := range []string{"pty-1", "shell", "role=shell"} {
		if paneMatches(p, sel) {
			t.Errorf("paneMatches(%q) = true, want false", sel)
		}
	}
}

func TestResolveRecordOutput(t *testing.T) {
	tmp := t.TempDir()

	// Explicit .cast file — used verbatim (abs), not under docgen/asciicasts.
	castFile := filepath.Join(tmp, "sub", "demo.cast")
	abs, under, err := resolveRecordOutput("ignored", castFile, "")
	if err != nil {
		t.Fatalf("resolveRecordOutput file: %v", err)
	}
	if abs != castFile {
		t.Errorf("abs = %q, want %q", abs, castFile)
	}
	if under {
		t.Error("plain tmp path should not be flagged under docgen/asciicasts")
	}

	// Explicit directory — name.cast appended.
	dir := filepath.Join(tmp, "outdir")
	abs, _, err = resolveRecordOutput("nav-demo", dir, "")
	if err != nil {
		t.Fatalf("resolveRecordOutput dir: %v", err)
	}
	if want := filepath.Join(dir, "nav-demo.cast"); abs != want {
		t.Errorf("abs = %q, want %q", abs, want)
	}

	// A docgen/**/asciicasts destination is flagged for the snippet.
	docgenDir := filepath.Join(tmp, "workspaces", "nav", "docgen", "asciicasts")
	_, under, err = resolveRecordOutput("nav-demo", docgenDir, "")
	if err != nil {
		t.Fatalf("resolveRecordOutput docgen: %v", err)
	}
	if !under {
		t.Error("docgen/asciicasts destination should be flagged as under docgen/asciicasts")
	}
}

func TestIsUnderDocgenAsciicasts(t *testing.T) {
	yes := []string{
		"/n/workspaces/nav/docgen/asciicasts/x.cast",
		"/repo/.notebook/docgen/section/asciicasts/y.cast",
	}
	no := []string{
		"/tmp/x.cast",
		"/repo/asciicasts/x.cast",   // no docgen ancestor
		"/repo/docgen/casts/x.cast", // not an asciicasts dir
	}
	for _, p := range yes {
		if !isUnderDocgenAsciicasts(p) {
			t.Errorf("isUnderDocgenAsciicasts(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if isUnderDocgenAsciicasts(p) {
			t.Errorf("isUnderDocgenAsciicasts(%q) = true, want false", p)
		}
	}
}

func TestWebsiteSnippet(t *testing.T) {
	got := websiteSnippet("nav-demo.cast")
	want := "```asciinema\n{ \"src\": \"./asciicasts/nav-demo.cast\", \"autoPlay\": true, \"loop\": true }\n```"
	if got != want {
		t.Errorf("websiteSnippet mismatch:\n got: %q\nwant: %q", got, want)
	}
}
