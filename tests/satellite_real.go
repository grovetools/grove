package tests

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/tend/pkg/harness"
)

// realSatellite points the scenarios at a live VM from the user's REAL
// satellite registry. Gated on TEND_SATELLITE_REAL=1 (name from
// TEND_SATELLITE_NAME, default "grove-satellite"). The real registry is only
// READ — its entry is copied verbatim into the sandbox's satellites.json —
// and all repo state lands in a per-run scratch --remote-code-dir on the VM,
// removed on Close.
type realSatellite struct {
	name          string
	entryRaw      json.RawMessage
	entry         satelliteRegistryEntry
	remoteCodeDir string // ~/code/tend-sat-sim-<hex>, scratch, removed on Close
	knownHosts    string
	host, port    string
}

func newRealSatellite(ctx *harness.Context) (satelliteEndpoint, string, error) {
	name := os.Getenv("TEND_SATELLITE_NAME")
	if name == "" {
		name = "grove-satellite"
	}

	// The REAL state file: paths.StateDir() evaluated against the harness
	// process's (un-sandboxed) environment — the same resolution the user's
	// grove uses (GROVE_HOME → XDG_STATE_HOME → ~/.local/state, + /grove).
	statePath := filepath.Join(paths.StateDir(), "satellites.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Sprintf("TEND_SATELLITE_REAL=1 but %s is unreadable (%v)", statePath, err), nil
	}
	var state struct {
		Satellites map[string]json.RawMessage `json:"satellites"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, "", fmt.Errorf("parse %s: %w", statePath, err)
	}
	raw, ok := state.Satellites[name]
	if !ok {
		return nil, fmt.Sprintf("TEND_SATELLITE_REAL=1 but satellite %q is not in %s", name, statePath), nil
	}
	var entry satelliteRegistryEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, "", fmt.Errorf("parse entry %q in %s: %w", name, statePath, err)
	}
	if entry.SSHAddr == "" || entry.HostKey == "" {
		return nil, fmt.Sprintf("satellite %q entry lacks ssh_addr/host_key — cannot drive it", name), nil
	}

	host, port, err := net.SplitHostPort(entry.SSHAddr)
	if err != nil {
		host, port = entry.SSHAddr, "22"
	}

	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return nil, "", err
	}
	r := &realSatellite{
		name:          name,
		entryRaw:      raw,
		entry:         entry,
		remoteCodeDir: "~/code/tend-sat-sim-" + hex.EncodeToString(suffix[:]),
		host:          host,
		port:          port,
	}

	// Pinned known_hosts for the provider's own test-side ssh calls — the
	// same never-TOFU stance as grove's transport.
	khLine := host + " " + entry.HostKey
	if port != "" && port != "22" {
		khLine = "[" + host + "]:" + port + " " + entry.HostKey
	}
	khPath := filepath.Join(ctx.RootDir, "real_known_hosts")
	if err := os.WriteFile(khPath, []byte(khLine+"\n"), 0o600); err != nil {
		return nil, "", err
	}
	r.knownHosts = khPath

	// Reachability probe doubles as the gate that the entry actually works.
	if _, err := r.Exec("true"); err != nil {
		return nil, fmt.Sprintf("satellite %q not reachable over ssh (%v)", name, err), nil
	}
	return r, "", nil
}

func (r *realSatellite) Name() string          { return r.name }
func (r *realSatellite) RemoteCodeDir() string { return r.remoteCodeDir }
func (r *realSatellite) IsSim() bool           { return false }

// ExtraGroveEnv: the real VM stages under its own /tmp (grove's default).
func (r *realSatellite) ExtraGroveEnv() []string { return nil }

func (r *realSatellite) RegistryEntry() satelliteRegistryEntry { return r.entry }

// RegistryEntryJSON returns the real registry entry verbatim so unknown
// fields survive the copy into the sandbox registry.
func (r *realSatellite) RegistryEntryJSON() json.RawMessage { return r.entryRaw }

// Exec streams a bash script to the VM over ssh with grove's own pinned
// option set (BatchMode, StrictHostKeyChecking=yes, generated known_hosts,
// locked HostKeyAlgorithms, IdentitiesOnly when an identity file is pinned).
func (r *realSatellite) Exec(script string) (string, error) {
	hostKeyAlgo := strings.Fields(r.entry.HostKey)[0]
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + r.knownHosts,
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "HostKeyAlgorithms=" + hostKeyAlgo,
	}
	if r.entry.IdentityFile != "" {
		args = append(args, "-o", "IdentitiesOnly=yes", "-i", r.entry.IdentityFile)
	}
	dest := r.host
	if r.entry.User != "" {
		dest = r.entry.User + "@" + r.host
	}
	args = append(args, "-p", r.port, dest, "bash -s")
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("real exec: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// Close removes the per-run scratch code dir on the VM.
func (r *realSatellite) Close() error {
	scratch := remoteCodeDirExpr(r.remoteCodeDir)
	if !strings.HasPrefix(scratch, "$HOME/code/tend-sat-sim-") {
		return fmt.Errorf("refusing to rm unexpected scratch dir %q", scratch)
	}
	_, err := r.Exec(fmt.Sprintf(`rm -rf "%s"`, scratch))
	return err
}
