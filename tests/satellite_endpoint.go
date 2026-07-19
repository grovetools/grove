package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/grovetools/tend/pkg/harness"
)

// The satellite E2E scenarios drive `grove satellite repos|worktree push/pull`
// against a satellite endpoint: something reachable over REAL `ssh`/`scp`
// (grove's transport shells out to the actual binaries with a pinned host
// key) that has a HOME of its own, git, and optionally a grove binary on its
// PATH. Two providers implement the seam:
//
//   - simSatellite (satellite_sim.go): an in-process SSH server bound to
//     127.0.0.1 with a throwaway satellite HOME. The default.
//   - realSatellite (satellite_real.go): the entry from the user's REAL
//     satellite registry, gated on TEND_SATELLITE_REAL=1.
//
// Scenarios consume the interface only, so the same scenario bodies verify
// the sim today and a live VM later.

// satelliteEndpoint is the provider seam the scenarios consume.
type satelliteEndpoint interface {
	// Name is the registry name the scenario passes to `grove satellite …`.
	Name() string
	// RegistryEntry returns the satellites.json entry fields for this
	// endpoint (ssh_addr, user, host_key, identity_file).
	RegistryEntry() satelliteRegistryEntry
	// RegistryEntryJSON is the raw entry written into the sandbox's
	// satellites.json (the real provider copies its entry verbatim so
	// fields this seam does not model survive the round trip).
	RegistryEntryJSON() json.RawMessage
	// RemoteCodeDir is the value for --remote-code-dir (may be ~-relative).
	RemoteCodeDir() string
	// Exec runs a bash script on the satellite (test-side manipulation such
	// as making "agent" commits) and returns its stdout.
	Exec(script string) (string, error)
	// IsSim reports whether this endpoint is the local simulator (some
	// scenarios, e.g. the vintage guard, only make sense against the sim).
	IsSim() bool
	// ExtraGroveEnv returns extra environment entries for the laptop-side
	// grove invocations (the sim redirects the VM stage base to a writable
	// location via GROVE_SATELLITE_STAGE_BASE; the real VM uses defaults).
	ExtraGroveEnv() []string
	// Close tears the endpoint down (kills the sim server / removes the
	// real VM's scratch code dir).
	Close() error
}

// satelliteRegistryEntry mirrors the JSON shape of one satellites.json entry
// (cmd/satellite_state.go / satelliteConfigEntry). Only the ssh-transport
// fields the scenarios need are modeled; the real provider copies unknown
// fields verbatim via raw JSON.
type satelliteRegistryEntry struct {
	SSHAddr      string `json:"ssh_addr"`
	User         string `json:"user,omitempty"`
	HostKey      string `json:"host_key"`
	IdentityFile string `json:"identity_file,omitempty"`
}

// writeSatelliteRegistry writes the sandbox's satellites.json state file
// ({"satellites":{"<name>":{...}}}) into the sandboxed grove state dir, which
// the sandboxed grove binary resolves via XDG_STATE_HOME.
func writeSatelliteRegistry(ctx *harness.Context, name string, entry json.RawMessage) error {
	stateDir := filepath.Join(ctx.StateDir(), "grove")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	payload := map[string]map[string]json.RawMessage{
		"satellites": {name: entry},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir, "satellites.json"), append(data, '\n'), 0o600)
}

// setupSatelliteEndpoint builds the endpoint for a scenario: the real VM when
// TEND_SATELLITE_REAL=1, else the local simulator. A non-empty skip reason
// (environmental prerequisite missing, e.g. no real registry entry) means the
// scenario should pass-with-notice: tend has no runtime skip, so callers
// record the reason and every subsequent step early-returns.
func setupSatelliteEndpoint(ctx *harness.Context, installGrove bool) (satelliteEndpoint, string, error) {
	if os.Getenv("TEND_SATELLITE_REAL") == "1" {
		return newRealSatellite(ctx)
	}
	return newSimSatellite(ctx, installGrove)
}

// --- scenario-level serialization ---

// The VM-side stage dirs grove uses are fixed absolute paths on the satellite
// (<stage base>/grove-satellite-repos, …). For the sim they land on the local
// filesystem, and for the real provider on the one shared VM — either way
// concurrent satellite scenarios would clobber each other's staged bundles
// under `tend run -p`. A blocking flock serializes just these scenarios.
func satelliteScenarioLockPath() string {
	return filepath.Join(writableTmpBase(), "grove-satellite-e2e.lock")
}

// writableTmpBase prefers /tmp (short paths with a conservative charset —
// the VM worktree path grove computes must satisfy its safe-path validation,
// and macOS's default TMPDIR under /var/folders can contain characters
// outside it) but falls back to os.TempDir() in sandboxed environments where
// /tmp is not writable.
func writableTmpBase() string {
	probe, err := os.CreateTemp("/tmp", "grove-sat-probe-")
	if err != nil {
		return os.TempDir()
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	return "/tmp"
}

func acquireSatelliteLock() (*os.File, error) {
	return acquireFlock(satelliteScenarioLockPath())
}

// acquireFlock takes a blocking exclusive flock on path (creating it if
// needed) — shared by the satellite scenario lock above and the lifecycle
// scenarios' dedicated lock (scenarios_satellite_lifecycle.go).
func acquireFlock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flock %s: %w", f.Name(), err)
	}
	return f, nil
}

func releaseSatelliteLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

// remoteCodeDirExpr renders a --remote-code-dir value for use inside an
// Exec script: a leading ~/ becomes $HOME/ (same convention as grove's
// generated scripts, which run in non-login shells without tilde context).
func remoteCodeDirExpr(dir string) string {
	if strings.HasPrefix(dir, "~/") {
		return "$HOME/" + strings.TrimPrefix(dir, "~/")
	}
	return dir
}
