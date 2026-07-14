package tests

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/tend/pkg/command"
	"github.com/grovetools/tend/pkg/harness"
	"github.com/grovetools/tend/pkg/verify"
	"golang.org/x/crypto/ssh"
)

// Satellite verb/contract E2E scenarios beyond the repo/worktree sync stack
// (scenarios_satellite.go): config propagation, registry merge semantics,
// the host-key pinning contract (C2), upgrade's delta/deploy engine, and the
// repos push/pull flag matrix. Same provider seam (satellite_endpoint.go);
// scenarios that would mutate global VM state beyond the scratch
// --remote-code-dir (config push writes ~/.config/grove; a registry-merge
// test rewires auth fields) are sim-only and pass-with-notice under
// TEND_SATELLITE_REAL=1, following the vintage-guard idiom.

// --- shared helpers ---

// writeLaptopGroveConfig writes the sandboxed laptop's GLOBAL grove config
// (XDG_CONFIG_HOME/grove/grove.toml — the file loadConfiguredSatellites and
// the seed-fragment assembly read).
func writeLaptopGroveConfig(ctx *harness.Context, content string) error {
	dir := filepath.Join(ctx.ConfigDir(), "grove")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "grove.toml"), []byte(content), 0o644)
}

// writeCustomSatelliteRegistry overwrites the sandbox satellites.json with a
// hand-modified entry (scenarios that tamper with host_key/identity fields).
func writeCustomSatelliteRegistry(ctx *harness.Context, name string, entry satelliteRegistryEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return writeSatelliteRegistry(ctx, name, data)
}

// newTestHostKeyLine generates a fresh, VALID ed25519 host key line ("<type>
// <base64>") that no server presents — the wrong-pin fixture for the C2
// host-key scenarios.
func newTestHostKeyLine() (string, error) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", err
	}
	return sshPub.Type() + " " + base64.StdEncoding.EncodeToString(sshPub.Marshal()), nil
}

// writeUnauthorizedIdentity writes a valid-but-unauthorized ed25519 private
// key: ssh offers it (IdentitiesOnly=yes), the sim's server rejects it.
func writeUnauthorizedIdentity(path string) error {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := ssh.MarshalPrivateKey(priv, "tend-unauthorized")
	if err != nil {
		return err
	}
	return os.WriteFile(path, pem.EncodeToMemory(block), 0o600)
}

// groveSatelliteStdin is groveSatellite with an attached stdin, for verbs
// whose confirmation prompts are part of the contract under test.
func groveSatelliteStdin(ctx *harness.Context, stdin string, args ...string) *command.Result {
	cmd := ctx.Bin(append([]string{"satellite"}, args...)...)
	cmd.Dir(ctx.GetString(satKeyCodeDir))
	cmd.Env(satEndpoint(ctx).ExtraGroveEnv()...)
	cmd.Timeout(3 * time.Minute)
	cmd.Stdin(strings.NewReader(stdin))
	return cmd.Run()
}

