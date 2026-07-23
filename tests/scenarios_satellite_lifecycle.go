package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/grovetools/tend/pkg/command"
	"github.com/grovetools/tend/pkg/harness"
	"github.com/grovetools/tend/pkg/verify"
)

// Satellite lifecycle E2E scenarios: the acceptance gate for the local
// exec-kind providers. Each scenario boots a REAL machine with
// `grove satellite up <name> --target tart|docker`, runs the existing
// satellite scenario suite against it in real mode (a `./bin/tend run`
// subprocess with TEND_SATELLITE_REAL=1 pointed at the sandbox state file),
// then `grove satellite down`s it and asserts zero residue — machine gone,
// state entry gone, provider dir gone.
//
// Gating (two layers, both pass-with-NOTICE per the suite idiom):
//   - Opt-in: TEND_SATELLITE_LIFECYCLE=1. Booting VMs/containers and a
//     multi-minute cross-build must never happen under a default `tend run`
//     (including test-smart hook runs, which select scenarios BY NAME) — so
//     without the env the scenario is a cheap NOTICE pass.
//   - Provider availability, mirroring the provider's own PrepareUp gate:
//     tart needs an Apple Silicon macOS host with tart on PATH; docker needs
//     the CLI AND a reachable daemon (`docker info` exit 0).
//
// Locking: the lifecycle scenarios serialize among themselves on a DEDICATED
// flock (they share the host's tart/docker stores and the Go build cache).
// They deliberately do NOT touch the satellite scenario lock
// (grove-satellite-e2e.lock): the inner real-mode suite acquires that one
// per scenario, so holding it here would deadlock the inner run.
//
// Recursion guard: the inner run selects the suite scenarios by EXACT name
// (every "satellite"-tagged scenario without the satellite-lifecycle tag),
// so a lifecycle scenario can never select itself; TEND_SATELLITE_LIFECYCLE
// is additionally cleared in the inner environment as belt-and-braces.
//
// Sandboxing: `up` runs via ctx.Bin, so the state entry, the provider dir,
// and the infra config write-back all land in the scenario sandbox — the
// user's real satellite registry is never read or written. Only the
// provider-level stores are real and shared (tart VM store / docker daemon),
// which is exactly what the residue assertions cover. The shared image
// caches (tart OCI cache, docker grove-satellite:<hash> image) are NOT
// asserted on and never deleted — they are warm-start caches by design.

const (
	// satLifecycleEnv is the opt-in gate: unset/anything-but-1 means the
	// lifecycle scenarios pass-with-NOTICE without touching any provider.
	satLifecycleEnv     = "TEND_SATELLITE_LIFECYCLE"
	satFullLifecycleEnv = "TEND_FULL_TART_LIFECYCLE"
	// satelliteLifecycleTag marks the lifecycle scenarios so the inner-run
	// selection (satellite tag minus this tag) can never recurse.
	satelliteLifecycleTag = "satellite-lifecycle"

	satKeyLcLock    = "sat_lc_lock"
	satKeyLcName    = "sat_lc_name"
	satKeyLcRoot    = "sat_lc_root"
	satKeyLcEnv     = "sat_lc_env"
	satKeyLcUpTried = "sat_lc_up_tried"
	satKeyLcDowned  = "sat_lc_downed"
)

// SatelliteTartLifecycleScenario: full up → real-mode suite → down cycle
// against a local tart VM (Apple Silicon hosts).
func SatelliteTartLifecycleScenario() *harness.Scenario {
	return satelliteLifecycleScenario("tart", false)
}

// SatelliteTartFullLifecycleScenario is a separately gated Phase-1 run using
// the fixed external Tart store and the unadvertised experiment gate.
func SatelliteTartFullLifecycleScenario() *harness.Scenario {
	return satelliteLifecycleScenario("tart", true)
}

// SatelliteDockerLifecycleScenario: the same cycle against a local docker
// container (any host with a reachable docker daemon).
func SatelliteDockerLifecycleScenario() *harness.Scenario {
	return satelliteLifecycleScenario("docker", false)
}

