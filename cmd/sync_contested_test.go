package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeContestedDaemon stands up a daemon socket at the path
// paths.SocketPath() resolves to under a sandboxed GROVE_HOME, so these tests
// never look at the real machine's daemon. adopted receives the ids the verb
// asks to adopt — including, importantly, none at all.
func fakeContestedDaemon(t *testing.T, contested []contestedNotespace, adoptStatus int) *[]string {
	t.Helper()
	// macOS caps unix socket paths at ~104 bytes; t.TempDir() is too long.
	home, err := os.MkdirTemp("/tmp", "grctd")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("GROVE_HOME", home)
	runDir := filepath.Join(home, "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(runDir, "groved.sock"))
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}

	adopted := &[]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sync/contested", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Contested []contestedNotespace `json:"contested"`
		}{Contested: contested})
	})
	mux.HandleFunc("/api/sync/contested/adopt", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			NotespaceID string `json:"notespace_id"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		*adopted = append(*adopted, req.NotespaceID)
		if adoptStatus != http.StatusOK {
			http.Error(w, "notespace is not contested", adoptStatus)
			return
		}
		var target contestedNotespace
		for _, entry := range contested {
			if entry.NotespaceID == req.NotespaceID {
				target = entry
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Adopted contestedNotespace `json:"adopted"`
			Receipt string             `json:"receipt"`
		}{Adopted: target, Receipt: filepath.Join(home, "state", "sync", "adoptions", req.NotespaceID+".toml")})
	})

	srv := httptest.NewUnstartedServer(mux)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return adopted
}

func contestedFixture() contestedNotespace {
	return contestedNotespace{
		NotespaceID:  "01NSALPHA",
		Root:         "/notebooks/default/notespaces/alpha",
		Reason:       "adoption pending: 1 of 2 colliding path(s) hold un-synced local notes that differ (subject match)",
		Detail:       "hash overlap: 1/2 colliding path(s) are already byte-identical\n  differs   notes/a.md (local aaaa, server bbbb)",
		Colliding:    2,
		Identical:    1,
		Divergent:    1,
		SubjectMatch: "match",
	}
}

func TestSyncContestedRendersTheEvidence(t *testing.T) {
	fakeContestedDaemon(t, []contestedNotespace{contestedFixture()}, http.StatusOK)

	cmd := newSyncContestedCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{"01NSALPHA", "hash overlap", "1/2", "subject match  match", "notes/a.md", "grove sync adopt-notespace"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("contested output is missing %q:\n%s", want, rendered)
		}
	}
}

func TestSyncContestedSaysSoWhenNothingIsWithheld(t *testing.T) {
	fakeContestedDaemon(t, nil, http.StatusOK)
	cmd := newSyncContestedCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No contested notespaces") {
		t.Fatalf("output = %q", out.String())
	}
}

// Adoption is a decision, so the default run prints the evidence and changes
// nothing. This is the property that keeps a `grove sync adopt-notespace` typed
// out of curiosity from converging two histories.
func TestAdoptNotespaceWithoutConfirmChangesNothing(t *testing.T) {
	adopted := fakeContestedDaemon(t, []contestedNotespace{contestedFixture()}, http.StatusOK)

	var out bytes.Buffer
	if err := runSyncAdoptNotespace(context.Background(), &out, "01NSALPHA", false); err != nil {
		t.Fatal(err)
	}
	if len(*adopted) != 0 {
		t.Fatalf("a run without --confirm adopted %v", *adopted)
	}
	if !strings.Contains(out.String(), "Nothing changed") || !strings.Contains(out.String(), "--confirm") {
		t.Fatalf("output does not say it changed nothing:\n%s", out.String())
	}
}

func TestAdoptNotespaceWithConfirmAdoptsExactlyTheNamedNotespace(t *testing.T) {
	adopted := fakeContestedDaemon(t, []contestedNotespace{contestedFixture()}, http.StatusOK)

	var out bytes.Buffer
	if err := runSyncAdoptNotespace(context.Background(), &out, "01NSALPHA", true); err != nil {
		t.Fatal(err)
	}
	if len(*adopted) != 1 || (*adopted)[0] != "01NSALPHA" {
		t.Fatalf("adopted = %v, want exactly [01NSALPHA]", *adopted)
	}
	if !strings.Contains(out.String(), "adopted") || !strings.Contains(out.String(), "receipt") {
		t.Fatalf("adoption printed no evidence:\n%s", out.String())
	}
}

// A notespace that is not contested is refused locally, naming what IS — the
// verb never falls back to "the only one".
func TestAdoptNotespaceRefusesAnUncontestedID(t *testing.T) {
	adopted := fakeContestedDaemon(t, []contestedNotespace{contestedFixture()}, http.StatusOK)

	var out bytes.Buffer
	err := runSyncAdoptNotespace(context.Background(), &out, "01NSOTHER", true)
	if err == nil {
		t.Fatal("adopting an uncontested notespace was accepted")
	}
	if !strings.Contains(err.Error(), "01NSALPHA") {
		t.Fatalf("the refusal does not name what is contested: %v", err)
	}
	if len(*adopted) != 0 {
		t.Fatalf("the daemon was asked to adopt %v despite the local refusal", *adopted)
	}
}

func TestAdoptNotespaceSurfacesADaemonRefusal(t *testing.T) {
	fakeContestedDaemon(t, []contestedNotespace{contestedFixture()}, http.StatusConflict)
	var out bytes.Buffer
	err := runSyncAdoptNotespace(context.Background(), &out, "01NSALPHA", true)
	if err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("a daemon refusal was not surfaced: %v", err)
	}
}
