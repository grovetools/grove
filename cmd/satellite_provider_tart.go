package cmd

// The tart satellite provider — local Apple-Silicon VMs via the tart CLI
// (github.com/cirruslabs/tart), linux guests only in this slice. Unlike gcp
// there is no terraform: the provider clones a cached OCI image, runs the VM
// detached, and performs a layer-0 auth bootstrap
// (dedicated ed25519 key installed over the image's default password auth,
// which is then disabled), after which the shared `up` verb provisions the
// exec-kind satellite client-side from a locally cross-built stack
// (implied --prebuilt).
//
// Empirically pinned tart facts this file relies on (hands-on spike):
//   - `tart stop` is effectively a hard poweroff (returns in ~0.1s regardless
//     of --timeout; unsynced guest writes from the last ~minute are silently
//     lost). Graceful down therefore goes over ssh: `sync; sudo poweroff`,
//     then wait for the VM to reach "stopped" in `tart list`. Any guest-state
//     mutation ends with `sync` before a stop can be trusted.
//   - the detached `tart run --no-graphics` process IS the VM: it exits 0
//     when the guest powers off.
//   - the sshd host key is baked into the image, so it is identical across
//     clones of one image pull — pinning it (C2) still works, it just does
//     not distinguish two clones of the same image.
//   - the image's sshd_config.d is first-match-wins and ships
//     50-cloud-init.conf with `PasswordAuthentication yes`, so the grove
//     drop-in must sort FIRST (00-grove.conf); a 99- file silently loses.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/grovetools/grove/pkg/satellitecontract"
)

// tartSatelliteTarget is the infra target name the provider registers under.
const tartSatelliteTarget = "tart"

// Full Tart is selectable through --kind full now that destructive record
// return is generation-bound and fail-closed; exec remains the target default.
// defaultTartImage is the guest image cloned when neither --image nor
// [satellites.<name>.infra] image is set: Ubuntu 24.04 arm64, boots to sshd
// in ~15s with default creds admin/admin, passwordless sudo, git
// preinstalled.
const defaultTartImage = "ghcr.io/cirruslabs/ubuntu:latest"

// tartGuestUser/tartGuestPassword are the cirruslabs images' default
// credentials — the password is only ever used for the layer-0 bootstrap,
// which ends with password auth disabled.
const (
	tartGuestUser     = "admin"
	tartGuestPassword = "admin"
)

// tartVMNamePrefix namespaces the VMs grove creates in the shared tart store.
const tartVMNamePrefix = "grove-sat-"

// tartVMName is the local tart VM name for a satellite.
func tartVMName(satName string) string { return tartVMNamePrefix + satName }

// isGroveManagedTartVMImage identifies local Grove satellite VMs. Grove cannot
// police direct tart or host-backup operations, but it must never itself clone
// a potentially hydrated/authenticated guest as a new full satellite.
func isGroveManagedTartVMImage(image string) bool {
	return strings.HasPrefix(filepath.Clean(image), tartVMNamePrefix)
}

// tartProviderRef is the provider_ref state value marking a VM as
// grove-created for a given satellite ("tart:<vm-name>").
func tartProviderRef(vmName string) string { return tartSatelliteTarget + ":" + vmName }

// tartSatelliteProvider provisions local tart VMs.
type tartSatelliteProvider struct {
	target string
	// image is the resolved guest image, set by PrepareUp for Up.
	image string
}

// newTartSatelliteProvider is the registry constructor for the "tart" target.
func newTartSatelliteProvider(target string) satelliteProvider {
	return &tartSatelliteProvider{target: target}
}

func (p *tartSatelliteProvider) Kind() string { return p.target }

// DefaultSatelliteKind: tart satellites are exec-only endpoints (sshd + grove
// binaries, no groved dial or sync wiring) — the full stack needs the
// bootstrap script, which this provider does not run (a later slice).
func (p *tartSatelliteProvider) DefaultSatelliteKind() string { return satelliteKindExec }

// UsesBootstrapScript is kind-aware: exec remains client-side/prebuilt only;
// experimental full runs the shared full-node bootstrap after Tart guest prep.
func (p *tartSatelliteProvider) UsesBootstrapScript(kind string) bool {
	return kind == satelliteKindFull
}

