package cmd

// Prebuilt-binaries bootstrap for `grove satellite up --prebuilt`: cross-build
// a FRESH satellite's grove stack on the laptop and install it over the pinned
// SSH connection, instead of the VM-side git clone + source build the default
// bootstrap does. The heavy lifting (cross-build via the grove build
// orchestrator, sha256-manifest + verify, temp+mv install, prebuilt-heads
// overlay) is reused verbatim from `satellite upgrade --prebuilt`
// (deploySatellitePrebuilt et al. in satellite_upgrade.go); this file only adds
// the fixed ship set, the systemd-unit shipping, and their fail-fast
// validation.

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// satellitePrebuiltStackRepos is the FIXED set of repos a prebuilt `up` ships
// to make a healthy sync+federation satellite. It is exactly the binaries the
// source bootstrap guarantees — grove/groved/flow/nb/treemux/tuimux (step 4's
// required set) plus grove-syncd (step 6) — mapped to the repos that produce
// them: groved comes from the daemon repo, grove-syncd from the sync repo, the
// rest are same-named. compositor is a library (no binaries) and is never
// shipped; its zig static libs are consumed at cross-build time by the repos
// that link it (grove/flow/nb/treemux/tuimux), which `grove build --target`
// builds in an earlier wave.
var satellitePrebuiltStackRepos = []string{"grove", "daemon", "flow", "nb", "treemux", "tuimux", "sync"}

// satelliteSyncdUnitRel is the grove-syncd systemd unit's path relative to the
// ecosystem worktree root. `up --prebuilt` ships THIS real file to the VM
// rather than forking/hardcoding the unit content, so the installed unit can
// never drift from the source of truth.
const satelliteSyncdUnitRel = "sync/systemd/grove-syncd.service"

// satelliteUpAssetsDir is a durable remote staging dir for non-binary assets
// `up --prebuilt` ships (the grove-syncd unit). Deliberately NOT the prebuilt
// install stage dir (satelliteStageDir): the install script rm -rf's that on
// success, and bootstrap's step 6 still needs the unit afterwards.
var satelliteUpAssetsDir = satelliteStageBase() + "/grove-satellite-up"

// validateSatellitePrebuiltStack fails fast (before terraform) when the
// ecosystem worktree can't supply what a prebuilt `up` needs: every stack repo
// must be a git checkout, and the grove-syncd systemd unit must be present to
// ship. Runs while the provision is still free — a wrong --source-dir aborts
// with no orphaned VM.
func validateSatellitePrebuiltStack(sourceAbs string) error {
	// compositor is not shipped (a library) but its per-target zig static libs
	// are linked by the stack, so it is cross-built first — it must be present.
	for _, r := range append([]string{"compositor"}, satellitePrebuiltStackRepos...) {
		if _, err := os.Stat(filepath.Join(sourceAbs, r, ".git")); err != nil {
			return fmt.Errorf("--prebuilt: %s is not a git repo under %s (the prebuilt stack needs it) — is --source-dir the ecosystem worktree root?", r, sourceAbs)
		}
	}
	unit := filepath.Join(sourceAbs, filepath.FromSlash(satelliteSyncdUnitRel))
	if _, err := os.Stat(unit); err != nil {
		return fmt.Errorf("--prebuilt: grove-syncd systemd unit not found at %s (needed to install the service without a VM-side source tree): %w", unit, err)
	}
	return nil
}

// satellitePrebuiltStackDeltas builds the repoDelta ship set for a FRESH VM:
// every stack repo at its local tip, forced (there is no VM checkout to diff
// against). It mirrors the local-tip/dirty probing `satellite upgrade` does so
// the shared deploySatellitePrebuilt can consume the result unchanged — a dirty
// tree ships as <sha>-dirty exactly as in upgrade.
func satellitePrebuiltStackDeltas(sourceAbs string) (updates []repoDelta, dirty map[string]bool, err error) {
	dirty = map[string]bool{}
	for _, r := range satellitePrebuiltStackRepos {
		dir := filepath.Join(sourceAbs, r)
		tip, terr := localRepoTip(dir)
		if terr != nil {
			return nil, nil, fmt.Errorf("read local HEAD of %s: %w", r, terr)
		}
		d, derr := localRepoDirty(dir)
		if derr != nil {
			return nil, nil, fmt.Errorf("read local dirtiness of %s: %w", r, derr)
		}
		updates = append(updates, repoDelta{Repo: r, Branch: tip.Branch, LocalSHA: tip.SHA, Status: deltaStatusForced})
		dirty[r] = d
	}
	return updates, dirty, nil
}

// waitForSatelliteSSHAuth polls a trivial remote command until the pinned
// transport can actually authenticate. On a FRESH VM, sshd answers (and
// ssh-keyscan succeeds) before the GCE guest agent has written the metadata
// SSH key to authorized_keys, so the first BatchMode connection gets
// "Permission denied (publickey)". The source bootstrap absorbs this in its
// step-1 startup-done loop (10s retries for 15 min); the prebuilt ship runs
// before bootstrap, so it needs its own wait.
func waitForSatelliteSSHAuth(ssh *satelliteSSH, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for attempt := 0; ; attempt++ {
		if lastErr = ssh.runCommand("true"); lastErr == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ssh auth to %s not ready after %s (fresh-VM key propagation usually takes seconds — check the VM's guest agent and that your key is in the instance metadata): %w", ssh.dest(), timeout, lastErr)
		}
		if attempt == 0 {
			fmt.Printf("Waiting for SSH auth on %s (fresh VM: authorized_keys propagate shortly after boot)...\n", ssh.dest())
		}
		time.Sleep(5 * time.Second)
	}
}

// shipSatelliteSyncdUnit scp's the grove-syncd systemd unit (sourced from the
// ecosystem worktree) to a durable remote staging dir over the pinned
// transport, and returns the remote path bootstrap's step 6 installs from via
// --syncd-unit.
func shipSatelliteSyncdUnit(ssh *satelliteSSH, sourceAbs string) (string, error) {
	local := filepath.Join(sourceAbs, filepath.FromSlash(satelliteSyncdUnitRel))
	if _, err := os.Stat(local); err != nil {
		return "", fmt.Errorf("grove-syncd unit not found at %s: %w", local, err)
	}
	if err := ssh.runCommand("mkdir -p " + satelliteUpAssetsDir); err != nil {
		return "", fmt.Errorf("create remote assets dir %s: %w", satelliteUpAssetsDir, err)
	}
	if err := ssh.scp([]string{local}, satelliteUpAssetsDir+"/"); err != nil {
		return "", fmt.Errorf("scp grove-syncd unit: %w", err)
	}
	return satelliteUpAssetsDir + "/grove-syncd.service", nil
}
