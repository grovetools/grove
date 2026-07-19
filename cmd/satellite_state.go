package cmd

// The satellite provisioning STATE file — the config/state split's state half.
//
// `grove satellite up` used to write the whole flat [satellites.<name>] entry
// into the global grove config, but most of those fields (ssh_addr, host_key,
// socket_path, sync_remote_addr) are machine-derived provisioning state that
// churns on every VM recreate — writing them into grove.toml duplicated and
// drifted from the user's real config (often a dotfiles fragment symlinked
// into the config dir). They now live in a CLI-owned JSON state file,
// $XDG_STATE_HOME/grove/satellites.json (paths.StateDir), which `up` upserts
// and `down` removes. Reads go through loadMergedSatellites: config ∪ state,
// merged per name per field (see mergeSatelliteEntries), the exact merge the
// daemon's satellite.LoadRegistry performs at boot.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/grovetools/core/pkg/paths"
)

// satelliteStateFileName under paths.StateDir(). Must match the daemon's
// satellite.LoadRegistry (daemon/internal/daemon/satellite/registry.go).
const satelliteStateFileName = "satellites.json"

// satelliteStatePath resolves the CLI-owned provisioning state file.
func satelliteStatePath() (string, error) {
	dir := paths.StateDir()
	if dir == "" {
		return "", fmt.Errorf("could not resolve grove state directory")
	}
	return filepath.Join(dir, satelliteStateFileName), nil
}

// satelliteStateFile is the on-disk JSON shape:
//
//	{"satellites": {"<name>": {"ssh_addr": ..., "host_key": ..., ...}}}
//
// Entry field names ride satelliteConfigEntry's json tags, which mirror the
// yaml/toml tags the config path uses, so the merge code stays obvious.
type satelliteStateFile struct {
	Satellites map[string]satelliteConfigEntry `json:"satellites"`
}

// loadSatelliteState reads the state file. An absent file is the normal
// fresh-machine case and yields an empty map; a read/parse failure is an
// error (callers decide whether it is fatal — the merged read path degrades
// with a warning, the write path self-heals).
func loadSatelliteState() (map[string]satelliteConfigEntry, error) {
	path, err := satelliteStatePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]satelliteConfigEntry{}, nil
		}
		return nil, err
	}
	var sf satelliteStateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if sf.Satellites == nil {
		sf.Satellites = map[string]satelliteConfigEntry{}
	}
	return sf.Satellites, nil
}

// writeSatelliteState atomically persists the full satellite map
// (temp file + rename in the state dir, so a crash never leaves a
// half-written satellites.json).
func writeSatelliteState(entries map[string]satelliteConfigEntry) error {
	path, err := satelliteStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if entries == nil {
		entries = map[string]satelliteConfigEntry{}
	}
	data, err := json.MarshalIndent(satelliteStateFile{Satellites: entries}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".satellites-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// upsertSatelliteState writes/replaces one satellite's state entry. A corrupt
// state file is machine-owned and non-fatal here: it is warned about and
// rewritten fresh (the whole file's content is re-derivable by re-running
// `up` per satellite).
func upsertSatelliteState(name string, entry satelliteConfigEntry) error {
	entries, err := loadSatelliteState()
	if err != nil {
		fmt.Printf("warning: satellite state file unreadable, rewriting it: %v\n", err)
		entries = map[string]satelliteConfigEntry{}
	}
	entries[name] = entry
	return writeSatelliteState(entries)
}

// removeSatelliteState removes one satellite's state entry, reporting whether
// it was present. Removing the last entry leaves an empty (but valid) file. A
// corrupt state file is rewritten empty (self-heal) and reports not-present.
func removeSatelliteState(name string) (bool, error) {
	entries, err := loadSatelliteState()
	if err != nil {
		fmt.Printf("warning: satellite state file unreadable, rewriting it: %v\n", err)
		return false, writeSatelliteState(nil)
	}
	if _, ok := entries[name]; !ok {
		return false, nil
	}
	delete(entries, name)
	return true, writeSatelliteState(entries)
}

// mergeSatelliteEntries merges the config view ([satellites.*] tables from the
// layered grove config, dotfiles fragments included) with the state view
// (satellites.json), per name per field — the same rule as the daemon's
// satellite.LoadRegistry:
//
//   - machine-derived fields (ssh_addr, host_key, socket_path,
//     sync_remote_addr): a non-empty STATE value wins — they churn on every VM
//     recreate and the CLI's provisioning snapshot is the truth.
//   - user-authored fields (user, identity_file, kind, sync_local_port): a
//     non-empty CONFIG value wins over the state's resolved snapshot.
//
// A satellite present in only one source passes through complete. When config
// still carries a flat ssh_addr/host_key that CONFLICTS with state (a legacy
// block an older `up` wrote into grove.toml), state wins and a one-line
// warning per satellite suggests deleting the stale block.
func mergeSatelliteEntries(fromConfig, fromState map[string]satelliteConfigEntry) (map[string]satelliteConfigEntry, []string) {
	merged := make(map[string]satelliteConfigEntry, len(fromConfig)+len(fromState))
	for name, e := range fromConfig {
		merged[name] = e
	}

	names := make([]string, 0, len(fromState))
	for name := range fromState {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic warning order

	var warnings []string
	for _, name := range names {
		st := fromState[name]
		cfg, ok := merged[name]
		if !ok {
			merged[name] = st
			continue
		}
		if (cfg.SSHAddr != "" && st.SSHAddr != "" && cfg.SSHAddr != st.SSHAddr) ||
			(cfg.HostKey != "" && st.HostKey != "" && cfg.HostKey != st.HostKey) {
			warnings = append(warnings, fmt.Sprintf(
				"warning: [satellites.%s] in the grove config carries a stale ssh_addr/host_key (the satellite state file wins) — delete the flat [satellites.%s] block from your config; it is provisioning state now",
				name, name))
		}
		out := cfg
		if st.SSHAddr != "" {
			out.SSHAddr = st.SSHAddr
		}
		if st.HostKey != "" {
			out.HostKey = st.HostKey
		}
		if st.SocketPath != "" {
			out.SocketPath = st.SocketPath
		}
		if st.SyncRemoteAddr != "" {
			out.SyncRemoteAddr = st.SyncRemoteAddr
		}
		if st.ProviderRef != "" {
			// Machine-derived like ssh_addr/host_key: the CLI's provisioning
			// snapshot is the truth for which machine the provider created.
			out.ProviderRef = st.ProviderRef
		}
		if out.User == "" {
			out.User = st.User
		}
		if out.IdentityFile == "" {
			out.IdentityFile = st.IdentityFile
		}
		if out.Kind == "" {
			// Kind is user-authored first, but `up` also stamps it into the
			// state snapshot — same fallback rule as user/identity_file
			// (empty in both sources = full).
			out.Kind = st.Kind
		}
		if out.SyncLocalPort == 0 {
			out.SyncLocalPort = st.SyncLocalPort
		}
		merged[name] = out
	}
	return merged, warnings
}

// loadMergedSatellites is the CLI's registry read: config ∪ state, mirroring
// the daemon's LoadRegistry so status/list/upgrade/config-push/down see both
// hand-managed (config-only) and provisioned (state-backed) satellites.
// Warnings (drifted legacy blocks, unreadable state file) print to stderr.
func loadMergedSatellites() map[string]satelliteConfigEntry {
	fromConfig := loadConfiguredSatellites()
	fromState, err := loadSatelliteState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not read the satellite state file: %v\n", err)
		fromState = nil
	}
	merged, warnings := mergeSatelliteEntries(fromConfig, fromState)
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, w)
	}
	return merged
}