// DefaultPrebuiltTarget: the tart linux guest's arch — Apple Silicon hosts
// run arm64 guests (the value the shared verb hardcoded before the hook).
func (p *tartSatelliteProvider) DefaultPrebuiltTarget() (string, error) {
	return "linux/arm64", nil
}

// PrepareUp validates the host environment (tart on PATH, Apple Silicon
// macOS) and resolves the guest image. Read-only: the only tart invocation is
// `tart list`, to warn about a multi-GB pull when the image is uncached.
func (p *tartSatelliteProvider) PrepareUp(opts *satelliteUpOptions) error {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return fmt.Errorf("the %q satellite target needs an Apple Silicon macOS host (tart uses Virtualization.framework; this host is %s/%s)", p.target, runtime.GOOS, runtime.GOARCH)
	}
	if _, err := exec.LookPath("tart"); err != nil {
		return fmt.Errorf("tart not found on PATH — install it with `brew install cirruslabs/cli/tart`: %w", err)
	}
	if opts.SatelliteKind == satelliteKindFull {
		if filepath.Clean(expandUserPath(opts.Infra.TartHome)) != satellitecontract.ExpectedTartHome {
			return fmt.Errorf("full Tart requires --tart-home %s (no environment/default fallback)", satellitecontract.ExpectedTartHome)
		}
		opts.Infra.TartHome = satellitecontract.ExpectedTartHome
		facts, err := inspectTartHostVolume()
		if err != nil {
			return fmt.Errorf("full Tart host preflight: %w", err)
		}
		expectedIdentity := opts.Infra.TartVolumeIdentity
		if expectedIdentity == "" {
			expectedIdentity = facts.MountIdentity
		}
		if err := satellitecontract.ValidateTartVolume(facts, expectedIdentity, opts.CapacityPlan.Host); err != nil {
			return fmt.Errorf("full Tart host preflight (before clone): %w", err)
		}
		opts.Infra.TartVolumeIdentity = expectedIdentity
	} else {
		opts.Infra.TartHome = resolveTartHome(opts.Infra.TartHome)
	}
	p.image = opts.Infra.Image
	if p.image == "" {
		p.image = defaultTartImage
	}
	if opts.SatelliteKind == satelliteKindFull && isGroveManagedTartVMImage(p.image) {
		return fmt.Errorf("full Tart image %q names a Grove-managed satellite VM; authenticated or hydrated guests must never be clone sources — use a credential-free base image", p.image)
	}
	// Best-effort pull-size warning: an uncached image means a multi-GB OCI
	// pull on the first `up` (the ubuntu image is ~3.0GB compressed).
	if vms, err := tartList(opts.Infra); err == nil {
		cached := false
		for _, vm := range vms {
			if vm.Source == "OCI" && vm.Name == p.image {
				cached = true
				break
			}
		}
		if !cached {
			fmt.Printf("note: image %q is not in the local tart cache — `up` will pull it first (multi-GB download).\n", p.image)
		}
	}
	return nil
}

