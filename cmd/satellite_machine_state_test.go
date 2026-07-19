package cmd

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The machine_state probe is what closes F3, so these tests pin the three
// non-empty states each local provider reports, the "" fallbacks the contract
// leans on, and the promise that a broken/slow probe never fails status.

// stubTartCLIOnPath puts a fake `tart` at the front of PATH whose `list`
// answers with body (a JSON array), so the probe never touches the real tool
// (mirrors stubTartOnPath, but with a caller-chosen listing).
func stubTartCLIOnPath(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	// printf is a /bin/sh builtin, so the stub needs nothing on the (stub-only)
	// PATH; body carries only double quotes, safe inside the single-quoted arg.
	script := "#!/bin/sh\nprintf '%s' '" + body + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "tart"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// TestMachineStateClassifiers pins the pure lookup→state mapping both providers
// share: absent (not in the inventory), running, stopped.
func TestMachineStateClassifiers(t *testing.T) {
	if got := machineStateForTartVM(nil); got != satelliteMachineAbsent {
		t.Errorf("nil VM = %q, want %q", got, satelliteMachineAbsent)
	}
	if got := machineStateForTartVM(&tartVM{Running: true}); got != satelliteMachineRunning {
		t.Errorf("running VM = %q, want %q", got, satelliteMachineRunning)
	}
	if got := machineStateForTartVM(&tartVM{Running: false}); got != satelliteMachineStopped {
		t.Errorf("stopped VM = %q, want %q", got, satelliteMachineStopped)
	}
	if got := machineStateForDockerContainer(false, false); got != satelliteMachineAbsent {
		t.Errorf("absent container = %q, want %q", got, satelliteMachineAbsent)
	}
	if got := machineStateForDockerContainer(true, true); got != satelliteMachineRunning {
		t.Errorf("running container = %q, want %q", got, satelliteMachineRunning)
	}
	if got := machineStateForDockerContainer(false, true); got != satelliteMachineStopped {
		t.Errorf("present-not-running container = %q, want %q", got, satelliteMachineStopped)
	}
}

// TestProbeTartMachineStates pins the tart probe end to end over a stubbed
// `tart list`: a running VM, a stopped VM, and a satellite whose VM is not in
// the listing at all (absent), while a gcp/refless satellite is never probed
// and keeps machine_state "" (absent from the map).
func TestProbeTartMachineStates(t *testing.T) {
	setupGroveHome(t)
	stubTartCLIOnPath(t, `[
  {"Name":"grove-sat-run","Source":"local","State":"running","Running":true},
  {"Name":"grove-sat-stop","Source":"local","State":"stopped","Running":false}
]`)

	configured := map[string]satelliteConfigEntry{
		"run":   {ProviderRef: "tart:grove-sat-run"},
		"stop":  {ProviderRef: "tart:grove-sat-stop"},
		"gone":  {ProviderRef: "tart:grove-sat-gone"}, // deleted out of band
		"cloud": {SSHAddr: "203.0.113.7:22"},          // gcp/full — never probed
	}
	got := probeSatelliteMachineStates(configured)

	for name, want := range map[string]string{
		"run":  satelliteMachineRunning,
		"stop": satelliteMachineStopped,
		"gone": satelliteMachineAbsent,
	} {
		if got[name] != want {
			t.Errorf("machine_state[%q] = %q, want %q", name, got[name], want)
		}
	}
	if v, ok := got["cloud"]; ok {
		t.Errorf("gcp satellite was probed: machine_state = %q, want unmapped (\"\")", v)
	}
}

// TestProbeDockerMachineStates pins the docker probe over a single stubbed
// `docker ps -a`: running, a non-running (exited) container, and one absent
// from the listing.
func TestProbeDockerMachineStates(t *testing.T) {
	stubDockerOnPath(t, "printf 'grove-sat-run\\trunning\\ngrove-sat-stop\\texited\\n'\n")

	configured := map[string]satelliteConfigEntry{
		"run":  {ProviderRef: "docker:grove-sat-run"},
		"stop": {ProviderRef: "docker:grove-sat-stop"},
		"gone": {ProviderRef: "docker:grove-sat-gone"},
	}
	got := probeSatelliteMachineStates(configured)

	for name, want := range map[string]string{
		"run":  satelliteMachineRunning,
		"stop": satelliteMachineStopped,
		"gone": satelliteMachineAbsent,
	} {
		if got[name] != want {
			t.Errorf("machine_state[%q] = %q, want %q", name, got[name], want)
		}
	}
}

// TestProbeMachineStatesProbeErrorLeavesUnknown pins F3's robustness half: a
// provider whose probe errors contributes no entries, so those satellites keep
// machine_state "" rather than a wrong value — and the probe itself never fails.
func TestProbeMachineStatesProbeErrorLeavesUnknown(t *testing.T) {
	setupGroveHome(t)
	stubTartCLIOnPath(t, "") // an empty body is not a JSON array → parse error
	got := probeSatelliteMachineStates(map[string]satelliteConfigEntry{
		"sat": {ProviderRef: "tart:grove-sat-sat"},
	})
	if v, ok := got["sat"]; ok {
		t.Errorf("a failed probe reported machine_state %q; want the satellite unmapped (\"\")", v)
	}
}

// TestProbeMachineStatesTimeout pins that a slow provider cannot stall status:
// a `tart list` that outlives the probe budget is abandoned and its satellite
// keeps machine_state "".
func TestProbeMachineStatesTimeout(t *testing.T) {
	setupGroveHome(t)
	dir := t.TempDir()
	// A tart that sleeps well past the (tiny) probe timeout below. `exec sleep`
	// so the killed process IS the sleep (holding stdout), modelling a real
	// single-process `tart list` that hangs — not an sh with a lingering child.
	// PATH keeps the stub dir first (so `tart` is ours) but includes /bin for
	// `sleep`.
	if err := os.WriteFile(filepath.Join(dir, "tart"), []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/bin:/usr/bin")

	start := time.Now()
	got := probeSatelliteMachineStatesWithin(map[string]satelliteConfigEntry{
		"sat": {ProviderRef: "tart:grove-sat-sat"},
	}, 100*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("probe did not honor its timeout: took %s", elapsed)
	}
	if v, ok := got["sat"]; ok {
		t.Errorf("a timed-out probe reported machine_state %q; want the satellite unmapped (\"\")", v)
	}
}

// TestProbeMachineStatesNoLocalSatellites pins the zero-subprocess promise:
// with only gcp/refless satellites, no provider is invoked at all — the stub
// tart's sentinel proves `tart list` never ran.
func TestProbeMachineStatesNoLocalSatellites(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "tart-was-invoked")
	// `: > file` is a shell redirection (no external binary needed on the
	// stub-only PATH): the sentinel appears iff `tart` was actually run.
	script := "#!/bin/sh\n: > " + sentinel + "\necho '[]'\n"
	if err := os.WriteFile(filepath.Join(dir, "tart"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	got := probeSatelliteMachineStates(map[string]satelliteConfigEntry{
		"cloud": {SSHAddr: "203.0.113.7:22"}, // no provider_ref → no local provider
	})
	if len(got) != 0 {
		t.Errorf("probe returned %v for a registry with no local satellites, want empty", got)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Error("`tart list` ran despite no tart satellite being registered")
	}
}

// TestSatelliteTableRowsMachineStateSuffix pins F3's human surface: a non-empty
// machine_state is appended to the derived State cell (never replacing its
// logic), and an unknown ("") one leaves the cell alone.
func TestSatelliteTableRowsMachineStateSuffix(t *testing.T) {
	stateIdx := -1
	for i, h := range satelliteTableHeaders {
		if h == "State" {
			stateIdx = i
		}
	}
	if stateIdx == -1 {
		t.Fatalf("no State column in headers: %v", satelliteTableHeaders)
	}

	configured := map[string]satelliteConfigEntry{
		"stopped": {Kind: satelliteKindExec, SSHAddr: "192.168.64.2:22", HostKey: "ssh-ed25519 AAAA", ProviderRef: "tart:grove-sat-stopped"},
		"running": {Kind: satelliteKindExec, SSHAddr: "192.168.64.3:22", HostKey: "ssh-ed25519 AAAA", ProviderRef: "tart:grove-sat-running"},
		"unknown": {Kind: satelliteKindExec, SSHAddr: "192.168.64.4:22", HostKey: "ssh-ed25519 AAAA", ProviderRef: "tart:grove-sat-unknown"},
		"halfway": {Kind: satelliteKindExec, ProviderRef: "tart:grove-sat-halfway"}, // partial up
	}
	machineStates := map[string]string{
		"stopped": satelliteMachineStopped,
		"running": satelliteMachineRunning,
		"halfway": satelliteMachineStopped,
		// "unknown" deliberately absent → "".
	}
	rows := satelliteTableRows(configured, nil, machineStates)
	byName := map[string][]string{}
	for _, r := range rows {
		byName[r[0]] = r
	}

	for _, tc := range []struct{ name, want string }{
		{"stopped", satelliteStateExecOnly + " (stopped)"},
		{"running", satelliteStateExecOnly + " (running)"},
		{"unknown", satelliteStateExecOnly}, // no suffix when the probe cannot say
		{"halfway", satelliteStatePartialUp + " (stopped)"},
	} {
		if got := byName[tc.name][stateIdx]; got != tc.want {
			t.Errorf("%s State cell = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestSatelliteStatusPayloadMachineState pins the --json wiring: the probe
// result lands on the matching satellite's machine_state, an unmapped one stays
// "", and the State string is left as the raw derived value (machine consumers
// branch on machine_state, not on a suffixed State).
func TestSatelliteStatusPayloadMachineState(t *testing.T) {
	got := satelliteStatusPayload(
		map[string]satelliteConfigEntry{
			"exec":  {Kind: satelliteKindExec, SSHAddr: "192.168.64.2:22", HostKey: "ssh-ed25519 AAAA", ProviderRef: "tart:grove-sat-exec"},
			"cloud": {SSHAddr: "203.0.113.7:22"},
		},
		nil,
		map[string]string{"exec": satelliteMachineStopped},
	)
	byName := map[string]satelliteJSON{}
	for _, s := range got {
		byName[s.Name] = s
	}
	if byName["exec"].MachineState != satelliteMachineStopped {
		t.Errorf("exec machine_state = %q, want %q", byName["exec"].MachineState, satelliteMachineStopped)
	}
	if byName["exec"].State != satelliteStateExecOnly {
		t.Errorf("exec State = %q, want the raw %q (no suffix in JSON)", byName["exec"].State, satelliteStateExecOnly)
	}
	if byName["cloud"].MachineState != "" {
		t.Errorf("gcp machine_state = %q, want \"\"", byName["cloud"].MachineState)
	}
}

// TestRenderSatellitesSucceedsOnProbeError pins the end-to-end contract: even
// when the provider probe fails, `status --json` still exits 0 and every
// satellite object carries machine_state (as "").
func TestRenderSatellitesSucceedsOnProbeError(t *testing.T) {
	setupGroveHome(t)
	writeSatelliteStateEntry(t, "tartdemo", satelliteConfigEntry{
		SSHAddr:     "192.168.64.2:22",
		User:        "admin",
		HostKey:     "ssh-ed25519 AAAA",
		Kind:        satelliteKindExec,
		ProviderRef: "tart:grove-sat-tartdemo",
	})
	stubTartCLIOnPath(t, "") // parse error → probe cannot say

	orig := os.Stdout
	t.Cleanup(func() { os.Stdout = orig })
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := renderSatellites(true)
	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("status --json failed on a broken probe: %v", runErr)
	}

	// A real daemon may be running on the dev machine and inject live rows, so
	// locate the state-file satellite by name rather than asserting a count.
	var doc satelliteStatusJSON
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("stdout is not one JSON document (%v): %s", err, out)
	}
	found := false
	for _, s := range doc.Satellites {
		if s.Name != "tartdemo" {
			continue
		}
		found = true
		if s.MachineState != "" {
			t.Errorf("machine_state = %q on a failed probe, want \"\"", s.MachineState)
		}
	}
	if !found {
		t.Fatalf("tartdemo missing from the status document: %s", out)
	}

	// The key must be PRESENT, not merely zero — re-check on the raw object.
	var raw struct {
		Satellites []map[string]json.RawMessage `json:"satellites"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	for _, s := range raw.Satellites {
		if _, ok := s["machine_state"]; !ok {
			t.Errorf("machine_state key absent from a status object: %v", s)
		}
	}
}