func satelliteLifecycleScenario(target string, full bool) *harness.Scenario {
	steps := []harness.Step{
		harness.NewStep("Gate: lifecycle opt-in + provider availability", func(ctx *harness.Context) error {
			gate := satLifecycleEnv
			if full {
				gate = satFullLifecycleEnv
			}
			if os.Getenv(gate) != "1" {
				reason := fmt.Sprintf("lifecycle scenario boots a real %s machine — set %s=1 to opt in", target, gate)
				ctx.Set(satKeySkip, reason)
				fmt.Printf("NOTICE: satellite lifecycle scenario skipped — %s\n", reason)
				return nil
			}
			if reason := satelliteLifecycleProviderGate(ctx, target); reason != "" {
				ctx.Set(satKeySkip, reason)
				fmt.Printf("NOTICE: satellite lifecycle scenario skipped — %s\n", reason)
				return nil
			}
			lock, err := acquireFlock(satelliteLifecycleLockPath())
			if err != nil {
				return err
			}
			ctx.Set(satKeyLcLock, lock)
			root, err := os.Getwd()
			if err != nil {
				return err
			}
			ctx.Set(satKeyLcRoot, root)
			// A distinct name keeps the lifecycle machines (and their state
			// entries, were the sandbox ever misconfigured) clearly apart
			// from any real satellite the user runs.
			name := "sat-e2e-" + target
			env := satelliteLifecycleUpEnv(target)
			if full {
				name += "-full"
				env = append(env, "GROVE_EXPERIMENTAL_FULL_TART=1", "TART_HOME=/Volumes/solot7/tart")
			}
			ctx.Set(satKeyLcName, name)
			ctx.Set(satKeyLcEnv, env)
			return nil
		}),
		satStep("Provision the satellite (grove satellite up)", func(ctx *harness.Context) error {
			name := ctx.GetString(satKeyLcName)
			ctx.Set(satKeyLcUpTried, true)
			// --sync-workspaces "" empties the exec-kind mirror set: the
			// inner suite ships its own fixture repos, so mirroring the real
			// ecosystem would only add minutes of bundle traffic.
			upArgs := []string{"satellite", "up", name, "--target", target, "--yes", "--sync-workspaces", ""}
			if full {
				upArgs = append(upArgs, "--kind", "full", "--tart-home", "/Volumes/solot7/tart")
			}
			cmd := ctx.Bin(upArgs...)
			cmd.Dir(ctx.GetString(satKeyLcRoot))
			cmd.Env(ctx.GetStringSlice(satKeyLcEnv)...)
			cmd.Timeout(30 * time.Minute)
			result := cmd.Run()
			ctx.ShowCommandOutput("grove satellite up "+name+" --target "+target, result.Stdout, result.Stderr)
			if err := ctx.Check("satellite up succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("up installs the prebuilt stack", result.Stdout, "Prebuilt grove stack installed")
				v.Contains("up reports the provisioned satellite", result.Stdout, fmt.Sprintf("Satellite %q provisioned at", name))
			})
		}),
		satStep("State entry records the machine (exec kind, provider_ref, identity)", func(ctx *harness.Context) error {
			name := ctx.GetString(satKeyLcName)
			entry, ok, err := readLifecycleStateEntry(ctx, name)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("no %q entry in the sandbox satellites.json after up", name)
			}
			keyPath := filepath.Join(ctx.StateDir(), "grove", "satellites", name, target, "id_ed25519")
			_, keyErr := os.Stat(keyPath)
			return ctx.Verify(func(v *verify.Collector) {
				wantKind := "exec"
				if full {
					wantKind = "" // full is normalized to omitted/default
				}
				v.Equal("state entry has resolved kind", wantKind, entry.Kind)
				v.Equal("provider_ref marks the machine grove-created", target+":grove-sat-"+name, entry.ProviderRef)
				v.True("ssh_addr recorded", entry.SSHAddr != "")
				v.True("host_key pinned", entry.HostKey != "")
				v.Equal("identity_file is the provider-dir key", keyPath, entry.IdentityFile)
				v.True("provider identity key exists on disk", keyErr == nil)
			})
		}),
		satStep("Full Tart services survive a guest reboot", func(ctx *harness.Context) error {
			if !full {
				return nil
			}
			name := ctx.GetString(satKeyLcName)
			root := ctx.GetString(satKeyLcRoot)
			env := ctx.GetStringSlice(satKeyLcEnv)
			check := func() *command.Result {
				cmd := ctx.Bin("satellite", "exec", name, "--", "bash", "-lc", "sudo systemctl is-active grove-syncd && systemctl --user is-active groved && test -S /run/user/$(id -u)/grove/groved.sock")
				cmd.Dir(root)
				cmd.Env(env...)
				cmd.Timeout(30 * time.Second)
				return cmd.Run()
			}
			before := check()
			if err := ctx.Check("both full services are active before reboot", before.AssertSuccess()); err != nil {
				return err
			}
			reboot := ctx.Bin("satellite", "exec", name, "--", "sudo", "reboot")
			reboot.Dir(root)
			reboot.Env(env...)
			reboot.Timeout(30 * time.Second)
			_ = reboot.Run() // ssh normally exits nonzero while the guest drops
			deadline := time.Now().Add(3 * time.Minute)
			var after *command.Result
			for time.Now().Before(deadline) {
				time.Sleep(5 * time.Second)
				after = check()
				if after.Error == nil && after.ExitCode == 0 {
					return nil
				}
			}
			if after == nil {
				return fmt.Errorf("no post-reboot service probe ran")
			}
			return fmt.Errorf("full Tart services did not recover after reboot: exit %d: %s", after.ExitCode, strings.TrimSpace(after.Stdout+after.Stderr))
		}),
		satStep("Real-mode satellite suite passes against the live machine", func(ctx *harness.Context) error {
			name := ctx.GetString(satKeyLcName)
			root := ctx.GetString(satKeyLcRoot)
			names := satelliteInnerSuiteNames()
			if len(names) == 0 {
				return fmt.Errorf("no inner satellite scenarios found — tag filter broken?")
			}
			args := append([]string{"run"}, names...)
			args = append(args, "--timeout", "30m")
			// ctx.Command sandboxes the inner tend's env (HOME + XDG dirs),
			// so its real-mode provider resolves paths.StateDir() to THIS
			// scenario's sandbox state — the satellites.json `up` just wrote.
			cmd := ctx.Command(filepath.Join(root, "bin", "tend"), args...)
			cmd.Dir(root)
			cmd.Env(
				"TEND_SATELLITE_REAL=1",
				"TEND_SATELLITE_NAME="+name,
				satLifecycleEnv+"=", // never recurse, even if selection changes
				"GROVE_HOME=",       // state must resolve via the sandbox XDG_STATE_HOME
			)
			cmd.Timeout(45 * time.Minute)
			result := cmd.Run()
			// The inner run IS the acceptance evidence — always surface it
			// in the outer output (ShowCommandOutput is verbosity-gated).
			fmt.Printf("--- inner `tend run` output (real mode, %d scenarios) ---\n%s%s--- end inner output ---\n",
				len(names), result.Stdout, result.Stderr)
			if err := ctx.Check("inner real-mode suite exits green", result.AssertSuccess()); err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("every inner scenario passed", result.Stdout, fmt.Sprintf("All %d scenario(s) passed", len(names)))
				// Prove the inner run really was REAL mode, not the sim: the
				// sim-only scenarios pass-with-NOTICE under
				// TEND_SATELLITE_REAL=1 and only there — a sim-mode inner run
				// (e.g. the env failing to propagate) never prints this.
				v.Contains("inner run drove the real endpoint (sim-only scenarios skipped)",
					result.Stdout, "sim-only scenario — skipped under TEND_SATELLITE_REAL=1")
				v.NotContains("no inner scenario skipped for an unreachable satellite", result.Stdout, "not reachable over ssh")
				v.NotContains("no inner scenario skipped for a missing registry entry", result.Stdout, "is not in")
			})
		}),
		satStep("Destroy the satellite (grove satellite down)", func(ctx *harness.Context) error {
			name := ctx.GetString(satKeyLcName)
			cmd := ctx.Bin("satellite", "down", name, "--target", target, "--yes")
			cmd.Dir(ctx.GetString(satKeyLcRoot))
			cmd.Env(ctx.GetStringSlice(satKeyLcEnv)...)
			cmd.Timeout(10 * time.Minute)
			result := cmd.Run()
			ctx.ShowCommandOutput("grove satellite down "+name, result.Stdout, result.Stderr)
			if err := ctx.Check("satellite down succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			ctx.Set(satKeyLcDowned, true)
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("down deregisters the satellite", result.Stdout, fmt.Sprintf("Satellite %q destroyed and deregistered", name))
			})
		}),
		satStep("Zero residue: machine, state entry, and provider dir all gone", func(ctx *harness.Context) error {
			name := ctx.GetString(satKeyLcName)
			_, entryStillThere, err := readLifecycleStateEntry(ctx, name)
			if err != nil {
				return err
			}
			provDir := filepath.Join(ctx.StateDir(), "grove", "satellites", name, target)
			_, dirErr := os.Stat(provDir)
			machineListed, runnerAlive, err := satelliteLifecycleMachinePresent(ctx, target, name)
			if err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.True("state entry removed", !entryStillThere)
				v.True("provider dir removed ("+provDir+")", os.IsNotExist(dirErr))
				v.True("machine gone from the provider store", !machineListed)
				v.True("no runner process left behind", !runnerAlive)
			})
		}),
	}
	scenarioName, gate := "satellite-lifecycle-"+target, satLifecycleEnv
	if full {
		scenarioName += "-full"
		gate = satFullLifecycleEnv
	}
	return harness.NewScenario(
		scenarioName,
		fmt.Sprintf("Boots a real %s satellite, passes the real-mode satellite suite against it, and downs it to zero residue (opt-in via %s=1)", target, gate),
		[]string{"satellite", satelliteLifecycleTag, "slow"},
		steps,
	).WithTeardown(
		harness.NewStep("Lifecycle teardown (best-effort down + lock release)", func(ctx *harness.Context) error {
			if ctx.GetBool(satKeyLcUpTried) && !ctx.GetBool(satKeyLcDowned) {
				name := ctx.GetString(satKeyLcName)
				fmt.Printf("(lifecycle teardown: running best-effort `grove satellite down %s --target %s`)\n", name, target)
				cmd := ctx.Bin("satellite", "down", name, "--target", target, "--yes")
				cmd.Dir(ctx.GetString(satKeyLcRoot))
				cmd.Env(ctx.GetStringSlice(satKeyLcEnv)...)
				cmd.Timeout(10 * time.Minute)
				if result := cmd.Run(); result.Error != nil || result.ExitCode != 0 {
					fmt.Printf("(lifecycle teardown: best-effort down failed (exit %d): %s)\n", result.ExitCode, strings.TrimSpace(result.Stdout+result.Stderr))
				}
			}
			if ctx.HasKey(satKeyLcLock) {
				releaseSatelliteLock(ctx.Get(satKeyLcLock).(*os.File))
			}
			return nil
		}),
	)
}