// laptopCommit commits a new file in a laptop repo and returns the new HEAD.
func laptopCommit(ctx *harness.Context, repoDir, file, content, msg string) (string, error) {
	if err := os.WriteFile(filepath.Join(repoDir, file), []byte(content), 0o644); err != nil {
		return "", err
	}
	if _, err := gitOut(ctx, repoDir, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := gitOut(ctx, repoDir, "commit", "-q", "-m", msg); err != nil {
		return "", err
	}
	return gitOut(ctx, repoDir, "rev-parse", "HEAD")
}

// simStageBase extracts the sim's GROVE_SATELLITE_STAGE_BASE (a LOCAL path —
// the sim "VM" shares the filesystem); "" for the real provider.
func simStageBase(ep satelliteEndpoint) string {
	for _, kv := range ep.ExtraGroveEnv() {
		if v, ok := strings.CutPrefix(kv, "GROVE_SATELLITE_STAGE_BASE="); ok {
			return v
		}
	}
	return ""
}

// vmProbePresent runs a VM-side existence probe, answering "present"/"absent".
func vmProbePresent(ctx *harness.Context, pathExpr string) (string, error) {
	return satExecOut(ctx, fmt.Sprintf(`test -e %s && echo present || echo absent`, pathExpr))
}

// --- satellite-config-push ---

// SatelliteConfigPushScenario: `grove satellite config push` end-to-end —
// denylist hard-error, --diff preview (no writes), the real push landing
// seed fragments + the rendered zz-satellite-custom.toml + the manifest in
// the VM's ~/.config/grove, converged re-push, and the removed-fragment
// warning. Sim-only: the push writes the VM's global config dir, outside the
// real provider's scratch-dir contract.
func SatelliteConfigPushScenario() *harness.Scenario {
	const fragName = "tend-frag.toml"
	const customName = "zz-satellite-custom.toml"
	const fragContent = "[notifications]\ntopic = \"from-fragment\"\n"

	// Laptop config declaring both propagation inputs for the endpoint.
	fullConfig := func(name string) string {
		return fmt.Sprintf("[satellites.%s]\nseed_fragments = [%q]\n\n[satellites.%s.config.notifications]\ntopic = \"sim-notify\"\n",
			name, fragName, name)
	}

	steps := satelliteSetupSteps(false, true)
	steps = append(steps,
		satStep("Denylisted seed fragment hard-errors before any transport", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			cfg := fmt.Sprintf("[satellites.%s]\nseed_fragments = [\"secrets.toml\"]\n", ep.Name())
			if err := writeLaptopGroveConfig(ctx, cfg); err != nil {
				return err
			}
			result := groveSatellite(ctx, "config", "push", ep.Name())
			ctx.ShowCommandOutput("grove satellite config push (denied fragment)", result.Stdout, result.Stderr)
			if err := ctx.Check("denied fragment fails the push", result.AssertFailure()); err != nil {
				return err
			}
			vmDir, err := vmProbePresent(ctx, `"$HOME/.config/grove"`)
			if err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("error names the denylist match", result.Stdout+result.Stderr, "is denied")
				v.Equal("nothing reached the VM config dir", "absent", vmDir)
			})
		}),
		satStep("--diff previews the fragment set and writes nothing", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			if err := writeLaptopGroveConfig(ctx, fullConfig(ep.Name())); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(ctx.ConfigDir(), "grove", fragName), []byte(fragContent), 0o644); err != nil {
				return err
			}
			result := groveSatellite(ctx, "config", "push", ep.Name(), "--diff")
			ctx.ShowCommandOutput("grove satellite config push --diff", result.Stdout, result.Stderr)
			if err := ctx.Check("--diff succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			vmDir, err := vmProbePresent(ctx, `"$HOME/.config/grove"`)
			if err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("seed fragment previewed as new", result.Stdout, fragName+": new on VM")
				v.Contains("custom fragment previewed as new", result.Stdout, customName+": new on VM")
				v.Contains("diff body shows the fragment content", result.Stdout, `+topic = "from-fragment"`)
				v.Contains("diff mode announces it wrote nothing", result.Stdout, "(--diff: nothing was written)")
				v.Equal("VM config dir still untouched after --diff", "absent", vmDir)
			})
		}),
		satStep("Push lands fragments + manifest in the VM's config dir", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			result := groveSatellite(ctx, "config", "push", ep.Name())
			ctx.ShowCommandOutput("grove satellite config push", result.Stdout, result.Stderr)
			if err := ctx.Check("config push succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			vmFrag, err := satExecOut(ctx, fmt.Sprintf(`cat "$HOME/.config/grove/%s"`, fragName))
			if err != nil {
				return fmt.Errorf("seed fragment missing on the VM: %w", err)
			}
			vmCustom, err := satExecOut(ctx, fmt.Sprintf(`cat "$HOME/.config/grove/%s"`, customName))
			if err != nil {
				return fmt.Errorf("custom fragment missing on the VM: %w", err)
			}
			vmManifest, err := satExecOut(ctx, `cat "$HOME/.config/grove/.grove-satellite-pushed"`)
			if err != nil {
				return fmt.Errorf("push manifest missing on the VM: %w", err)
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("push reports both fragments", result.Stdout, "Pushed 2 fragment(s)")
				v.Contains("push prints the daemon-restart caveat", result.Stdout, "restart it there")
				v.Equal("seed fragment shipped verbatim", strings.TrimSpace(fragContent), vmFrag)
				v.Contains("custom fragment carries the config value", vmCustom, "sim-notify")
				v.Contains("custom fragment pins the winning priority", vmCustom, "priority = 1000")
				v.Contains("custom fragment is marked generated", vmCustom, "Generated by `grove satellite config push`")
				v.Contains("manifest lists the seed fragment", vmManifest, fragName)
				v.Contains("manifest lists the custom fragment", vmManifest, customName)
			})
		}),
		satStep("Re-push is converged (diff reports both unchanged)", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			diff := groveSatellite(ctx, "config", "push", ep.Name(), "--diff")
			ctx.ShowCommandOutput("grove satellite config push --diff (converged)", diff.Stdout, diff.Stderr)
			if err := ctx.Check("converged --diff succeeds", diff.AssertSuccess()); err != nil {
				return err
			}
			// The push itself has no skip-if-identical arm; a re-push re-ships
			// byte-identical content (idempotent, asserted via the diff above
			// staying clean afterwards).
			repush := groveSatellite(ctx, "config", "push", ep.Name())
			ctx.ShowCommandOutput("grove satellite config push (re-push)", repush.Stdout, repush.Stderr)
			if err := ctx.Check("re-push succeeds", repush.AssertSuccess()); err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("seed fragment converged", diff.Stdout, fragName+": unchanged")
				v.Contains("custom fragment converged", diff.Stdout, customName+": unchanged")
				v.Contains("re-push ships the same set", repush.Stdout, "Pushed 2 fragment(s)")
			})
		}),
		satStep("Dropping a pushed fragment warns and leaves it on the VM", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			cfg := fmt.Sprintf("[satellites.%s.config.notifications]\ntopic = \"sim-notify\"\n", ep.Name())
			if err := writeLaptopGroveConfig(ctx, cfg); err != nil {
				return err
			}
			result := groveSatellite(ctx, "config", "push", ep.Name())
			ctx.ShowCommandOutput("grove satellite config push (fragment removed)", result.Stdout, result.Stderr)
			if err := ctx.Check("push after removal succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			vmFrag, err := vmProbePresent(ctx, fmt.Sprintf(`"$HOME/.config/grove/%s"`, fragName))
			if err != nil {
				return err
			}
			vmManifest, err := satExecOut(ctx, `cat "$HOME/.config/grove/.grove-satellite-pushed"`)
			if err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("push warns about the dropped fragment", result.Stdout+result.Stderr, "no longer listed: "+fragName)
				v.Contains("only the custom fragment ships", result.Stdout, "Pushed 1 fragment(s)")
				v.Equal("dropped fragment stays on the VM (deletion is manual)", "present", vmFrag)
				v.NotContains("manifest no longer lists the dropped fragment", vmManifest, fragName)
			})
		}),
	)
	return harness.NewScenario(
		"satellite-config-push",
		"Propagates laptop config fragments to the VM's ~/.config/grove: denylist, --diff, push, convergence, removal warning",
		[]string{"satellite"},
		steps,
	).WithTeardown(satelliteTeardownSteps()...)
}