// Up creates (or restarts) the satellite's local VM and returns its endpoint.
// No billable-confirm prompt: a local VM costs nothing to create and `down`
// removes it entirely. Steps: clone-if-missing (the OCI pull happens
// implicitly) → detached `tart run --no-graphics` → poll `tart ip` + TCP :22
// → layer-0 auth bootstrap (dedicated key in, password auth off) → endpoint.
func (p *tartSatelliteProvider) Up(ctx context.Context, opts *satelliteUpOptions) (satelliteEndpoint, error) {
	if p.image == "" {
		return satelliteEndpoint{}, fmt.Errorf("tart satellite provider: Up called without PrepareUp")
	}
	full := opts.SatelliteKind == satelliteKindFull
	// Shared fail-fast work (the verb resolves nothing here for tart today,
	// but honor the contract).
	if opts.PostConfirm != nil {
		if err := opts.PostConfirm(); err != nil {
			return satelliteEndpoint{}, err
		}
	}

	vmName := tartVMName(opts.Name)
	vms, err := tartList(opts.Infra)
	if err != nil {
		return satelliteEndpoint{}, err
	}
	existing := findTartVM(vms, vmName)
	if existing != nil {
		// A VM of our name already exists: restart it only when the state
		// file says WE created it for this satellite; otherwise it is a
		// collision with something else in the user's tart store.
		entries, serr := loadSatelliteState()
		if serr != nil || entries[opts.Name].ProviderRef != tartProviderRef(vmName) {
			return satelliteEndpoint{}, fmt.Errorf(
				"tart VM %q already exists but is not recorded as satellite %q's VM in the grove state file — refusing to adopt it (delete it with `tart delete %s` or pick another satellite name)",
				vmName, opts.Name, vmName)
		}
		if entries[opts.Name].effectiveKind() != opts.SatelliteKind {
			return satelliteEndpoint{}, fmt.Errorf("tart VM %q is recorded as kind %q, not requested kind %q — refusing an in-place kind conversion; destroy and recreate it", vmName, entries[opts.Name].effectiveKind(), opts.SatelliteKind)
		}
		if existing.Running {
			fmt.Printf("Local tart VM %q is already running — reusing it.\n", vmName)
		} else {
			fmt.Printf("Restarting local tart VM %q...\n", vmName)
			if err := p.startVM(opts, vmName); err != nil {
				return satelliteEndpoint{}, err
			}
		}
	} else {
		fmt.Printf("Creating local tart VM %q from %s (CoW clone; uncached images are pulled first)...\n", vmName, p.image)
		if out, err := tartCommand(opts.Infra, "clone", p.image, vmName).CombinedOutput(); err != nil {
			return satelliteEndpoint{}, fmt.Errorf("tart clone %s %s: %w: %s", p.image, vmName, err, strings.TrimSpace(string(out)))
		}
		// Stamp provider_ref into the state file the moment the VM exists:
		// if a later `up` step fails (cross-build, ship), the re-run's
		// ours-check above must still recognize the VM, and `down` must be
		// able to find it. The shared verb overwrites this partial entry
		// with the full one on success and removes it on `down`.
		entries, serr := loadSatelliteState()
		if serr != nil {
			_ = tartCommand(opts.Infra, "delete", vmName).Run()
			return satelliteEndpoint{}, fmt.Errorf("load partial-up state after clone (VM deleted): %w", serr)
		}
		stamp := entries[opts.Name]
		stamp.ProviderRef = tartProviderRef(vmName)
		stamp.ProviderTartHome = opts.Infra.TartHome
		stamp.Kind = opts.SatelliteKind
		if err := upsertSatelliteState(opts.Name, stamp); err != nil {
			_ = tartCommand(opts.Infra, "delete", vmName).Run()
			return satelliteEndpoint{}, fmt.Errorf("persist provider_ref and TART_HOME immediately after clone (VM deleted): %w", err)
		}
		if err := p.startVM(opts, vmName); err != nil {
			return satelliteEndpoint{}, err
		}
	}

	logPath, _ := tartRunLogPath(opts.Name)
	ip, err := waitForTartVMSSHPort(ctx, opts.Infra, vmName, logPath, 2*time.Minute)
	if err != nil {
		return satelliteEndpoint{}, err
	}

	// Layer-0 auth bootstrap: dedicated per-satellite key, host key pinned
	// from the very first connection (the key is baked into the image, so
	// keyscan-then-pin is deterministic across clones).
	keyPath, err := ensureTartSatelliteKey(opts.Name)
	if err != nil {
		return satelliteEndpoint{}, err
	}
	hostKey, err := sshKeyscanHostKey(ip)
	if err != nil {
		return satelliteEndpoint{}, fmt.Errorf("ssh-keyscan host-key pin: %w", err)
	}
	if full {
		fmt.Println("warning: this Tart image carries clone-shared SSH host keys; Grove pins the key, but clone identity is not yet unique")
	}
	if err := tartLayer0Bootstrap(ip, hostKey, keyPath, opts.Name, full); err != nil {
		return satelliteEndpoint{}, fmt.Errorf("layer-0 ssh bootstrap of %s: %w", vmName, err)
	}

	return satelliteEndpoint{
		SSHAddr:      ip + ":22",
		User:         tartGuestUser,
		IdentityFile: keyPath,
		ProviderRef:  tartProviderRef(vmName),
	}, nil
}

