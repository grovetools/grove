package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCapsulePATH(t *testing.T) {
	sep := string(os.PathListSeparator)
	join := func(parts ...string) string { return strings.Join(parts, sep) }

	tests := []struct {
		name    string
		current string
		prepend []string
		want    string
	}{
		{
			name:    "prepends the toolchain dir",
			current: join("/usr/bin", "/bin"),
			prepend: []string{"/grove/bin"},
			want:    join("/grove/bin", "/usr/bin", "/bin"),
		},
		{
			name:    "already first is a no-op",
			current: join("/grove/bin", "/usr/bin"),
			prepend: []string{"/grove/bin"},
			want:    join("/grove/bin", "/usr/bin"),
		},
		{
			name:    "moves a later occurrence to the front instead of duplicating",
			current: join("/usr/bin", "/grove/bin", "/bin"),
			prepend: []string{"/grove/bin"},
			want:    join("/grove/bin", "/usr/bin", "/bin"),
		},
		{
			name:    "workspace dirs keep their order and stay ahead of the toolchain",
			current: join("/usr/bin"),
			prepend: []string{"/ws/a/bin", "/ws/b/bin", "/grove/bin"},
			want:    join("/ws/a/bin", "/ws/b/bin", "/grove/bin", "/usr/bin"),
		},
		{
			name:    "repeated delegation cannot grow PATH",
			current: join("/ws/a/bin", "/grove/bin", "/usr/bin"),
			prepend: []string{"/ws/a/bin", "/grove/bin"},
			want:    join("/ws/a/bin", "/grove/bin", "/usr/bin"),
		},
		{
			name:    "nothing to prepend leaves PATH alone",
			current: join("/usr/bin"),
			prepend: nil,
			want:    join("/usr/bin"),
		},
		{
			name:    "empty prepend entries are ignored",
			current: join("/usr/bin"),
			prepend: []string{""},
			want:    join("/usr/bin"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := capsulePATH(tt.current, tt.prepend); got != tt.want {
				t.Errorf("capsulePATH(%q, %v) = %q, want %q", tt.current, tt.prepend, got, tt.want)
			}
		})
	}
}

// The capsule is what makes `grove` the only name on the global PATH workable:
// a delegated child must see the toolchain dir even when the parent's PATH has
// never heard of it.
func TestToolResolutionEnvCarriesCapsule(t *testing.T) {
	binDir := t.TempDir()
	t.Setenv("GROVE_BIN", binDir)
	t.Setenv("PATH", "/usr/bin")

	res := toolResolution{Path: filepath.Join(binDir, "treemux")}
	env := res.Env()

	var pathValue string
	found := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			pathValue = strings.TrimPrefix(entry, "PATH=")
			found = true
		}
	}
	if !found {
		t.Fatal("Env() produced no PATH entry")
	}
	if want := binDir + string(os.PathListSeparator) + "/usr/bin"; pathValue != want {
		t.Errorf("PATH = %q, want %q", pathValue, want)
	}
}

func TestToolResolutionEnvKeepsWorkspaceBinsFirst(t *testing.T) {
	binDir := t.TempDir()
	wsBin := t.TempDir()
	t.Setenv("GROVE_BIN", binDir)
	t.Setenv("PATH", "/usr/bin")

	res := toolResolution{
		Path:        filepath.Join(wsBin, "nb"),
		pathPrepend: []string{wsBin},
		extraEnv:    []string{"GROVE_WORKSPACE_ROOT=/ws"},
	}
	env := res.Env()

	var pathValue string
	sawWorkspaceRoot := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			pathValue = strings.TrimPrefix(entry, "PATH=")
		}
		if entry == "GROVE_WORKSPACE_ROOT=/ws" {
			sawWorkspaceRoot = true
		}
	}
	want := strings.Join([]string{wsBin, binDir, "/usr/bin"}, string(os.PathListSeparator))
	if pathValue != want {
		t.Errorf("PATH = %q, want %q", pathValue, want)
	}
	if !sawWorkspaceRoot {
		t.Error("Env() dropped GROVE_WORKSPACE_ROOT")
	}
}