// --- satellite-status-list-registry ---

// SatelliteRegistryMergeScenario: registry merge semantics (config ∪ state,
// cmd/satellite_state.go mergeSatelliteEntries) through real CLI output —
// state wins the machine-derived fields (ssh_addr/host_key), config wins the
// user-authored ones (user/identity_file), a conflicting flat config block
// draws the stale warning — and the merge is proven end-to-end by a transport
// verb that only connects if BOTH precedence directions held. Sim-only: it
// deliberately plants a broken identity_file in the state entry.
func SatelliteRegistryMergeScenario() *harness.Scenario {
	const staleAddr = "192.0.2.1:2201" // TEST-NET-1: never routable
	steps := satelliteSetupSteps(false, true)
	steps = append(steps,
		satStep("Seed conflicting config and state registry views", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			real := ep.RegistryEntry()
			badKey := filepath.Join(ctx.RootDir, "unauthorized_ed25519")
			if err := writeUnauthorizedIdentity(badKey); err != nil {
				return err
			}
			// STATE: correct machine-derived fields, but a wrong (valid,
			// unauthorized) identity_file — config must win that field.
			st := real
			st.IdentityFile = badKey
			if err := writeCustomSatelliteRegistry(ctx, ep.Name(), st); err != nil {
				return err
			}
			// CONFIG: a stale flat ssh_addr (state must win + warn) plus the
			// REAL user/identity_file (config must win those).
			cfg := fmt.Sprintf("[satellites.%s]\nssh_addr = %q\nuser = %q\nidentity_file = %q\n",
				ep.Name(), staleAddr, real.User, real.IdentityFile)
			return writeLaptopGroveConfig(ctx, cfg)
		}),
		satStep("list renders the merged registry with the state-wins warning", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			result := groveSatellite(ctx, "list")
			ctx.ShowCommandOutput("grove satellite list", result.Stdout, result.Stderr)
			if err := ctx.Check("satellite list succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("row shows the satellite", result.Stdout, ep.Name())
				v.Contains("Addr column carries the STATE ssh_addr", result.Stdout, ep.RegistryEntry().SSHAddr)
				v.NotContains("stale config ssh_addr does not surface", result.Stdout, staleAddr)
				v.Contains("no daemon: satellite reads not connected", result.Stdout, "not connected")
				v.Contains("stale flat config block draws the warning", result.Stderr, "the satellite state file wins")
			})
		}),
		satStep("status renders the same merged view", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			result := groveSatellite(ctx, "status")
			ctx.ShowCommandOutput("grove satellite status", result.Stdout, result.Stderr)
			if err := ctx.Check("satellite status succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("status shows the satellite", result.Stdout, ep.Name())
				v.Contains("status Addr matches the state entry", result.Stdout, ep.RegistryEntry().SSHAddr)
			})
		}),
		satStep("Transport proves per-field precedence (state addr, config identity)", func(ctx *harness.Context) error {
			// This connect succeeds ONLY if the merge picked the state's
			// ssh_addr (config's points at TEST-NET-1) AND the config's
			// identity_file (the state's key is unauthorized).
			ep := satEndpoint(ctx)
			result := groveSatellite(ctx, "repos", "push", ep.Name(),
				"--repos", "alpha",
				"--source-dir", ctx.GetString(satKeyCodeDir),
				"--remote-code-dir", ep.RemoteCodeDir(),
				"--dry-run")
			ctx.ShowCommandOutput("grove satellite repos push --dry-run (merged transport)", result.Stdout, result.Stderr)
			if err := ctx.Check("dry-run push connects over the merged entry", result.AssertSuccess()); err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("probe reached the VM and saw the missing repo", result.Stdout, "(dry-run) would mirror 1 repo(s): alpha")
			})
		}),
	)
	return harness.NewScenario(
		"satellite-status-list-registry",
		"Registry merge semantics: state wins ssh_addr/host_key (with the stale-config warning), config wins user/identity_file",
		[]string{"satellite"},
		steps,
	).WithTeardown(satelliteTeardownSteps()...)
}