// Down tears the VM down: destroy confirm, PostConfirm, graceful guest
// poweroff over ssh (per the pinned tart facts — `tart stop` is a hard
// poweroff and only a fallback for unreachable guests), `tart delete`, and
// removal of the provider's local files (keys, run log). The registry/state
// entry removal is the shared verb's job.
func (p *tartSatelliteProvider) Down(ctx context.Context, opts *satelliteDownOptions) error {
	if _, err := exec.LookPath("tart"); err != nil {
		return fmt.Errorf("tart not found on PATH — install it with `brew install cirruslabs/cli/tart`: %w", err)
	}
	if opts.Infra.TartHome == "" {
		if entry := loadMergedSatellites()[opts.Name]; entry.ProviderTartHome != "" {
			opts.Infra.TartHome = entry.ProviderTartHome
		}
	}
	opts.Infra.TartHome = resolveTartHome(opts.Infra.TartHome)

	// The registry entry (config ∪ state) supplies the pinned ssh transport
	// for the graceful poweroff and the recorded VM name.
	entry, hasEntry := loadMergedSatellites()[opts.Name]
	vmName := tartVMName(opts.Name)
	if hasEntry && strings.HasPrefix(entry.ProviderRef, tartSatelliteTarget+":") {
		vmName = strings.TrimPrefix(entry.ProviderRef, tartSatelliteTarget+":")
	}

	if !opts.AssumeYes {
		if err := confirmOrAbort(fmt.Sprintf("Delete local tart VM %q and remove satellite %q's registry entry?", vmName, opts.Name)); err != nil {
			return err
		}
	}
	if opts.PostConfirm != nil {
		if err := opts.PostConfirm(); err != nil {
			return err
		}
	}

	vms, err := tartList(opts.Infra)
	if err != nil {
		return err
	}
	vm := findTartVM(vms, vmName)
	if vm == nil {
		fmt.Printf("(no local tart VM %q — nothing to delete; cleaning up provider files)\n", vmName)
	} else {
		if vm.Running {
			if err := stopTartVMGracefully(opts.Infra, entry, hasEntry, vmName); err != nil {
				return err
			}
		}
		if out, err := tartCommand(opts.Infra, "delete", vmName).CombinedOutput(); err != nil {
			return fmt.Errorf("tart delete %s: %w: %s", vmName, err, strings.TrimSpace(string(out)))
		}
		fmt.Printf("Local tart VM %q deleted.\n", vmName)
	}

	// Provider-local files (dedicated key, run log). The image cache is
	// shared across satellites and stays.
	if dir, derr := tartProviderDir(opts.Name); derr == nil {
		_ = os.RemoveAll(dir)
	}
	return nil
}

