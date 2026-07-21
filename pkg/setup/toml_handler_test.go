package setup

import (
	"path/filepath"
	"testing"
)

func TestGlobalTOMLConfigPathUsesCoreResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("XDG_CONFIG_HOME", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("GROVE_HOME", "")
		t.Setenv("XDG_CONFIG_HOME", xdg)

		want := filepath.Join(xdg, "grove", "grove.toml")
		if got := GlobalTOMLConfigPath(); got != want {
			t.Fatalf("GlobalTOMLConfigPath() = %q, want %q", got, want)
		}
	})

	t.Run("GROVE_HOME takes precedence", func(t *testing.T) {
		groveHome := t.TempDir()
		t.Setenv("GROVE_HOME", groveHome)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "ignored"))

		want := filepath.Join(groveHome, "config", "grove", "grove.toml")
		if got := GlobalTOMLConfigPath(); got != want {
			t.Fatalf("GlobalTOMLConfigPath() = %q, want %q", got, want)
		}
	})

	t.Run("platform default", func(t *testing.T) {
		t.Setenv("GROVE_HOME", "")
		t.Setenv("XDG_CONFIG_HOME", "")

		want := filepath.Join(home, ".config", "grove", "grove.toml")
		if got := GlobalTOMLConfigPath(); got != want {
			t.Fatalf("GlobalTOMLConfigPath() = %q, want %q", got, want)
		}
	})
}
