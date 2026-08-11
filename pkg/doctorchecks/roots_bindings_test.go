package doctorchecks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/pkg/doctor"
)

func writeRecordedPair(t *testing.T, roots, notebooks string) string {
	t.Helper()
	groveDir := setupScratchConfig(t)
	t.Setenv("HOME", filepath.Dir(filepath.Dir(groveDir)))
	if roots != "" {
		write(t, filepath.Join(groveDir, "roots.toml"), roots)
	}
	if notebooks != "" {
		write(t, filepath.Join(groveDir, "notebooks.toml"), notebooks)
	}
	return groveDir
}

func runRootsBindings() doctor.CheckResult {
	return (&rootsBindingsCheck{}).Run(context.Background(), doctor.RunOptions{})
}

func TestRootsBindingsInventoryAndMissingPolicy(t *testing.T) {
	tests := []struct {
		name       string
		scan       bool
		enabled    string
		makeRoot   bool
		wantStatus doctor.Status
		want       []string
	}{
		{name: "scan present", scan: true, makeRoot: true, wantStatus: doctor.StatusOK, want: []string{"kind=scan", "state=present", "expanded=", "canonical=", "notebook=\"main\"", "notebook_root="}},
		{name: "specific present", makeRoot: true, wantStatus: doctor.StatusOK, want: []string{"kind=specific", "enabled=true"}},
		{name: "enabled missing fails", wantStatus: doctor.StatusFail, want: []string{"state=missing", "absent while enabled"}},
		{name: "disabled missing is explicit", enabled: "enabled = false\n", wantStatus: doctor.StatusOK, want: []string{"enabled=false", "state=explicitly-missing"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "code")
			if tc.makeRoot {
				if err := os.MkdirAll(root, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			notes := filepath.Join(t.TempDir(), "notes")
			if err := os.MkdirAll(notes, 0o755); err != nil {
				t.Fatal(err)
			}
			scan := ""
			if tc.scan {
				scan = "scan = true\n"
			}
			writeRecordedPair(t,
				"[roots.alpha]\npath = \""+root+"\"\n"+scan+tc.enabled+"notebook = \"main\"\n",
				"default = \"main\"\n[notebooks.main]\nroot = \""+notes+"\"\n")

			res := runRootsBindings()
			if res.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s: %+v", res.Status, tc.wantStatus, res)
			}
			text := res.Message + " " + res.Error
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("result missing %q: %s", want, text)
				}
			}
		})
	}
}

func TestRootsBindingsStrictPairDiagnostics(t *testing.T) {
	tests := []struct {
		name, roots, notebooks, want string
	}{
		{name: "unknown roots key", roots: "[roots.alpha]\npath = \"/tmp/a\"\ntyop = true\n", notebooks: "default = \"main\"\n[notebooks.main]\nroot = \"/tmp/n\"\n", want: "strict mode"},
		{name: "duplicate root table", roots: "[roots.alpha]\npath = \"/tmp/a\"\n[roots.alpha]\npath = \"/tmp/b\"\n", notebooks: "default = \"main\"\n[notebooks.main]\nroot = \"/tmp/n\"\n", want: "already exists"},
		{name: "missing enabled root path", roots: "[roots.alpha]\nenabled = true\nnotebook = \"main\"\n", notebooks: "default = \"main\"\n[notebooks.main]\nroot = \"/tmp/n\"\n", want: "[roots.alpha] has no path"},
		{name: "duplicate paths", roots: "[roots.alpha]\npath = \"/tmp/a\"\nnotebook = \"main\"\n[roots.beta]\npath = \"/tmp/a\"\nnotebook = \"main\"\n", notebooks: "default = \"main\"\n[notebooks.main]\nroot = \"/tmp/n\"\n", want: "both declare path"},
		{name: "duplicate notebook table", roots: "", notebooks: "[notebooks.main]\nroot = \"/tmp/a\"\n[notebooks.main]\nroot = \"/tmp/b\"\n", want: "table main already exists"},
		{name: "unresolved root notebook", roots: "[roots.alpha]\npath = \"/tmp/a\"\nnotebook = \"missing\"\n", notebooks: "default = \"main\"\n[notebooks.main]\nroot = \"/tmp/n\"\n", want: "names no [notebooks.missing]"},
		{name: "unresolved default", roots: "", notebooks: "default = \"missing\"\n[notebooks.main]\nroot = \"/tmp/n\"\n", want: "default = \"missing\""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			writeRecordedPair(t, tc.roots, tc.notebooks)
			res := runRootsBindings()
			if res.Status != doctor.StatusFail {
				t.Fatalf("status = %s: %+v", res.Status, res)
			}
			if !strings.Contains(res.Error, tc.want) {
				t.Errorf("error missing %q: %s", tc.want, res.Error)
			}
			if !strings.Contains(res.Error, ".toml") {
				t.Errorf("error does not name recorded file: %s", res.Error)
			}
		})
	}
}

func TestRootsBindingsFollowsSymlinkedWorkspaceDirectoriesSafely(t *testing.T) {
	notes := filepath.Join(t.TempDir(), "notes")
	workspaces := filepath.Join(notes, "notespaces")
	if err := os.MkdirAll(workspaces, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "linked-workspace")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(target, "grove.toml"), "name = \"deprecated\"\n")
	if err := os.Symlink(target, filepath.Join(workspaces, "linked")); err != nil {
		t.Fatal(err)
	}
	// A cyclic directory link must not hang the inventory or turn into a
	// doctor failure; os.Stat rejects it and the fixed-depth scan moves on.
	if err := os.Symlink("cycle", filepath.Join(workspaces, "cycle")); err != nil {
		t.Fatal(err)
	}
	writeRecordedPair(t, "", "default = \"main\"\n[notebooks.main]\nroot = \""+notes+"\"\n")

	res := runRootsBindings()
	if res.Status != doctor.StatusWarn {
		t.Fatalf("status = %s: %+v", res.Status, res)
	}
	text := res.Message + " " + res.Error
	for _, want := range []string{"found 1 deprecated", filepath.Join("notespaces", "linked", "grove.toml")} {
		if !strings.Contains(text, want) {
			t.Errorf("result missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "cycle") {
		t.Errorf("cyclic workspace link leaked into inventory: %s", text)
	}
}

func TestRootsBindingsWarnsForEveryNotebookEmbeddedConfig(t *testing.T) {
	notesA := filepath.Join(t.TempDir(), "notes-a")
	notesB := filepath.Join(t.TempDir(), "notes-b")
	for _, item := range []struct{ root, workspace, file string }{
		{notesA, "alpha", "grove.toml"}, {notesB, "beta", "grove.yml"},
	} {
		dir := filepath.Join(item.root, "notespaces", item.workspace)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, item.file), "name = \"deprecated\"\n")
	}
	writeRecordedPair(t, "", "default = \"a\"\n[notebooks.a]\nroot = \""+notesA+"\"\n[notebooks.b]\nroot = \""+notesB+"\"\n")

	res := runRootsBindings()
	if res.Status != doctor.StatusWarn {
		t.Fatalf("status = %s: %+v", res.Status, res)
	}
	for _, want := range []string{"project-notebook", "grove.toml", "grove.yml", "notebook \"a\"", "notebook \"b\""} {
		if !strings.Contains(res.Message+res.Error, want) {
			t.Errorf("result missing %q: %+v", want, res)
		}
	}
}