// --- satellite-hostkey-pin ---

// SatelliteHostKeyPinScenario: the C2 security contract. A registry entry
// pinning a DIFFERENT valid host key than the server presents must fail every
// transport-using verb closed — no prompt, no TOFU, nothing written VM-side —
// and an entry with NO host_key must refuse before dialing at all
// (newSatelliteSSH). Runs in real mode too: both arms are read-only failures.
func SatelliteHostKeyPinScenario() *harness.Scenario {
	steps := satelliteSetupSteps(false, false)
	steps = append(steps,
		satStep("Pin a wrong (but valid) host key in the registry", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			wrong, err := newTestHostKeyLine()
			if err != nil {
				return err
			}
			entry := ep.RegistryEntry()
			entry.HostKey = wrong
			return writeCustomSatelliteRegistry(ctx, ep.Name(), entry)
		}),
		satStep("repos push fails closed: no TOFU, nothing lands on the VM", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			result := groveSatellite(ctx, reposPushArgs(ctx)...)
			ctx.ShowCommandOutput("grove satellite repos push (wrong pinned host key)", result.Stdout, result.Stderr)
			if err := ctx.Check("push fails under the wrong pin", result.AssertFailure()); err != nil {
				return err
			}
			code := remoteCodeDirExpr(ep.RemoteCodeDir())
			vmAlpha, err := vmProbePresent(ctx, fmt.Sprintf(`"%s/alpha"`, code))
			if err != nil {
				return err
			}
			combined := result.Stdout + result.Stderr
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("push dies at the first transport probe", combined, "read remote HEADs")
				v.NotContains("no interactive TOFU prompt", combined, "Are you sure you want to continue connecting")
				v.NotContains("no bundles shipped", result.Stdout, "Shipping")
				v.Equal("nothing written VM-side", "absent", vmAlpha)
				if ep.IsSim() {
					// The sim always presents an ed25519 key, so the client's
					// failure mode is deterministic; real servers may fail
					// algorithm negotiation with a different message instead.
					v.Contains("ssh reports host-key verification failure", combined, "Host key verification failed")
				}
			})
		}),
		satStep("repos pull fails closed the same way", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			result := groveSatellite(ctx, "repos", "pull", ep.Name(),
				"--repos", strings.Join(satFixtureRepos, ","),
				"--source-dir", ctx.GetString(satKeyCodeDir),
				"--remote-code-dir", ep.RemoteCodeDir())
			ctx.ShowCommandOutput("grove satellite repos pull (wrong pinned host key)", result.Stdout, result.Stderr)
			if err := ctx.Check("pull fails under the wrong pin", result.AssertFailure()); err != nil {
				return err
			}
			combined := result.Stdout + result.Stderr
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("pull dies at the first transport probe", combined, "read remote HEADs")
				v.NotContains("no interactive TOFU prompt", combined, "Are you sure you want to continue connecting")
			})
		}),
		satStep("Missing host_key refuses to dial at all", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			entry := ep.RegistryEntry()
			entry.HostKey = ""
			if err := writeCustomSatelliteRegistry(ctx, ep.Name(), entry); err != nil {
				return err
			}
			result := groveSatellite(ctx, reposPushArgs(ctx)...)
			ctx.ShowCommandOutput("grove satellite repos push (no host_key)", result.Stdout, result.Stderr)
			if err := ctx.Check("push refuses without a pin", result.AssertFailure()); err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("refusal names the missing pin", result.Stdout+result.Stderr, "refusing to ssh without a pin")
			})
		}),
	)
	return harness.NewScenario(
		"satellite-hostkey-pin",
		"C2 contract: a wrong pinned host key fails every transport verb closed (no TOFU, no VM writes); a missing host_key refuses to dial",
		[]string{"satellite"},
		steps,
	).WithTeardown(satelliteTeardownSteps()...)
}

