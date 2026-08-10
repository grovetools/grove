package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/pkg/coderoot"
)

func runSubscribeCmd(t *testing.T, name, path, notebook string, disabled bool) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runSubscribe(&out, name, path, notebook, disabled)
	return out.String(), err
}

func loadSandboxRoots(t *testing.T, configDir string) coderoot.Table {
	t.Helper()
	table, err := coderoot.LoadFrom(filepath.Join(configDir, coderoot.RootsFileName), filepath.Join(configDir, coderoot.NotebooksFileName))
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func TestSubscribeWritesIntentOnly(t *testing.T) {
	home, configDir, _ := sandboxAdoption(t)
	dest := filepath.Join(home, "code", "grovetools")
	out, err := runSubscribeCmd(t, "grovetools", dest, "", false)
	if err != nil {
		t.Fatalf("subscribe: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("subscribe created %s", dest)
	}
	if !strings.Contains(out, "grove ecosystem materialize grovetools") {
		t.Errorf("missing materialize hint: %s", out)
	}
	if got := loadSandboxRoots(t, configDir).Roots["grovetools"].Path; got != dest {
		t.Errorf("path=%q want %q", got, dest)
	}
}

func TestSubscribeWritesPartialMemberIntent(t *testing.T) {
	_, configDir, _ := sandboxAdoption(t)
	var out bytes.Buffer
	if err := runSubscribeWithFilters(&out, "grovetools", "/code/grovetools", "", false, []string{"core", "nav", "core"}, nil); err != nil {
		t.Fatal(err)
	}
	got := loadSandboxRoots(t, configDir).Roots["grovetools"].Repos
	if strings.Join(got, ",") != "core,nav" {
		t.Fatalf("repos=%v", got)
	}
	if err := runSubscribeWithFilters(&out, "grovetools", "/code/grovetools", "", false, []string{"core"}, []string{"nav"}); err == nil {
		t.Fatal("accepted include and exclude")
	}
}

func TestSubscribePreservesTheRestOfRootsToml(t *testing.T) {
	home, configDir, _ := sandboxAdoption(t)
	original := "# my roots\n[roots.chickens]\npath = \"~/code/chickens\"\nscan = true\n"
	if err := os.WriteFile(filepath.Join(configDir, coderoot.NotebooksFileName), []byte("default = \"nb\"\n[notebooks.nb]\nroot = \"~/notebooks/nb\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rootsPath := filepath.Join(configDir, coderoot.RootsFileName)
	if err := os.WriteFile(rootsPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runSubscribeCmd(t, "grovetools", filepath.Join(home, "code", "grovetools"), "", false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	data, _ := os.ReadFile(rootsPath)
	text := string(data)
	for _, want := range []string{"# my roots", "[roots.chickens]", "scan = true", "[roots.grovetools]"} {
		if !strings.Contains(text, want) {
			t.Errorf("lost %q:\n%s", want, text)
		}
	}
}

func TestSubscribeIsIdempotent(t *testing.T) {
	home, configDir, _ := sandboxAdoption(t)
	dest := filepath.Join(home, "code", "grovetools")
	if out, err := runSubscribeCmd(t, "grovetools", dest, "", false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	path := filepath.Join(configDir, coderoot.RootsFileName)
	before, _ := os.ReadFile(path)
	out, err := runSubscribeCmd(t, "grovetools", dest, "", false)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Errorf("second subscribe rewrote roots.toml")
	}
	if !strings.Contains(out, "already declared") {
		t.Errorf("missing idempotence output: %s", out)
	}
}

func TestSubscribeDefaultsBesideExistingEcosystems(t *testing.T) {
	home, configDir, _ := sandboxAdoption(t)
	first := filepath.Join(home, "src", "alpha")
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, coderoot.NotebooksFileName), []byte("default = \"nb\"\n[notebooks.nb]\nroot = \"~/notebooks/nb\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, coderoot.RootsFileName), []byte("[roots.alpha]\npath = \""+first+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runSubscribeCmd(t, "beta", "", "", false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := filepath.Join(home, "src", "beta")
	if got := loadSandboxRoots(t, configDir).Roots["beta"].Path; got != want {
		t.Errorf("beta=%q want %q", got, want)
	}
}

func TestSubscribeNoticesAnExistingCheckout(t *testing.T) {
	home, _, _ := sandboxAdoption(t)
	dest := filepath.Join(home, "code", "grovetools")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "grove.toml"), []byte("name = \"grovetools\"\nworkspaces = [\"*\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runSubscribeCmd(t, "grovetools", dest, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "present on disk") || strings.Contains(out, "materialize") {
		t.Errorf("bad output: %s", out)
	}
}