// satelliteLifecycleProviderGate mirrors the provider's own PrepareUp
// availability checks; a non-empty return is the pass-with-NOTICE reason
// naming the missing prerequisite.
func satelliteLifecycleProviderGate(ctx *harness.Context, target string) string {
	switch target {
	case "tart":
		if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
			return fmt.Sprintf("the tart target needs an Apple Silicon macOS host (this host is %s/%s)", runtime.GOOS, runtime.GOARCH)
		}
		if _, err := exec.LookPath("tart"); err != nil {
			return "tart not found on PATH (`brew install cirruslabs/cli/tart`)"
		}
	case "docker":
		if _, err := exec.LookPath("docker"); err != nil {
			return "docker CLI not found on PATH"
		}
		// The CLI existing is NOT enough — the gate is a live daemon, probed
		// exactly as the provider's PrepareUp does (`docker info`), through
		// ctx.Command so the same DOCKER_HOST detection applies to the probe
		// as to the `up` under test.
		if result := ctx.Command("docker", "info").Timeout(30 * time.Second).Run(); result.Error != nil || result.ExitCode != 0 {
			return "docker daemon unreachable (`docker info` failed) — start Docker Desktop or colima"
		}
	}
	return ""
}

// satelliteLifecycleUpEnv is the extra environment for the sandboxed
// `up`/`down` invocations. The sandbox HOME would otherwise give the
// implied-prebuilt cross-build cold Go/zig caches (and tart an empty VM/image
// store), so the host's real caches are passed through explicitly.
func satelliteLifecycleUpEnv(target string) []string {
	var env []string
	realHome, _ := os.UserHomeDir() // the harness process is unsandboxed
	if target == "tart" {
		tartHome := os.Getenv("TART_HOME")
		if tartHome == "" && realHome != "" {
			tartHome = filepath.Join(realHome, ".tart")
		}
		if tartHome != "" {
			env = append(env, "TART_HOME="+tartHome)
		}
	}
	for _, v := range []string{"GOCACHE", "GOMODCACHE"} {
		if out, err := exec.Command("go", "env", v).Output(); err == nil {
			if p := strings.TrimSpace(string(out)); p != "" {
				env = append(env, v+"="+p)
			}
		}
	}
	if out, err := exec.Command("zig", "env").Output(); err == nil {
		var zigEnv struct {
			GlobalCacheDir string `json:"global_cache_dir"`
		}
		if json.Unmarshal(out, &zigEnv) == nil && zigEnv.GlobalCacheDir != "" {
			env = append(env, "ZIG_GLOBAL_CACHE_DIR="+zigEnv.GlobalCacheDir)
		}
	}
	return env
}

