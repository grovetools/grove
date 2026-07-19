package cmd

import (
	"encoding/json"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// The `grove satellite --json` contract.
//
// The consumer is a script or an agent driving the verbs in a loop, so the
// shapes here are versioned (`schema`), self-describing, and FLAT-KEYED: every
// documented field is always present, carrying its zero value when unknown,
// rather than being elided by omitempty. A parser can therefore index a field
// without probing for it, and a missing key means "this grove predates the
// field" instead of "this satellite happens not to have one".
//
// Every verb's payload embeds the same satelliteJSON object, so one parser
// serves status, list, up and down.
const (
	satelliteStatusSchema = "grove.satellite.status/v1"
	satelliteUpSchema     = "grove.satellite.up/v1"
	satelliteDownSchema   = "grove.satellite.down/v1"
)

// satelliteJSON is the machine view of one satellite: the registry entry's
// identity and endpoint fields plus the daemon's live health, with the human
// table's derived cells (State, the ssh invocation) precomputed so a caller
// never re-derives them from prose or from column order.
type satelliteJSON struct {
	Name string `json:"name"`
	// Kind is always normalized ("full" or "exec") — never the empty string
	// the registry uses for the default.
	Kind string `json:"kind"`
	// State is the same string the State column renders, so the two views
	// cannot disagree. Machine consumers should branch on the booleans below
	// rather than on this string.
	State string `json:"state"`
	// PartialUp is satelliteEntryIsPartial: a provider stamped a
	// provider_ref and `up` then failed. The machine may exist; the
	// satellite is unusable.
	PartialUp bool `json:"partial_up"`
	// Live reports whether the running daemon had a status for this name
	// (false = the row is registry-derived only).
	Live bool `json:"live"`
	// Provider is the infra target that created the machine, derived from
	// the provider_ref prefix (empty for entries carrying no ref, e.g. gcp).
	Provider    string `json:"provider"`
	ProviderRef string `json:"provider_ref"`
	// SSHAddr is the registry's host:port; SSHHost/SSHPort are the same
	// value pre-split, because the docker target publishes sshd on a random
	// loopback high port and every consumer otherwise re-parses it.
	SSHAddr      string `json:"ssh_addr"`
	SSHHost      string `json:"ssh_host"`
	SSHPort      int    `json:"ssh_port"`
	User         string `json:"user"`
	IdentityFile string `json:"identity_file"`
	// HostKeyPinned reports whether the entry carries the pinned host key
	// the transport refuses to run without (C2 — grove never TOFUs).
	HostKeyPinned bool `json:"host_key_pinned"`
	// SSHCommand is a ready-to-run ssh invocation for this satellite, and
	// GroveSSHCommand is the grove verb that wraps it with the pin applied.
	// Both exist so reaching a guest never requires assembling a private-key
	// path by convention.
	SSHCommand      string `json:"ssh_command"`
	GroveSSHCommand string `json:"grove_ssh_command"`
	SocketPath      string `json:"socket_path"`
	SyncLocalPort   int    `json:"sync_local_port"`
	SyncRemoteAddr  string `json:"sync_remote_addr"`
	Forward         string `json:"forward"`
	// Since is RFC3339 (empty when the daemon reported no connection time) —
	// the absolute instant behind the table's relative "5m ago" cell.
	Since     string `json:"since"`
	LastError string `json:"last_error"`
}

// satelliteStatusJSON is the `status`/`list` payload.
type satelliteStatusJSON struct {
	Schema string `json:"schema"`
	// DaemonReachable distinguishes "no satellite is connected" from "the
	// laptop daemon was not answering, so every row is registry-derived".
	DaemonReachable bool            `json:"daemon_reachable"`
	Satellites      []satelliteJSON `json:"satellites"`
}

// satelliteTimingsJSON carries wall-clock costs. Phases is an open map so a
// verb can report new steps without a schema bump; TotalMS is always the whole
// verb.
type satelliteTimingsJSON struct {
	TotalMS int64            `json:"total_ms"`
	Phases  map[string]int64 `json:"phases"`
}

// satelliteVerbJSON is the `up`/`down` summary. It is emitted on failure too
// (Ok=false, Error set) — an agent that only ever parses stdout still learns
// why the verb failed, and the process exit status stays non-zero.
type satelliteVerbJSON struct {
	Schema string `json:"schema"`
	Action string `json:"action"`
	Ok     bool   `json:"ok"`
	Name   string `json:"name"`
	Error  string `json:"error"`
	// Satellite is the resulting entry for `up`, and the entry as it stood
	// before teardown for `down`.
	Satellite      satelliteJSON        `json:"satellite"`
	DaemonReloaded bool                 `json:"daemon_reloaded"`
	Timings        satelliteTimingsJSON `json:"timings"`
	// RemovedState/RemovedInfraBlock/RemovedLegacyEntry report `down`'s
	// residue cleanup; they are always false for `up`.
	RemovedState       bool `json:"removed_state"`
	RemovedInfraBlock  bool `json:"removed_infra_block"`
	RemovedLegacyEntry bool `json:"removed_legacy_entry"`
}

// newSatelliteTimings starts a timing accumulator whose zero phases map is
// ready for record().
func newSatelliteTimings() *satelliteTimings {
	return &satelliteTimings{start: time.Now(), phases: map[string]int64{}}
}

type satelliteTimings struct {
	start  time.Time
	phases map[string]int64
}

// record stores the elapsed time since from under phase (suffixed _ms in the
// payload, so callers pass the bare step name).
func (t *satelliteTimings) record(phase string, from time.Time) {
	t.phases[phase+"_ms"] = time.Since(from).Milliseconds()
}

func (t *satelliteTimings) payload() satelliteTimingsJSON {
	return satelliteTimingsJSON{TotalMS: time.Since(t.start).Milliseconds(), Phases: t.phases}
}

// satelliteEntryJSON projects a registry entry (config ∪ state) into the
// machine view, with live carrying the daemon's status when it had one.
func satelliteEntryJSON(name string, entry satelliteConfigEntry, live *satelliteLiveStatus) satelliteJSON {
	out := satelliteJSON{
		Name:            name,
		Kind:            entry.effectiveKind(),
		State:           satelliteEntryState(name, entry, live),
		PartialUp:       satelliteEntryIsPartial(entry),
		Live:            live != nil,
		Provider:        satelliteProviderRefTarget(entry.ProviderRef),
		ProviderRef:     entry.ProviderRef,
		SSHAddr:         entry.SSHAddr,
		User:            entry.User,
		IdentityFile:    entry.IdentityFile,
		HostKeyPinned:   entry.HostKey != "",
		SSHCommand:      satelliteSSHCommand(entry),
		GroveSSHCommand: "grove satellite ssh " + name,
		SocketPath:      entry.SocketPath,
		SyncLocalPort:   entry.SyncLocalPort,
		SyncRemoteAddr:  entry.SyncRemoteAddr,
	}
	out.SSHHost, out.SSHPort = splitSatelliteAddr(entry.SSHAddr)
	if live != nil {
		if live.addr != "" {
			out.SSHAddr = live.addr
			out.SSHHost, out.SSHPort = splitSatelliteAddr(live.addr)
		}
		out.Forward = live.forward
		out.LastError = live.lastError
		if !live.since.IsZero() {
			out.Since = live.since.Format(time.RFC3339)
		}
	} else if satelliteEntryIsPartial(entry) {
		out.LastError = satellitePartialUpRemediation(name)
	}
	return out
}

// splitSatelliteAddr splits a registry ssh_addr into host and port, defaulting
// the port to 22 for the bare-host form gcp/tart record.
func splitSatelliteAddr(addr string) (string, int) {
	if addr == "" {
		return "", 0
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 22
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return host, 22
	}
	return host, n
}

// satelliteSSHCommand renders the ssh invocation that reaches a satellite —
// identity file, non-default port, and login user filled in. This is the line
// that used to have to be reconstructed from satellites.json plus the state
// directory's key-path convention. Empty when the entry has no address yet.
func satelliteSSHCommand(entry satelliteConfigEntry) string {
	host, port := splitSatelliteAddr(entry.SSHAddr)
	if host == "" {
		return ""
	}
	parts := []string{"ssh"}
	if entry.IdentityFile != "" {
		parts = append(parts, "-i", entry.IdentityFile, "-o", "IdentitiesOnly=yes")
	}
	if port != 0 && port != 22 {
		parts = append(parts, "-p", strconv.Itoa(port))
	}
	dest := host
	if entry.User != "" {
		dest = entry.User + "@" + host
	}
	return strings.Join(append(parts, dest), " ")
}

// writeSatelliteJSON encodes payload as the indented, newline-terminated
// document every other grove `--json` surface emits (cf. env_ecosystem,
// env_prune).
func writeSatelliteJSON(w *os.File, payload any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

// satelliteVerbReport accumulates the --json summary of a mutating verb while
// it runs, and emits it once at the end — success or failure.
type satelliteVerbReport struct {
	enabled bool
	// out is the process's real stdout, held aside while prose goes to
	// stderr.
	out      *os.File
	schema   string
	action   string
	name     string
	timings  *satelliteTimings
	entry    satelliteConfigEntry
	reloaded bool

	removedState       bool
	removedInfraBlock  bool
	removedLegacyEntry bool
}

// beginSatelliteVerbReport starts one. Under --json it routes the verb's human
// progress prose to stderr for the REMAINDER OF THE PROCESS: `up` and `down`
// narrate through package-level fmt.Print, and their subprocesses (terraform,
// the bootstrap script, the pinned ssh transport) inherit os.Stdout when they
// are started, so swapping the handle is what keeps stdout a single parseable
// document without threading a writer through every provider. It is never
// restored because the styled error printer would otherwise append prose after
// the document; the verb owns the process to its exit.
func beginSatelliteVerbReport(cmd *cobra.Command, action, schema, name string) *satelliteVerbReport {
	r := &satelliteVerbReport{
		enabled: satelliteJSONRequested(cmd),
		schema:  schema,
		action:  action,
		name:    name,
		timings: newSatelliteTimings(),
	}
	if r.enabled {
		r.out = os.Stdout
		os.Stdout = os.Stderr
	}
	return r
}

// phase records the wall-clock cost of a step that started at from.
func (r *satelliteVerbReport) phase(step string, from time.Time) { r.timings.record(step, from) }

// finish emits the summary. err nil means Ok; a non-nil err is reported in the
// document AND returned by the verb, so the process exit status still says the
// verb failed.
func (r *satelliteVerbReport) finish(err error) {
	if !r.enabled {
		return
	}
	sat := satelliteEntryJSON(r.name, r.entry, nil)
	if (r.entry == satelliteConfigEntry{}) {
		// No entry was ever assembled for this name — the verb failed before
		// one existed, or `down` found no record. The derived state would
		// otherwise describe a satellite that is not there.
		sat.State = ""
	}
	payload := satelliteVerbJSON{
		Schema:             r.schema,
		Action:             r.action,
		Ok:                 err == nil,
		Name:               r.name,
		Satellite:          sat,
		DaemonReloaded:     r.reloaded,
		Timings:            r.timings.payload(),
		RemovedState:       r.removedState,
		RemovedInfraBlock:  r.removedInfraBlock,
		RemovedLegacyEntry: r.removedLegacyEntry,
	}
	if err != nil {
		payload.Error = err.Error()
	}
	_ = writeSatelliteJSON(r.out, payload)
}
