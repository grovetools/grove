package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mustLoadSatelliteState loads the state file, failing the test on any error
// (absent file is the empty map, not an error).
func mustLoadSatelliteState(t *testing.T) map[string]satelliteConfigEntry {
	t.Helper()
	state, err := loadSatelliteState()
	if err != nil {
		t.Fatalf("loadSatelliteState: %v", err)
	}
	return state
}

// TestSatelliteStateRoundTrip covers the state file lifecycle: absent file →
// empty map, upsert (insert + replace) with multiple satellites, removal down
// to an empty-but-valid file, and the absent-name no-op.
func TestSatelliteStateRoundTrip(t *testing.T) {
	setupGroveHome(t)

	// Absent file → empty, not an error.
	if state := mustLoadSatelliteState(t); len(state) != 0 {
		t.Fatalf("absent state file loaded as %v, want empty", state)
	}

	sat1 := satelliteConfigEntry{
		SSHAddr:        "203.0.113.7:22",
		User:           "solair",
		HostKey:        "ssh-ed25519 AAAA",
		IdentityFile:   "/home/u/.ssh/id_ed25519",
		SocketPath:     "/run/user/1001/grove/groved.sock",
		SyncLocalPort:  8788,
		SyncRemoteAddr: "127.0.0.1:8788",
	}
	sat2 := satelliteConfigEntry{SSHAddr: "10.0.0.9:22", User: "u2", HostKey: "ssh-ed25519 BBBB"}
	if err := upsertSatelliteState("sat1", sat1); err != nil {
		t.Fatalf("upsertSatelliteState sat1: %v", err)
	}
	if err := upsertSatelliteState("sat2", sat2); err != nil {
		t.Fatalf("upsertSatelliteState sat2: %v", err)
	}

	state := mustLoadSatelliteState(t)
	if len(state) != 2 || state["sat1"] != sat1 || state["sat2"] != sat2 {
		t.Fatalf("state after two upserts = %+v", state)
	}

	// Upsert replaces in place (a re-provisioned VM gets a new addr + key).
	sat1.SSHAddr = "203.0.113.99:22"
	sat1.HostKey = "ssh-ed25519 NEWKEY"
	if err := upsertSatelliteState("sat1", sat1); err != nil {
		t.Fatalf("upsertSatelliteState (replace): %v", err)
	}
	state = mustLoadSatelliteState(t)
	if len(state) != 2 || state["sat1"] != sat1 {
		t.Fatalf("state after replace = %+v", state)
	}

	// Remove one; the other survives.
	removed, err := removeSatelliteState("sat1")
	if err != nil || !removed {
		t.Fatalf("removeSatelliteState sat1 = (%v, %v), want (true, nil)", removed, err)
	}
	state = mustLoadSatelliteState(t)
	if len(state) != 1 || state["sat2"] != sat2 {
		t.Fatalf("state after removing sat1 = %+v", state)
	}

	// Removing an absent name is a clean no-op.
	removed, err = removeSatelliteState("sat1")
	if err != nil || removed {
		t.Fatalf("removeSatelliteState (absent) = (%v, %v), want (false, nil)", removed, err)
	}

	// Removing the last entry leaves an empty but valid file.
	if removed, err = removeSatelliteState("sat2"); err != nil || !removed {
		t.Fatalf("removeSatelliteState sat2 = (%v, %v)", removed, err)
	}
	path, err := satelliteStatePath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file gone after removing the last entry: %v", err)
	}
	if state := mustLoadSatelliteState(t); len(state) != 0 {
		t.Fatalf("state after removing all = %+v, want empty", state)
	}
}