// --- satellite-upgrade-dry-run ---

// SatelliteUpgradeScenario: `grove satellite upgrade` against the sim —
// converged and changed dry-run delta tables, --repos force semantics, and
// (sim-only) the real source-mode deploy path through fetch/force-checkout on
// the VM with the recipe-less build arm (fixture repos have no Makefile, so
// build+install are exercised as "skipped (no build recipe)"; a real
// build/restart hard-requires a VM toolchain + systemd and stays uncoverable
// in the sim). The --prebuilt arm asserts the ship-nothing abort leaves the
// VM untouched (a full prebuilt cross-build is deliberately out of scope —
// too slow, and macOS sims lack sha256sum for the install script anyway).
func SatelliteUpgradeScenario() *harness.Scenario {
	upgradeArgs := func(ctx *harness.Context, extra ...string) []string {
		ep := satEndpoint(ctx)
		args := []string{
			"upgrade", ep.Name(),
			"--source-dir", ctx.GetString(satKeyCodeDir),
			"--remote-code-dir", ep.RemoteCodeDir(),
		}
		return append(args, extra...)
	}
	steps := satelliteSetupSteps(false, false)
	steps = append(steps,
		satStep("Initial repos push (upgrade diffs against the mirror)", pushFixtureRepos),
		satStep("Dry-run on a converged satellite reports nothing to do", func(ctx *harness.Context) error {
			result := groveSatellite(ctx, upgradeArgs(ctx, "--dry-run")...)
			ctx.ShowCommandOutput("grove satellite upgrade --dry-run (converged)", result.Stdout, result.Stderr)
			if err := ctx.Check("converged dry-run succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("delta table shows up-to-date rows", result.Stdout, "up-to-date")
				v.Contains("dry-run reports convergence", result.Stdout, "is up to date with")
				v.Contains("nothing would deploy", result.Stdout, "nothing to do")
			})
		}),
		satStep("Dry-run after a laptop commit shows the delta and ships nothing", func(ctx *harness.Context) error {
			oldVM, err := vmRepoHead(ctx, "alpha")
			if err != nil {
				return err
			}
			ctx.Set("old_vm_alpha", oldVM)
			newSha, err := laptopCommit(ctx, filepath.Join(ctx.GetString(satKeyCodeDir), "alpha"),
				"upgrade.txt", "upgrade fixture\n", "feat: laptop change for upgrade")
			if err != nil {
				return err
			}
			ctx.Set("new_alpha", newSha)
			result := groveSatellite(ctx, upgradeArgs(ctx, "--dry-run")...)
			ctx.ShowCommandOutput("grove satellite upgrade --dry-run (delta)", result.Stdout, result.Stderr)
			if err := ctx.Check("delta dry-run succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			vmAfter, err := vmRepoHead(ctx, "alpha")
			if err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("dry-run names the changed repo", result.Stdout, "(dry-run) would upgrade 1 repo(s): alpha")
				v.Contains("delta table marks the update", result.Stdout, "update")
				v.Equal("dry-run left the VM checkout alone", oldVM, vmAfter)
			})
		}),
		satStep("--repos force-lists an up-to-date repo as forced", func(ctx *harness.Context) error {
			result := groveSatellite(ctx, upgradeArgs(ctx, "--repos", "beta", "--dry-run")...)
			ctx.ShowCommandOutput("grove satellite upgrade --repos beta --dry-run", result.Stdout, result.Stderr)
			if err := ctx.Check("forced dry-run succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("up-to-date beta shows as forced", result.Stdout, "forced")
				v.Contains("forced repo enters the ship set", result.Stdout, "(dry-run) would upgrade 1 repo(s): beta")
			})
		}),
		satStep("Source-mode deploy converges the VM checkout (recipe-less build arm)", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			if !ep.IsSim() {
				// Real mode: the deploy stages at the fixed /tmp path on the
				// shared VM and answers interactive prompts — keep the real
				// provider's footprint to the scratch dir + read-only probes.
				fmt.Println("NOTICE: source-mode deploy arm is sim-only — skipped under TEND_SATELLITE_REAL=1")
				return nil
			}
			if writableTmpBase() != "/tmp" {
				// upgrade's stage dir is the hardcoded /tmp/grove-satellite-upgrade
				// (it does NOT honor GROVE_SATELLITE_STAGE_BASE — see the run
				// report); in sandboxes without a writable /tmp the deploy
				// cannot stage, so only the dry-run arms apply.
				fmt.Println("NOTICE: /tmp not writable — upgrade stages at the hardcoded /tmp/grove-satellite-upgrade; deploy arm skipped")
				return nil
			}
			// "y" confirms the deploy; "n" declines the systemd restart (no
			// systemctl in the sim — restart stays uncoverable here).
			result := groveSatelliteStdin(ctx, "y\nn\n", upgradeArgs(ctx)...)
			ctx.ShowCommandOutput("grove satellite upgrade (deploy, restart declined)", result.Stdout, result.Stderr)
			if err := ctx.Check("deploy succeeds with the restart declined", result.AssertSuccess()); err != nil {
				return err
			}
			vmAlpha, err := vmRepoHead(ctx, "alpha")
			if err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("deploy fetched + checked out the tip", result.Stdout, "fetch + checkout")
				v.Contains("recipe-less repo skips the build", result.Stdout, "skipped (no build recipe)")
				v.Contains("deploy script completed", result.Stdout, "deploy complete")
				v.Contains("declined restart prints the manual commands", result.Stdout, "services still run the old ones")
				v.Equal("VM checkout force-converged to the laptop tip", ctx.GetString("new_alpha"), vmAlpha)
			})
		}),
		satStep("--prebuilt with nothing shippable aborts before touching the VM", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			if !ep.IsSim() {
				fmt.Println("NOTICE: prebuilt abort arm is sim-only — skipped under TEND_SATELLITE_REAL=1")
				return nil
			}
			vmBefore, err := vmRepoHead(ctx, "beta")
			if err != nil {
				return err
			}
			// Recipe-less fixture repos produce no linux/amd64 binaries, so the
			// prebuilt deploy must drop everything and abort VM-untouched.
			//
			// PATH is restricted to the system dirs: the prebuilt local build
			// path (BuildReposForTarget → daemon.NewGlobalClient) AUTO-STARTS
			// groved when it can find one, and a groved started inside the
			// sandbox reads the sandbox satellites.json and opens a persistent
			// pinned SSH connection to the sim — which then never tears down
			// (Close blocks on open sessions) and outlives the scenario. With
			// no groved reachable the client degrades to the documented local
			// pool; ssh/scp/git/make all live in the system dirs.
			cmd := ctx.Bin(append([]string{"satellite"}, upgradeArgs(ctx, "--prebuilt", "--repos", "beta", "--yes")...)...)
			cmd.Dir(ctx.GetString(satKeyCodeDir))
			cmd.Env(ep.ExtraGroveEnv()...)
			cmd.Env("PATH=/usr/bin:/bin:/usr/sbin:/sbin")
			cmd.Timeout(3 * time.Minute)
			result := cmd.Run()
			ctx.ShowCommandOutput("grove satellite upgrade --prebuilt (nothing shippable)", result.Stdout, result.Stderr)
			if err := ctx.Check("prebuilt with an empty ship set fails", result.AssertFailure()); err != nil {
				return err
			}
			vmAfter, err := vmRepoHead(ctx, "beta")
			if err != nil {
				return err
			}
			combined := result.Stdout + result.Stderr
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("abort names the empty ship set", combined, "no repos left to ship")
				v.Contains("abort promises the VM was untouched", combined, "VM untouched")
				v.Equal("VM checkout indeed untouched", vmBefore, vmAfter)
			})
		}),
	)
	return harness.NewScenario(
		"satellite-upgrade-dry-run",
		"Upgrade delta tables (converged/changed/forced) plus the sim-only source deploy and the prebuilt VM-untouched abort",
		[]string{"satellite"},
		steps,
	).WithTeardown(satelliteTeardownSteps()...)
}

