package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverProjects_NoEcosystemFailsClosed(t *testing.T) {
	// Outside any ecosystem, ecosystem-scoped discovery must fail closed with
	// ErrNoEcosystemScope instead of returning the machine-wide project set.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, ".config"))

	oldDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(oldDir) })
	require.NoError(t, os.Chdir(tmpDir))

	projects, err := DiscoverProjects()
	require.Error(t, err, "discovery without an ecosystem scope must fail closed")
	assert.ErrorIs(t, err, ErrNoEcosystemScope)
	assert.Nil(t, projects, "the unfiltered machine-wide set must never be returned")

	projects, err = DiscoverAllProjects()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoEcosystemScope)
	assert.Nil(t, projects)
}
