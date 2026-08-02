package cmd

// Shipping grove-syncd to the forge.
//
// Forgejo installs itself from a pinned, checksummed public release. grove-syncd
// has no public release: it is built from this ecosystem. So it follows the
// satellite `up --prebuilt` precedent — cross-compiled on the laptop, shipped
// over the PINNED SSH connection, installed by a small remote script — and the
// VM never grows a Go toolchain, a git checkout, or a build.
//
// The pieces are reused by call, not by copy: BuildReposForTargetLocal is the
// same wave-ordered cross-build `satellite upgrade --prebuilt` uses,
// collectPrebuiltBinaries the same collector, and newSatelliteSSH the same
// host-key-pinned transport. What is forge-specific — and could not be shared —
// is the destination: a satellite installs into the login user's grove bin dir,
// while grove-syncd is a SYSTEM service at /usr/local/bin under a systemd unit
// terraform already wrote.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/config"

	orch "github.com/grovetools/grove/pkg/orchestrator"
)

// forgeSyncdRepo is the ecosystem repo that produces grove-syncd.
const forgeSyncdRepo = "sync"

// forgeSyncdBinary is the binary name inside it, and the path the systemd unit
// terraform wrote points at.
const (
	forgeSyncdBinary  = "grove-syncd"
	forgeSyncdInstall = "/usr/local/bin/grove-syncd"
)

// forgeSyncdStageDir is a remote staging dir for the shipped binary. Under
// /tmp on purpose: nothing durable lives here, and the install script removes
// it.
const forgeSyncdStageDir = "/tmp/grove-forge-syncd"

// forgeSyncdShipPlan is everything the ship step needs, resolved BEFORE
// terraform runs so a wrong --source-dir or a missing repo aborts while the
// provision is still free.
type forgeSyncdShipPlan struct {
	SourceAbs string
	Target    orch.Target
}

// planForgeSyncdShip validates the local side of `up --prebuilt`.
func planForgeSyncdShip(sourceDir, targetFlag string) (*forgeSyncdShipPlan, error) {
	target, err := orch.ParseTarget(targetFlag)
	if err != nil {
		return nil, fmt.Errorf("--prebuilt-target %q: %w", targetFlag, err)
	}
	if sourceDir == "" {
		root, err := defaultUpgradeSourceDir()
		if err != nil {
			return nil, err
		}
		sourceDir = root
	}
	sourceAbs, err := filepath.Abs(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("resolve --source-dir: %w", err)
	}
	if _, err := os.Stat(filepath.Join(sourceAbs, forgeSyncdRepo, ".git")); err != nil {
		return nil, fmt.Errorf("--prebuilt: %s is not a git repo under %s (grove-syncd is built from it) — is --source-dir the ecosystem worktree root?", forgeSyncdRepo, sourceAbs)
	}
	return &forgeSyncdShipPlan{SourceAbs: sourceAbs, Target: target}, nil
}

// shipForgeSyncd cross-builds grove-syncd and installs it on the forge.
//
// It runs AFTER terraform apply, so the address exists; the host key is scanned
// and pinned at this point rather than trusted on first use, matching the
// satellite's C2 rule.
func shipForgeSyncd(w io.Writer, plan *forgeSyncdShipPlan, outputs forgeOutputs, forgeCfg *config.ForgeConfig) error {
	if outputs.ExternalIP == "" {
		return fmt.Errorf("terraform produced no external_ip — cannot ship grove-syncd")
	}

	fmt.Fprintf(w, "\nCross-building %s for %s...\n", forgeSyncdRepo, plan.Target)
	results, err := BuildReposForTargetLocal(context.Background(), plan.SourceAbs, []string{forgeSyncdRepo}, plan.Target, 0)
	if err != nil {
		return fmt.Errorf("cross-build %s: %w", forgeSyncdRepo, err)
	}
	for _, r := range results {
		if r.Err != nil {
			return fmt.Errorf("cross-build %s failed: %w", r.Job.Name, r.Err)
		}
	}
	binDir := prebuiltBinDir(filepath.Join(plan.SourceAbs, forgeSyncdRepo), plan.Target)
	localBin := filepath.Join(binDir, forgeSyncdBinary)
	if _, err := os.Stat(localBin); err != nil {
		return fmt.Errorf("%s did not produce %s in %s (does its Makefile honor GROVE_BUILD_OUT?): %w", forgeSyncdRepo, forgeSyncdBinary, binDir, err)
	}

	addr := outputs.ExternalIP + ":22"
	fmt.Fprintf(w, "Pinning the host key of %s...\n", addr)
	hostKey, err := sshKeyscanHostKey(addr)
	if err != nil {
		return fmt.Errorf("scan forge host key: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "grove-forge-ssh-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	ssh, err := newSatelliteSSH(satelliteConfigEntry{
		SSHAddr:      addr,
		User:         strings.TrimSpace(forgeCfg.Infra.SSHUser),
		HostKey:      hostKey,
		IdentityFile: strings.TrimSpace(forgeCfg.Infra.IdentityFile),
	}, tmpDir)
	if err != nil {
		return fmt.Errorf("build pinned SSH transport: %w", err)
	}

	fmt.Fprintf(w, "Shipping %s to %s...\n", forgeSyncdBinary, forgeSyncdInstall)
	if err := ssh.runCommand("mkdir -p " + forgeSyncdStageDir); err != nil {
		return fmt.Errorf("create remote stage dir: %w", err)
	}
	if err := ssh.scp([]string{localBin}, forgeSyncdStageDir+"/"); err != nil {
		return fmt.Errorf("scp %s: %w", forgeSyncdBinary, err)
	}
	if err := ssh.runScript(forgeSyncdInstallScript()); err != nil {
		return fmt.Errorf("install %s on the forge: %w", forgeSyncdBinary, err)
	}
	fmt.Fprintf(w, "%s installed and started.\n", forgeSyncdBinary)
	return nil
}

// forgeSyncdInstallScript installs the staged binary and starts the unit
// terraform already enabled.
//
// install(1) to the final path rather than cp: it is atomic-enough (write to a
// temp name, rename) and sets mode and ownership in one step, so a service
// restarting mid-copy can never exec a half-written binary.
func forgeSyncdInstallScript() string {
	return strings.Join([]string{
		"set -euo pipefail",
		"test -f " + forgeSyncdStageDir + "/" + forgeSyncdBinary,
		"sudo install -m 0755 -o root -g root " + forgeSyncdStageDir + "/" + forgeSyncdBinary + " " + forgeSyncdInstall,
		"rm -rf " + forgeSyncdStageDir,
		// The unit's ConditionPathExists was false until this moment, so a
		// plain restart is what turns it from enabled-but-skipped into
		// running.
		"sudo systemctl daemon-reload",
		"sudo systemctl restart grove-syncd.service",
		"sudo systemctl --no-pager --lines=0 status grove-syncd.service",
	}, "\n")
}
