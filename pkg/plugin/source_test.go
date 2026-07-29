package plugin

import (
	"strings"
	"testing"
)

func TestParseSource(t *testing.T) {
	cases := []struct {
		spec     string
		override string
		wantURL  string
		wantRef  string
	}{
		{spec: "github.com/user/grove-panel-foo@v1.2.0", wantURL: "https://github.com/user/grove-panel-foo", wantRef: "v1.2.0"},
		{spec: "github.com/user/grove-panel-foo", wantURL: "https://github.com/user/grove-panel-foo"},
		{spec: "https://github.com/user/grove-panel-foo.git@main", wantURL: "https://github.com/user/grove-panel-foo.git", wantRef: "main"},
		{spec: "git@github.com:user/grove-panel-foo.git@v1", wantURL: "git@github.com:user/grove-panel-foo.git", wantRef: "v1"},
		{spec: "github.com/user/grove-panel-foo", override: "v2.0.0", wantURL: "https://github.com/user/grove-panel-foo", wantRef: "v2.0.0"},
		{spec: "ssh://git@example.com/x/y@abc123", wantURL: "ssh://git@example.com/x/y", wantRef: "abc123"},
	}
	for _, tc := range cases {
		t.Run(tc.spec, func(t *testing.T) {
			src, err := ParseSource(tc.spec, tc.override)
			if err != nil {
				t.Fatalf("ParseSource: %v", err)
			}
			if src.URL != tc.wantURL {
				t.Errorf("URL = %q, want %q", src.URL, tc.wantURL)
			}
			if src.Ref != tc.wantRef {
				t.Errorf("Ref = %q, want %q", src.Ref, tc.wantRef)
			}
			if src.Slug == "" || strings.ContainsAny(src.Slug, "/:@") {
				t.Errorf("Slug %q is not usable as a directory name", src.Slug)
			}
		})
	}
}

func TestParseSourceRejects(t *testing.T) {
	cases := []struct {
		name     string
		spec     string
		override string
		wantIn   string
	}{
		{"empty", "", "", "no plugin source"},
		{"not a source", "just-a-word", "", "is not a plugin source"},
		{"missing owner", "github.com/repo", "", "is not a plugin source"},
		{"conflicting refs", "github.com/u/r@v1", "v2", "two different refs"},
		{"local path that is not there", "/nonexistent/plugin/repo", "", "not a directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSource(tc.spec, tc.override)
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.wantIn)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantIn)
			}
		})
	}
}

// Two sources that read alike must not share a checkout directory.
func TestSlugsDoNotCollide(t *testing.T) {
	a, err := ParseSource("github.com/user/panel-foo", "")
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}
	b, err := ParseSource("github.com/user-panel/foo", "")
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}
	if a.Slug == b.Slug {
		t.Errorf("distinct sources share the slug %q", a.Slug)
	}
}

func TestResolvedSourceDisplay(t *testing.T) {
	src, err := ParseSource("https://github.com/user/grove-panel-foo.git@v1.2.0", "")
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}
	got := ResolvedSource{Source: src, Commit: "0123456789abcdef"}.Display()
	if got != "github.com/user/grove-panel-foo@v1.2.0" {
		t.Errorf("Display = %q", got)
	}

	src.Ref = ""
	got = ResolvedSource{Source: src, Commit: "0123456789abcdef"}.Display()
	if got != "github.com/user/grove-panel-foo@0123456789ab" {
		t.Errorf("Display without a ref = %q, want the pinned commit", got)
	}
}
