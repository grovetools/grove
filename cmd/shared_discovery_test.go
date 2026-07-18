package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverTargetProjects_UnclassifiableDirFailsClosed(t *testing.T) {
	// An empty temp dir has no workspace markers anywhere up its tree. The
	// discovery must fail closed with an actionable error — never fall back
	// to the machine-wide project set.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, ".config"))
	chdir(t, tmpDir)

	projects, _, err := DiscoverTargetProjects()
	require.Error(t, err, "unclassifiable directory must fail closed")
	assert.Nil(t, projects, "no projects may be returned when context is unknown")
	assert.Contains(t, err.Error(), "cannot determine grove workspace context")
}

func TestDiscoverTargetProjects_MangledContainerConfigFailsClosed(t *testing.T) {
	// A worktree-container-like directory: one-line grove.toml that is
	// unparseable and no top-level .git. The container must NOT be silently
	// demoted to an unclassifiable dir (triggering system-wide fan-out); the
	// error must surface the broken file.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, ".config"))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "grove.toml"), []byte("workspaces = [\"*\"\n"), 0o644))
	chdir(t, tmpDir)

	projects, _, err := DiscoverTargetProjects()
	require.Error(t, err, "mangled grove.toml must fail closed")
	assert.Nil(t, projects, "no projects may be returned when the container config is broken")
	assert.Contains(t, err.Error(), "grove.toml", "error must name the broken config file")
}