// --- satellite-repos-flag-matrix ---

// SatelliteReposFlagMatrixScenario: hardens the repos verbs' flag contracts —
// strict --repos unknown-name hard-errors, push --dry-run transfers nothing,
// push --force discards genuinely-unfetched VM commits (which were never
// fetched: refs/satellite stays absent locally), and a detached VM HEAD pulls
// into refs/satellite/<name>/detached. All mutations stay inside the scratch
// remote code dir, so the scenario runs in real mode too.
func SatelliteReposFlagMatrixScenario() *harness.Scenario {
	steps := satelliteSetupSteps(false, false)
	steps = append(steps,
		satStep("Initial repos push", pushFixtureRepos),
		satStep("Strict --repos rejects an unknown repo name and ships nothing", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			result := groveSatellite(ctx, "repos", "push", ep.Name(),
				"--repos", "unknown-name",
				"--source-dir", ctx.GetString(satKeyCodeDir),
				"--remote-code-dir", ep.RemoteCodeDir(),
				"--yes")
			ctx.ShowCommandOutput("grove satellite repos push --repos unknown-name", result.Stdout, result.Stderr)
			if err := ctx.Check("unknown --repos name hard-errors", result.AssertFailure()); err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("error names the missing repo", result.Stdout+result.Stderr, "no git repo")
				v.NotContains("nothing shipped", result.Stdout, "Shipping")
			})
		}),
		satStep("Dry-run push previews the delta and transfers nothing", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			oldVM, err := vmRepoHead(ctx, "alpha")
			if err != nil {
				return err
			}
			ctx.Set("old_vm_alpha", oldVM)
			newSha, err := laptopCommit(ctx, filepath.Join(ctx.GetString(satKeyCodeDir), "alpha"),
				"laptop2.txt", "second laptop change\n", "feat: laptop change for dry-run")
			if err != nil {
				return err
			}
			ctx.Set("new_alpha", newSha)
			result := groveSatellite(ctx, reposPushArgs(ctx, "--dry-run")...)
			ctx.ShowCommandOutput("grove satellite repos push --dry-run", result.Stdout, result.Stderr)
			if err := ctx.Check("dry-run push succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			vmAfter, err := vmRepoHead(ctx, "alpha")
			if err != nil {
				return err
			}
			// The sim's stage base is a local path: assert no stage dir was
			// even created (dry-run stops before any scp).
			stageAbsent := true
			if base := simStageBase(ep); base != "" {
				if _, err := os.Stat(filepath.Join(base, "grove-satellite-repos")); err == nil {
					stageAbsent = false
				}
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("dry-run names the changed repo", result.Stdout, "(dry-run) would mirror 1 repo(s): alpha")
				v.NotContains("dry-run ships no bundles", result.Stdout, "Shipping")
				v.Equal("VM checkout untouched by dry-run", oldVM, vmAfter)
				v.True("no VM stage dir created by dry-run", stageAbsent)
			})
		}),
		satStep("--force discards genuinely-unfetched VM commits", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			code := remoteCodeDirExpr(ep.RemoteCodeDir())
			vmSha, err := satExecOut(ctx, fmt.Sprintf(`cd "%s/alpha" || exit 1
echo "vm-only work" > vm-only.txt
git add -A
git -c user.name="VM Agent" -c user.email="vm@example.com" commit -q -m "feat: unfetched vm work"
git rev-parse HEAD`, code))
			if err != nil {
				return err
			}
			result := groveSatellite(ctx, reposPushArgs(ctx, "--force")...)
			ctx.ShowCommandOutput("grove satellite repos push --force", result.Stdout, result.Stderr)
			if err := ctx.Check("forced push succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			vmHead, err := vmRepoHead(ctx, "alpha")
			if err != nil {
				return err
			}
			vmReach, err := satExecOut(ctx, fmt.Sprintf(
				`git -C "%s/alpha" merge-base --is-ancestor %s main && echo reachable || echo gone`, code, vmSha))
			if err != nil {
				return err
			}
			alphaDir := filepath.Join(ctx.GetString(satKeyCodeDir), "alpha")
			_, objErr := gitOut(ctx, alphaDir, "cat-file", "-e", vmSha+"^{commit}")
			_, refErr := gitOut(ctx, alphaDir, "rev-parse", "--verify", "refs/satellite/"+ep.Name()+"/main")
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("force warns about the discard", result.Stdout, "WARNING: --force: VM-side commits will be DISCARDED for: alpha")
				v.Equal("VM force-checkouted to the laptop tip", ctx.GetString("new_alpha"), vmHead)
				v.Equal("discarded commit gone from the VM branch", "gone", vmReach)
				v.True("discarded commit was never fetched to the laptop", objErr != nil)
				v.True("no refs/satellite ref exists for the discarded commit", refErr != nil)
			})
		}),
		satStep("Detached VM HEAD pulls into refs/satellite/<name>/detached", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			code := remoteCodeDirExpr(ep.RemoteCodeDir())
			vmSha, err := satExecOut(ctx, fmt.Sprintf(`cd "%s/beta" || exit 1
git checkout -q --detach
echo "detached vm work" > detached.txt
git add -A
git -c user.name="VM Agent" -c user.email="vm@example.com" commit -q -m "feat: detached vm work"
git rev-parse HEAD`, code))
			if err != nil {
				return err
			}
			betaDir := filepath.Join(ctx.GetString(satKeyCodeDir), "beta")
			localBefore, err := gitOut(ctx, betaDir, "rev-parse", "main")
			if err != nil {
				return err
			}
			result := groveSatellite(ctx, "repos", "pull", ep.Name(),
				"--repos", "beta",
				"--source-dir", ctx.GetString(satKeyCodeDir),
				"--remote-code-dir", ep.RemoteCodeDir())
			ctx.ShowCommandOutput("grove satellite repos pull (detached VM HEAD)", result.Stdout, result.Stderr)
			if err := ctx.Check("pull from a detached VM HEAD succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			ref := "refs/satellite/" + ep.Name() + "/detached"
			refSha, err := gitOut(ctx, betaDir, "rev-parse", ref)
			if err != nil {
				return fmt.Errorf("detached satellite ref missing after pull: %w", err)
			}
			localAfter, err := gitOut(ctx, betaDir, "rev-parse", "main")
			if err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("pull files the fetch under the detached ref", result.Stdout, ref)
				v.Equal("detached ref holds the VM sha", vmSha, refSha)
				v.Equal("local branch untouched by the pull", localBefore, localAfter)
			})
		}),
	)
	return harness.NewScenario(
		"satellite-repos-flag-matrix",
		"Repos verb flag contracts: strict unknown --repos, dry-run transfers nothing, --force discard semantics, detached-HEAD pull ref",
		[]string{"satellite"},
		steps,
	).WithTeardown(satelliteTeardownSteps()...)
}