// TestSatelliteStateAtomicWrite pins the temp+rename write: the final file is
// valid JSON with 0600 perms, and no temp files are left behind in the state
// dir (a crash mid-write can leak a temp file, but a completed write never
// does — and the target is never observed half-written because the rename is
// atomic).
func TestSatelliteStateAtomicWrite(t *testing.T) {
	setupGroveHome(t)
	if err := upsertSatelliteState("sat1", satelliteConfigEntry{
		SSHAddr: "203.0.113.7:22", User: "solair", HostKey: "ssh-ed25519 AAAA",
	}); err != nil {
		t.Fatalf("upsertSatelliteState: %v", err)
	}

	path, err := satelliteStatePath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("state file missing: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("state file perm = %o, want 600", perm)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind after write: %s", e.Name())
		}
	}
}

// TestSatelliteStateCorruptFileSelfHeals: a corrupt satellites.json (the file
// is machine-owned) must never wedge the CLI — the merged read degrades to
// config-only, upsert rewrites the file fresh, and remove rewrites it empty.
func TestSatelliteStateCorruptFileSelfHeals(t *testing.T) {
	setupGroveHome(t)
	path, err := satelliteStatePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("{ this is not json")

	// Read path: an explicit error (loadMergedSatellites degrades on it).
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSatelliteState(); err == nil {
		t.Fatal("corrupt state file loaded without error")
	}
	merged, warnings := mergeSatelliteEntries(map[string]satelliteConfigEntry{
		"cfg-only": {SSHAddr: "1.1.1.1:22", User: "u", HostKey: "ssh-ed25519 CFG"},
	}, nil)
	if len(merged) != 1 || len(warnings) != 0 {
		t.Fatalf("config-only degrade = (%v, %v)", merged, warnings)
	}

	// Upsert self-heals: the corrupt content is replaced wholesale.
	entry := satelliteConfigEntry{SSHAddr: "203.0.113.7:22", User: "solair", HostKey: "ssh-ed25519 AAAA"}
	if err := upsertSatelliteState("sat1", entry); err != nil {
		t.Fatalf("upsertSatelliteState over corrupt file: %v", err)
	}
	if state := mustLoadSatelliteState(t); len(state) != 1 || state["sat1"] != entry {
		t.Fatalf("state after healing upsert = %+v", state)
	}

	// Remove self-heals too: corrupt file → rewritten empty, reported absent.
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := removeSatelliteState("sat1")
	if err != nil || removed {
		t.Fatalf("removeSatelliteState over corrupt file = (%v, %v), want (false, nil)", removed, err)
	}
	if state := mustLoadSatelliteState(t); len(state) != 0 {
		t.Fatalf("state after healing remove = %+v, want empty", state)
	}
}

