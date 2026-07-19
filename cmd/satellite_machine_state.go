package cmd

// The status-time machine-state probe for `grove satellite status`/`list`.
//
// Exec-kind satellites — the only kind the local tart/docker targets create —
// are never dialled by the daemon's ConnManager, so the live-status half says
// nothing about whether their VM/container is actually up: a powered-off tart
// VM keeps reporting State "exec-only" with an empty Last Error (F3), and an
// agent driving from --json cannot tell a usable satellite from a dead one
// short of attempting an exec and reading an ssh timeout. This probe closes the
// gap by asking each LOCAL provider that actually owns a satellite here for its
// inventory in ONE cheap, read-only subprocess (`tart list` / `docker ps -a`)
// and mapping every satellite's provider_ref handle to a machine_state the
// --json contract and the human table both surface.
//
// It never blocks status. Each provider probe is capped by a short timeout, so
// a hung docker daemon cannot stall `status`; a probe that errors or times out
// simply leaves its satellites' machine_state at "" (the contract's zero-value
// "the provider cannot say"), and every other row still renders. gcp/full
// satellites are never probed — their liveness is the ConnManager's job and
// status must make no billable API calls — so their machine_state stays "".

import (
	"context"
	"strings"
	"time"
)

// Machine states the probe reports as the machine_state field. "" (the zero
// value) is the contract's "unknown" — a provider that could not be asked,
// timed out, or does not report machine liveness (gcp) — so these are the only
// non-empty values a consumer sees.
const (
	// satelliteMachineRunning: the machine is in the provider's inventory and
	// powered on.
	satelliteMachineRunning = "running"
	// satelliteMachineStopped: in the provider's inventory but not running (a
	// stopped tart VM, a non-running docker container).
	satelliteMachineStopped = "stopped"
	// satelliteMachineAbsent: the provider answered and the machine is NOT in
	// its inventory — deleted out of band. Distinct from stopped: there is
	// nothing to restart.
	satelliteMachineAbsent = "absent"
)

// satelliteMachineProbeTimeout caps each provider's probe subprocess. Status
// must never stall on a slow `tart list` or a hung docker daemon, so a probe
// that outruns this is abandoned and its satellites keep machine_state "".
const satelliteMachineProbeTimeout = 3 * time.Second

// probeSatelliteMachineStates returns machine_state keyed by satellite name for
// every satellite whose provider_ref names a LOCAL provider that can be cheaply
// probed (tart, docker). It issues at most one subprocess per distinct probe
// target and only for providers that actually own a satellite here — a registry
// with no tart/docker satellites makes zero subprocess calls, and gcp/refless
// entries never contribute. A satellite absent from the returned map has an
// unknown machine_state ("") at the call site.
func probeSatelliteMachineStates(configured map[string]satelliteConfigEntry) map[string]string {
	return probeSatelliteMachineStatesWithin(configured, satelliteMachineProbeTimeout)
}

// probeSatelliteMachineStatesWithin is probeSatelliteMachineStates with an
// explicit per-provider timeout, so tests can drive the probe fast.
func probeSatelliteMachineStatesWithin(configured map[string]satelliteConfigEntry, timeout time.Duration) map[string]string {
	var tartNames, dockerNames []string
	for name, entry := range configured {
		// provider_ref is the authoritative provider identity (33739db); the
		// config target is only a fallback the daemon-side merge does not see.
		switch satelliteProviderRefTarget(entry.ProviderRef) {
		case tartSatelliteTarget:
			tartNames = append(tartNames, name)
		case dockerSatelliteTarget:
			dockerNames = append(dockerNames, name)
		}
	}
	out := map[string]string{}
	if len(tartNames) > 0 {
		probeTartMachineStates(configured, tartNames, timeout, out)
	}
	if len(dockerNames) > 0 {
		probeDockerMachineStates(configured, dockerNames, timeout, out)
	}
	return out
}

// probeTartMachineStates fills out[name] for the given tart satellites from
// `tart list`. It groups by the effective TART_HOME `up` recorded — one list
// call per distinct store, one in the common single-store case — so a VM
// created in a relocated store is probed against that store rather than
// reported absent. A list error or timeout leaves that store's satellites
// untouched (machine_state ""); status still succeeds.
func probeTartMachineStates(configured map[string]satelliteConfigEntry, names []string, timeout time.Duration, out map[string]string) {
	byHome := map[string][]string{}
	for _, name := range names {
		// resolveTartHome falls back to the process env then tart's own
		// default, so an entry with no recorded tart_home is probed exactly the
		// way every other tart command for it resolves the store.
		infra, _, err := loadSatelliteInfra(name)
		if err != nil {
			continue // a malformed infra block must never fail status
		}
		home := resolveTartHome(infra.TartHome)
		byHome[home] = append(byHome[home], name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for home, group := range byHome {
		vms, err := tartListContext(ctx, satelliteInfraConfig{TartHome: home})
		if err != nil {
			continue
		}
		for _, name := range group {
			vmName := strings.TrimPrefix(configured[name].ProviderRef, tartSatelliteTarget+":")
			out[name] = machineStateForTartVM(findTartVM(vms, vmName))
		}
	}
}

// machineStateForTartVM maps a `tart list` lookup (nil = not in the store) to a
// machine_state.
func machineStateForTartVM(vm *tartVM) string {
	switch {
	case vm == nil:
		return satelliteMachineAbsent
	case vm.Running:
		return satelliteMachineRunning
	default:
		return satelliteMachineStopped
	}
}

// probeDockerMachineStates fills out[name] for the given docker satellites from
// a single `docker ps -a`. A daemon error or timeout leaves them untouched
// (machine_state ""); status still succeeds.
func probeDockerMachineStates(configured map[string]satelliteConfigEntry, names []string, timeout time.Duration, out map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	states, err := dockerListContainerStates(ctx)
	if err != nil {
		return
	}
	for _, name := range names {
		container := strings.TrimPrefix(configured[name].ProviderRef, dockerSatelliteTarget+":")
		running, present := states[container]
		out[name] = machineStateForDockerContainer(running, present)
	}
}

// machineStateForDockerContainer maps a `docker ps -a` lookup to a
// machine_state.
func machineStateForDockerContainer(running, present bool) string {
	switch {
	case !present:
		return satelliteMachineAbsent
	case running:
		return satelliteMachineRunning
	default:
		return satelliteMachineStopped
	}
}