// satelliteInnerSuiteNames selects the inner real-mode suite by exact name:
// every satellite-tagged scenario EXCEPT the lifecycle scenarios themselves
// — the recursion guard, and the stable definition of "the existing satellite
// suite" as it grows.
func satelliteInnerSuiteNames() []string {
	var names []string
	for _, s := range AllScenarios() {
		satellite, lifecycle := false, false
		for _, t := range s.Tags {
			switch t {
			case "satellite":
				satellite = true
			case satelliteLifecycleTag:
				lifecycle = true
			}
		}
		if satellite && !lifecycle {
			names = append(names, s.Name)
		}
	}
	return names
}

// satelliteLifecycleStateEntry is the slice of a satellites.json state entry
// (cmd/satellite.go satelliteConfigEntry) the lifecycle assertions read.
type satelliteLifecycleStateEntry struct {
	SSHAddr      string `json:"ssh_addr"`
	HostKey      string `json:"host_key"`
	IdentityFile string `json:"identity_file"`
	Kind         string `json:"kind"`
	ProviderRef  string `json:"provider_ref"`
}

// readLifecycleStateEntry reads the sandbox satellites.json (the file the
// sandboxed `up` writes via its XDG_STATE_HOME) and returns name's entry.
func readLifecycleStateEntry(ctx *harness.Context, name string) (satelliteLifecycleStateEntry, bool, error) {
	var zero satelliteLifecycleStateEntry
	data, err := os.ReadFile(filepath.Join(ctx.StateDir(), "grove", "satellites.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return zero, false, nil
		}
		return zero, false, err
	}
	var state struct {
		Satellites map[string]satelliteLifecycleStateEntry `json:"satellites"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return zero, false, err
	}
	entry, ok := state.Satellites[name]
	return entry, ok, nil
}

// satelliteLifecycleMachinePresent probes the provider store for the
// satellite's machine (tart VM / docker container) and, for tart, whether a
// detached `tart run` process for it is still alive.
func satelliteLifecycleMachinePresent(ctx *harness.Context, target, name string) (machineListed, runnerAlive bool, err error) {
	machine := "grove-sat-" + name
	switch target {
	case "tart":
		cmd := exec.Command("tart", "list", "--format", "json")
		cmd.Env = os.Environ()
		for _, kv := range ctx.GetStringSlice(satKeyLcEnv) {
			if strings.HasPrefix(kv, "TART_HOME=") {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		out, lerr := cmd.Output()
		if lerr != nil {
			return false, false, fmt.Errorf("tart list: %w", lerr)
		}
		var vms []struct {
			Name   string `json:"Name"`
			Source string `json:"Source"`
		}
		if err := json.Unmarshal(out, &vms); err != nil {
			return false, false, fmt.Errorf("parse tart list: %w", err)
		}
		for _, vm := range vms {
			if vm.Source == "local" && vm.Name == machine {
				machineListed = true
			}
		}
		// pgrep exits 1 when nothing matches — that is the wanted outcome.
		if out, perr := exec.Command("pgrep", "-fl", "tart run").Output(); perr == nil {
			runnerAlive = strings.Contains(string(out), machine)
		}
		return machineListed, runnerAlive, nil
	case "docker":
		result := ctx.Command("docker", "ps", "-a", "--format", "{{.Names}}").Timeout(30 * time.Second).Run()
		if result.Error != nil || result.ExitCode != 0 {
			return false, false, fmt.Errorf("docker ps -a: exit %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
		}
		for _, line := range strings.Split(result.Stdout, "\n") {
			if strings.TrimSpace(line) == machine {
				machineListed = true
			}
		}
		return machineListed, false, nil
	}
	return false, false, fmt.Errorf("unknown lifecycle target %q", target)
}

// satelliteLifecycleLockPath serializes the lifecycle scenarios among
// themselves — a DEDICATED lock, distinct from the satellite scenario lock
// the inner real-mode suite acquires (sharing that one would deadlock).
func satelliteLifecycleLockPath() string {
	return filepath.Join(writableTmpBase(), "grove-satellite-lifecycle-e2e.lock")
}