// stopTartVMGracefully powers the guest off over the pinned ssh transport
// (`sync; sudo poweroff`) and waits for the VM to reach "stopped" — the
// detached `tart run` process exits when the guest powers off. Only when the
// guest is unreachable (no registry entry, or ssh fails) does it fall back to
// `tart stop`, which is effectively a hard poweroff.
func stopTartVMGracefully(infra satelliteInfraConfig, entry satelliteConfigEntry, hasEntry bool, vmName string) error {
	graceful := false
	if hasEntry && entry.SSHAddr != "" && entry.HostKey != "" {
		tmpDir, err := os.MkdirTemp("", "grove-satellite-tart-down-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()
		if ssh, err := newSatelliteSSH(entry, tmpDir); err == nil {
			// `sync` first, separately: it proves reachability, and it makes
			// the poweroff safe (unsynced guest writes are lost on a hard
			// stop). The poweroff itself drops the connection, so its error
			// is expected and ignored.
			if err := ssh.runCommand("sync"); err == nil {
				fmt.Printf("Powering off guest %q gracefully (sync + poweroff over ssh)...\n", vmName)
				_ = ssh.runCommand("sudo poweroff")
				graceful = waitForTartVMStopped(infra, vmName, 90*time.Second)
			}
		}
	}
	if !graceful {
		fmt.Printf("warning: guest %q unreachable over ssh — falling back to `tart stop` (hard poweroff).\n", vmName)
		if out, err := tartCommand(infra, "stop", "--timeout", "30", vmName).CombinedOutput(); err != nil {
			return fmt.Errorf("tart stop %s: %w: %s", vmName, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// --- tart CLI plumbing ---

// tartHomeFlagHelp documents --tart-home, shared by up and down.
const tartHomeFlagHelp = "Storage root for the tart VMs grove creates (the TART_HOME env var; tart target only). 'up' records the effective value in [satellites.<name>.infra] tart_home so 'down' drives the same store — default: that block, else the environment's TART_HOME, else " + defaultTartHomeDir

// defaultTartHomeDir is tart's own default storage root. `up` records it
// explicitly when neither the infra block nor the environment names one, so
// every later tart invocation grove makes for the satellite targets the same
// store — a `down` shell that exports a DIFFERENT TART_HOME would otherwise
// find no VM, report "nothing to delete", and orphan it while `down` wipes the
// state entry.
const defaultTartHomeDir = "~/.tart"

// resolveTartHome resolves the effective TART_HOME for a tart satellite:
// --tart-home / the infra block, else the process environment, else tart's own
// default. Never empty, so the resolved value is always persistable.
func resolveTartHome(configured string) string {
	if configured != "" {
		return configured
	}
	if env := os.Getenv("TART_HOME"); env != "" {
		return env
	}
	return defaultTartHomeDir
}

// tartCommand builds a tart invocation, relocating tart's storage via
// TART_HOME when [satellites.<name>.infra] tart_home is set (otherwise the
// process environment — including any exported TART_HOME — is inherited).
func tartCommand(infra satelliteInfraConfig, args ...string) *exec.Cmd {
	cmd := exec.Command("tart", args...) //nolint:gosec // G204: internal/flag-derived args
	if infra.TartHome != "" {
		cmd.Env = append(os.Environ(), "TART_HOME="+expandUserPath(infra.TartHome))
	}
	return cmd
}

// tartCommandContext is tartCommand bound to ctx, so a read-only `tart list`
// can be capped by a deadline (the status-time machine-state probe) without
// touching the long-lived detached `tart run` path tartCommand also serves.
func tartCommandContext(ctx context.Context, infra satelliteInfraConfig, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "tart", args...) //nolint:gosec // G204: internal/flag-derived args
	if infra.TartHome != "" {
		cmd.Env = append(os.Environ(), "TART_HOME="+expandUserPath(infra.TartHome))
	}
	return cmd
}

// tartVM is one `tart list --format json` row (fields we consume).
type tartVM struct {
	Name    string `json:"Name"`
	Source  string `json:"Source"` // "local" (VMs) or "OCI" (image cache)
	State   string `json:"State"`  // "stopped" | "running" | ...
	Running bool   `json:"Running"`
}

// tartList lists VMs and cached images.
func tartList(infra satelliteInfraConfig) ([]tartVM, error) {
	// context.Background() never cancels, and .Output() always Waits, so this
	// keeps the pre-context behavior for the up/down callers exactly.
	return tartListContext(context.Background(), infra)
}

// tartListContext is tartList bound to ctx: the status-time machine-state probe
// caps it with a deadline so a slow `tart list` cannot stall status.
func tartListContext(ctx context.Context, infra satelliteInfraConfig) ([]tartVM, error) {
	out, err := tartCommandContext(ctx, infra, "list", "--format", "json").Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("tart list: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("tart list: %w", err)
	}
	var vms []tartVM
	if err := json.Unmarshal(out, &vms); err != nil {
		return nil, fmt.Errorf("parse tart list output: %w", err)
	}
	return vms, nil
}

// findTartVM returns the local (non-image) VM with the given name, or nil.
func findTartVM(vms []tartVM, name string) *tartVM {
	for i := range vms {
		if vms[i].Source == "local" && vms[i].Name == name {
			return &vms[i]
		}
	}
	return nil
}

// tartProviderDir is the provider's slice of the per-satellite state dir
// (<StateDir>/satellites/<name>/tart): the dedicated ssh keypair and the
// detached `tart run` log live here.
func tartProviderDir(satName string) (string, error) {
	return satelliteProviderStateDir(satName, tartSatelliteTarget)
}

// tartRunLogPath is the detached `tart run` process's log file.
func tartRunLogPath(satName string) (string, error) {
	dir, err := tartProviderDir(satName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tart-run.log"), nil
}

// startVM launches the VM detached: `tart run --no-graphics` in its own
// session with output to the run log. The process IS the VM — it stays alive
// for the VM's lifetime and exits 0 when the guest powers off, so it must
// outlive this CLI invocation (Setsid + Release).
func (p *tartSatelliteProvider) startVM(opts *satelliteUpOptions, vmName string) error {
	logPath, err := tartRunLogPath(opts.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = logf.Close() }()
	fmt.Fprintf(logf, "--- tart run %s (grove satellite up %s) %s ---\n", vmName, opts.Name, time.Now().Format(time.RFC3339))
	cmd := tartCommand(opts.Infra, "run", "--no-graphics", vmName)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start detached `tart run %s`: %w", vmName, err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("detach `tart run %s`: %w", vmName, err)
	}
	return nil
}

// waitForTartVMSSHPort polls `tart ip` for the guest's DHCP lease (quick; the
// IP is stable across restarts since the VM keeps its MAC) and then TCP :22
// until sshd answers. The ubuntu image boots to sshd in ~15s.
func waitForTartVMSSHPort(ctx context.Context, infra satelliteInfraConfig, vmName, logPath string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var ip string
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		out, err := tartCommand(infra, "ip", vmName).Output()
		if err == nil {
			if cand := strings.TrimSpace(string(out)); net.ParseIP(cand) != nil {
				ip = cand
				break
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("tart VM %q never reported an IP within %s — is it booting? (check the run log: %s)", vmName, timeout, logPath)
		}
		time.Sleep(2 * time.Second)
	}
	// Shared TCP wait for the remainder of the deadline (same poll cadence as
	// the pre-factor loop); the timeout error keeps tart's run-log hint.
	if err := waitForTCPPort(ctx, net.JoinHostPort(ip, "22"), time.Until(deadline)); err != nil {
		if ctx.Err() != nil {
			return "", err
		}
		return "", fmt.Errorf("tart VM %q (%s): port 22 never opened within %s (check the run log: %s)", vmName, ip, timeout, logPath)
	}
	return ip, nil
}

// waitForTartVMStopped polls `tart list` until the VM leaves the running
// state (the detached `tart run` exits when the guest powers off).
func waitForTartVMStopped(infra satelliteInfraConfig, vmName string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		vms, err := tartList(infra)
		if err == nil {
			vm := findTartVM(vms, vmName)
			if vm == nil || !vm.Running {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Second)
	}
}

// --- layer-0 auth bootstrap ---

// ensureTartSatelliteKey generates (once) the satellite's dedicated ed25519
// keypair under the provider dir and returns the private key path (0600).
func ensureTartSatelliteKey(satName string) (string, error) {
	dir, err := tartProviderDir(satName)
	if err != nil {
		return "", err
	}
	return ensureSatelliteProviderKey(dir, tartVMName(satName))
}

// tartLayer0Bootstrap installs the satellite's public key into the guest's
// authorized_keys and disables password auth, using golang.org/x/crypto/ssh
// with the image's default password AND the keyscanned host key pinned (never
// TOFU, C2). Idempotent: when the dedicated key already authenticates and the
// grove sshd drop-in is in place (a restarted VM), it does nothing.
func tartLayer0Bootstrap(ip, hostKey, keyPath, satName string, full bool) error {
	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return err
	}
	pubLine := strings.TrimSpace(string(pub))
	hk, _, _, _, err := gossh.ParseAuthorizedKey([]byte(hostKey))
	if err != nil {
		return fmt.Errorf("parse keyscanned host key: %w", err)
	}
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	signer, err := gossh.ParsePrivateKey(keyData)
	if err != nil {
		return fmt.Errorf("parse satellite key %s: %w", keyPath, err)
	}
	baseConfig := func(auth gossh.AuthMethod) *gossh.ClientConfig {
		return &gossh.ClientConfig{
			User:              tartGuestUser,
			Auth:              []gossh.AuthMethod{auth},
			HostKeyCallback:   gossh.FixedHostKey(hk),
			HostKeyAlgorithms: []string{hk.Type()},
			Timeout:           15 * time.Second,
		}
	}

	// Idempotency probe: full prep is complete only when its startup sentinel
	// exists; an exec-prepared clone must not satisfy a later full request.
	if client, err := gossh.Dial("tcp", ip+":22", baseConfig(gossh.PublicKeys(signer))); err == nil {
		defer func() { _ = client.Close() }()
		probe := "test -f /etc/ssh/sshd_config.d/00-grove.conf"
		if full {
			probe += " && test -f /var/lib/grove-satellite/startup-done"
		}
		if _, err := tartSSHOutput(client, probe); err == nil {
			return nil
		}
		return tartConfigureGuest(client, pubLine, full)
	}

	// Fresh VM: the image's default password is the only way in.
	client, err := gossh.Dial("tcp", ip+":22", baseConfig(gossh.Password(tartGuestPassword)))
	if err != nil {
		return fmt.Errorf("neither the satellite key nor the image's default credentials authenticated to %s (satellite %s): %w", ip, satName, err)
	}
	defer func() { _ = client.Close() }()
	fmt.Printf("Installing the satellite ssh key and disabling password auth on the guest...\n")
	return tartConfigureGuest(client, pubLine, full)
}

// tartConfigureGuest applies the layer-0 guest state: authorized_keys entry,
// the FIRST-sorting sshd drop-in disabling password auth, the minimal exec
// guest prep the gcp bootstrap normally provides (grove bin dir + login-shell
// PATH), and a final `sync` so a subsequent stop cannot lose the changes.
func tartConfigureGuest(client *gossh.Client, pubLine string, full bool) error {
	fullPrep := ""
	if full {
		fullPrep = `
# Full-node prerequisites. Keep this idempotent and write startup-done LAST:
# the shared bootstrap treats that sentinel as proof all prep completed.
sudo mkdir -p /var/lib/grove-satellite
sudo rm -f /var/lib/grove-satellite/startup-done
sudo env DEBIAN_FRONTEND=noninteractive apt-get update -qq
sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl jq git ca-certificates
sudo loginctl enable-linger "$(id -un)"
sudo install -d -m 0755 /var/lib/grove-satellite
printf 'full-tart-v1\n' | sudo tee /var/lib/grove-satellite/startup-done >/dev/null
`
	}
	script := fmt.Sprintf(`set -eu
umask 077
mkdir -p "$HOME/.ssh"
grep -qF '%[1]s' "$HOME/.ssh/authorized_keys" 2>/dev/null || printf '%%s\n' '%[1]s' >> "$HOME/.ssh/authorized_keys"
chmod 700 "$HOME/.ssh"
chmod 600 "$HOME/.ssh/authorized_keys"
# sshd_config.d is first-match-wins and the image ships 50-cloud-init.conf
# with "PasswordAuthentication yes" — this drop-in must sort FIRST (00-).
printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\n' | sudo tee /etc/ssh/sshd_config.d/00-grove.conf >/dev/null
sudo systemctl reload ssh
# Exec-satellite guest prep (the gcp bootstrap's equivalents): the grove bin
# dir the prebuilt install targets, and a login-shell PATH that includes it.
mkdir -p "$HOME/.local/share/grove/bin"
printf 'export PATH="$HOME/.local/share/grove/bin:$PATH"\n' | sudo tee /etc/profile.d/grove-satellite.sh >/dev/null
%[2]s
# tart stop is effectively a hard poweroff; sync so none of the above is lost.
sync
`, pubLine, fullPrep)
	if out, err := tartSSHOutput(client, script); err != nil {
		return fmt.Errorf("guest configuration script: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// tartSSHOutput runs one remote command/script over an established layer-0
// client, returning combined output.
func tartSSHOutput(client *gossh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer func() { _ = sess.Close() }()
	out, err := sess.CombinedOutput(cmd)
	return string(out), err
}