// TestMergeSatelliteEntriesMatrix pins the per-field merge rule: machine-
// derived fields (ssh_addr, host_key, socket_path, sync_remote_addr) prefer a
// non-empty STATE value; user-authored fields (user, identity_file,
// sync_local_port) prefer a non-empty CONFIG value; either side's value fills
// a gap on the other. Config-only and state-only entries pass through
// complete.
func TestMergeSatelliteEntriesMatrix(t *testing.T) {
	fromConfig := map[string]satelliteConfigEntry{
		// Hand-managed VM: config-only, must pass through untouched.
		"hand": {SSHAddr: "192.0.2.1:22", User: "hand-u", HostKey: "ssh-ed25519 HAND"},
		// Both sides present: every field class exercised. Config pins the
		// user-authored fields and carries a stale addr/key; it has no
		// socket_path/sync_remote_addr (state fills those).
		"both": {
			SSHAddr:       "198.51.100.1:22", // stale (state differs → state wins)
			User:          "cfg-user",        // config wins
			HostKey:       "ssh-ed25519 STALE",
			IdentityFile:  "/cfg/key", // config wins
			SyncLocalPort: 9999,       // config wins
		},
	}
	fromState := map[string]satelliteConfigEntry{
		"both": {
			SSHAddr:        "203.0.113.7:22",
			User:           "state-user",
			HostKey:        "ssh-ed25519 FRESH",
			IdentityFile:   "/state/key",
			SocketPath:     "/run/user/1001/grove/groved.sock",
			SyncLocalPort:  8788,
			SyncRemoteAddr: "127.0.0.1:8788",
		},
		// Provisioned-only satellite: state-only, must pass through complete.
		"cattle": {
			SSHAddr: "203.0.113.8:22", User: "state-u", HostKey: "ssh-ed25519 CATTLE",
			SocketPath: "/run/user/1002/grove/groved.sock",
		},
	}

	merged, warnings := mergeSatelliteEntries(fromConfig, fromState)
	if len(merged) != 3 {
		t.Fatalf("merged names = %v, want 3", merged)
	}
	if got := merged["hand"]; got != fromConfig["hand"] {
		t.Errorf("config-only entry mangled: %+v", got)
	}
	if got := merged["cattle"]; got != fromState["cattle"] {
		t.Errorf("state-only entry mangled: %+v", got)
	}

	got := merged["both"]
	want := satelliteConfigEntry{
		SSHAddr:        "203.0.113.7:22",                   // state wins
		HostKey:        "ssh-ed25519 FRESH",                // state wins
		SocketPath:     "/run/user/1001/grove/groved.sock", // state fills the gap
		SyncRemoteAddr: "127.0.0.1:8788",                   // state fills the gap
		User:           "cfg-user",                         // config wins
		IdentityFile:   "/cfg/key",                         // config wins
		SyncLocalPort:  9999,                               // config wins
	}
	if got != want {
		t.Errorf("merged both = %+v, want %+v", got, want)
	}

	// The stale legacy addr/key conflict warns exactly once, naming the block.
	if len(warnings) != 1 || !strings.Contains(warnings[0], "[satellites.both]") {
		t.Errorf("warnings = %v, want one stale-block warning for both", warnings)
	}

	// User-authored fields fall back to the state snapshot when config leaves
	// them empty — a config entry that only pins the host_key still merges
	// into a complete entry.
	merged, warnings = mergeSatelliteEntries(
		map[string]satelliteConfigEntry{"s": {HostKey: "ssh-ed25519 PINNED"}},
		map[string]satelliteConfigEntry{"s": {
			SSHAddr: "203.0.113.9:22", User: "snap-u", HostKey: "ssh-ed25519 PINNED",
			IdentityFile: "/snap/key", SyncLocalPort: 8788,
		}},
	)
	got = merged["s"]
	if got.User != "snap-u" || got.IdentityFile != "/snap/key" || got.SyncLocalPort != 8788 || got.SSHAddr != "203.0.113.9:22" {
		t.Errorf("snapshot fallback merge = %+v", got)
	}
	if got.HostKey != "ssh-ed25519 PINNED" || len(warnings) != 0 {
		t.Errorf("matching host_key must not warn: %+v, %v", got, warnings)
	}
}

// TestSatelliteInfraDriftMessage pins the write-back rescope's user-facing
// half: identical resolved values → silence; drifted values → a message that
// names the changed fields and includes the paste-ready up-to-date block.
func TestSatelliteInfraDriftMessage(t *testing.T) {
	cfg := satelliteInfraConfig{Project: "my-proj", SSHUser: "solair", CIDR: "203.0.113.7/32"}

	if msg := satelliteInfraDriftMessage("sat1", cfg, cfg); msg != "" {
		t.Fatalf("no-drift message = %q, want empty", msg)
	}

	resolved := cfg
	resolved.CIDR = "198.51.100.9/32" // re-detected public IP
	resolved.Zone = "us-west1-a"
	msg := satelliteInfraDriftMessage("sat1", cfg, resolved)
	for _, want := range []string{
		"[satellites.sat1.infra]",
		`cidr: "203.0.113.7/32" -> "198.51.100.9/32"`,
		`zone: "" -> "us-west1-a"`,
		`cidr = "198.51.100.9/32"`,
		`zone = "us-west1-a"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("drift message missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "project:") || strings.Contains(msg, "ssh_user:") {
		t.Errorf("drift message lists unchanged fields:\n%s", msg)
	}
}
