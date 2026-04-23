package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/env"
)

// chdir switches the process cwd to path for the duration of the test, restoring
// it on cleanup. Used because the env-cmd helpers resolve ./.grove/env/* against
// the cwd rather than taking a base dir.
func chdir(t *testing.T, path string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(path); err != nil {
		t.Fatalf("chdir %s: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// TestResolveEnvCmdProfile_ExplicitFlag verifies --env takes priority over
// everything else — including an active env or a recorded last profile.
func TestResolveEnvCmdProfile_ExplicitFlag(t *testing.T) {
	tmp := t.TempDir()
	chdir(t, tmp)

	// Both an active env AND a sidecar exist; --env must still win.
	if err := os.MkdirAll(envStateDir(), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	state := env.EnvStateFile{Environment: "active-profile"}
	data, _ := json.Marshal(&state)
	if err := os.WriteFile(envStatePath(), data, 0644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	writeLastProfile("sidecar-profile")

	profile, note, err := resolveEnvCmdProfile("override", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile != "override" {
		t.Errorf("profile = %q, want override", profile)
	}
	if note != "" {
		t.Errorf("note = %q, want empty", note)
	}
}

// TestResolveEnvCmdProfile_ActiveEnv verifies that when no --env is passed but
// a state file reports an active env, its Environment is returned silently.
func TestResolveEnvCmdProfile_ActiveEnv(t *testing.T) {
	tmp := t.TempDir()
	chdir(t, tmp)

	if err := os.MkdirAll(envStateDir(), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	state := env.EnvStateFile{Environment: "hybrid-api"}
	data, _ := json.Marshal(&state)
	if err := os.WriteFile(envStatePath(), data, 0644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	profile, note, err := resolveEnvCmdProfile("", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile != "hybrid-api" {
		t.Errorf("profile = %q, want hybrid-api", profile)
	}
	if note != "" {
		t.Errorf("active-env resolution should not print a fallback note; got %q", note)
	}
}

// TestResolveEnvCmdProfile_LastProfileFallback is the bug-fix case: no active
// env, but a recorded last profile sidecar — env cmd must use it and surface
// a visible warning so the user knows the fallback happened.
func TestResolveEnvCmdProfile_LastProfileFallback(t *testing.T) {
	tmp := t.TempDir()
	chdir(t, tmp)

	writeLastProfile("docker-local")

	profile, note, err := resolveEnvCmdProfile("", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile != "docker-local" {
		t.Errorf("profile = %q, want docker-local", profile)
	}
	if !strings.Contains(note, "docker-local") || !strings.Contains(note, "last-active") {
		t.Errorf("note = %q, want mention of last-active and docker-local", note)
	}
}

// TestResolveEnvCmdProfile_NoActiveNoSidecar verifies the error path emits a
// clear message with the available-profiles list and does NOT return a profile
// the caller might accidentally use.
func TestResolveEnvCmdProfile_NoActiveNoSidecar(t *testing.T) {
	tmp := t.TempDir()
	chdir(t, tmp)

	cfg := &config.Config{
		Environment: &config.EnvironmentConfig{Provider: "docker"},
		Environments: map[string]*config.EnvironmentConfig{
			"docker-local": {Provider: "docker"},
			"hybrid-api":   {Provider: "terraform"},
		},
	}

	profile, note, err := resolveEnvCmdProfile("", cfg)
	if err == nil {
		t.Fatalf("expected error, got profile=%q note=%q", profile, note)
	}
	msg := err.Error()
	for _, want := range []string{"no active environment", "--env", "default", "docker-local", "hybrid-api"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
	if profile != "" {
		t.Errorf("profile on error should be empty; got %q", profile)
	}
}

// TestReadWriteLastProfile verifies the sidecar round-trips cleanly and that
// missing/empty cases return "" without errors.
func TestReadWriteLastProfile(t *testing.T) {
	tmp := t.TempDir()
	chdir(t, tmp)

	if got := readLastProfile(); got != "" {
		t.Errorf("readLastProfile on empty dir = %q, want \"\"", got)
	}
	writeLastProfile("terraform-infra")
	if got := readLastProfile(); got != "terraform-infra" {
		t.Errorf("readLastProfile after write = %q, want terraform-infra", got)
	}
	// Trailing whitespace/newline must be trimmed.
	if err := os.WriteFile(envLastProfilePath(), []byte("hybrid-api\n\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readLastProfile(); got != "hybrid-api" {
		t.Errorf("readLastProfile (with trailing whitespace) = %q, want hybrid-api", got)
	}
	// Sanity: the file lives at the documented path.
	if _, err := os.Stat(filepath.Join(envStateDir(), "last_profile")); err != nil {
		t.Errorf("last_profile sidecar missing: %v", err)
	}
}
