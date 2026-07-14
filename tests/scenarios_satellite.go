package tests

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/core/pkg/worktreeregistry"
	"github.com/grovetools/core/util/pathutil"
	"github.com/grovetools/tend/pkg/command"
	"github.com/grovetools/tend/pkg/harness"
	"github.com/grovetools/tend/pkg/verify"
)

// Satellite simulation E2E scenarios: a sandboxed laptop grove drives a
// satellite endpoint (local SSH simulator by default; a real VM under
// TEND_SATELLITE_REAL=1, see satellite_endpoint.go) through the production
// `grove satellite repos|worktree push/pull` verbs — real ssh/scp binaries,
// pinned host key, sandboxed XDG dirs on both sides, zero fakes in grove's
// own code path.

// satFixtureRepos are the tiny deterministic laptop repos mirrored to the
// satellite. Names deliberately differ from the default satellite repo set
// (cloud/grovetools), so every verb gets an explicit --repos list.
var satFixtureRepos = []string{"alpha", "beta"}

const (
	satKeyEndpoint = "sat_ep"
	satKeyLock     = "sat_lock"
	satKeySkip     = "sat_skip"
	satKeyCodeDir  = "sat_code_dir"
	satKeyPlan     = "sat_plan"
)

// satStep wraps a step body with the pass-with-notice guard: tend has no
// runtime skip, so when an environmental prerequisite is missing the setup
// records a reason and every subsequent step becomes a no-op.
func satStep(name string, fn harness.StepFunc) harness.Step {
	return harness.NewStep(name, func(ctx *harness.Context) error {
		if ctx.HasKey(satKeySkip) {
			return nil
		}
		return fn(ctx)
	})
}

// satelliteSetupSteps is the shared scenario prologue: serialize satellite
// scenarios (fixed VM-side stage dirs), seed the laptop ecosystem, start the
// endpoint, and write the sandbox satellite registry.
func satelliteSetupSteps(installGrove, simOnly bool) []harness.Step {
	return []harness.Step{
		harness.NewStep("Acquire satellite scenario lock", func(ctx *harness.Context) error {
			if simOnly && os.Getenv("TEND_SATELLITE_REAL") == "1" {
				ctx.Set(satKeySkip, "sim-only scenario skipped under TEND_SATELLITE_REAL=1")
				fmt.Println("NOTICE: sim-only scenario — skipped under TEND_SATELLITE_REAL=1")
				return nil
			}
			lock, err := acquireSatelliteLock()
			if err != nil {
				return err
			}
			ctx.Set(satKeyLock, lock)
			return nil
		}),
		satStep("Seed laptop ecosystem fixtures", seedLaptopEcosystem),
		satStep("Start satellite endpoint and write registry", func(ctx *harness.Context) error {
			ep, skip, err := setupSatelliteEndpoint(ctx, installGrove)
			if err != nil {
				return err
			}
			if skip != "" {
				ctx.Set(satKeySkip, skip)
				fmt.Printf("NOTICE: satellite scenario skipped — %s\n", skip)
				return nil
			}
			ctx.Set(satKeyEndpoint, ep)
			return writeSatelliteRegistry(ctx, ep.Name(), ep.RegistryEntryJSON())
		}),
	}
}

// satelliteTeardownSteps releases the endpoint and the serialization lock.
func satelliteTeardownSteps() []harness.Step {
	return []harness.Step{
		harness.NewStep("Teardown satellite endpoint", func(ctx *harness.Context) error {
			if ctx.HasKey(satKeyEndpoint) {
				if err := ctx.Get(satKeyEndpoint).(satelliteEndpoint).Close(); err != nil {
					fmt.Printf("(endpoint teardown: %v)\n", err)
				}
			}
			if ctx.HasKey(satKeyLock) {
				releaseSatelliteLock(ctx.Get(satKeyLock).(*os.File))
			}
			return nil
		}),
	}
}

// --- laptop fixtures ---

// seedLaptopEcosystem creates the sandboxed laptop side: a deterministic git
// identity, and a minimal ecosystem at <home>/code/grovetools — a wildcard
// grove.toml manifest plus the fixture repos with two commits each on main.
func seedLaptopEcosystem(ctx *harness.Context) error {
	home := ctx.HomeDir()
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(testGitConfig), 0o644); err != nil {
		return err
	}
	codeDir := filepath.Join(home, "code", "grovetools")
	if err := os.MkdirAll(codeDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(codeDir, "grove.toml"), []byte("workspaces = [\"*\"]\n"), 0o644); err != nil {
		return err
	}
	for _, repo := range satFixtureRepos {
		dir := filepath.Join(codeDir, repo)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if _, err := gitOut(ctx, dir, "init", "-q", "-b", "main"); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# "+repo+"\n"), 0o644); err != nil {
			return err
		}
		if _, err := gitOut(ctx, dir, "add", "-A"); err != nil {
			return err
		}
		if _, err := gitOut(ctx, dir, "commit", "-q", "-m", "feat: initial "+repo+" fixture"); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(repo+" line one\n"), 0o644); err != nil {
			return err
		}
		if _, err := gitOut(ctx, dir, "add", "-A"); err != nil {
			return err
		}
		if _, err := gitOut(ctx, dir, "commit", "-q", "-m", "feat: second "+repo+" commit"); err != nil {
			return err
		}
	}
	ctx.Set(satKeyCodeDir, codeDir)
	return nil
}

// gitOut runs git in the sandbox (via ctx.Command, so the sandboxed HOME and
// its deterministic .gitconfig apply) and returns trimmed stdout.
func gitOut(ctx *harness.Context, dir string, args ...string) (string, error) {
	result := ctx.Command("git", args...).Dir(dir).Run()
	if result.Error != nil || result.ExitCode != 0 {
		return "", fmt.Errorf("git %s in %s: exit %d: %s", strings.Join(args, " "), dir, result.ExitCode, result.Stderr)
	}
	return strings.TrimSpace(result.Stdout), nil
}

// satEndpoint fetches the scenario's endpoint.
func satEndpoint(ctx *harness.Context) satelliteEndpoint {
	return ctx.Get(satKeyEndpoint).(satelliteEndpoint)
}

// groveSatellite runs `grove satellite <args>` from the laptop ecosystem
// root with a generous timeout (real mode crosses a network).
func groveSatellite(ctx *harness.Context, args ...string) *command.Result {
	cmd := ctx.Bin(append([]string{"satellite"}, args...)...)
	cmd.Dir(ctx.GetString(satKeyCodeDir))
	cmd.Env(satEndpoint(ctx).ExtraGroveEnv()...)
	cmd.Timeout(3 * time.Minute)
	return cmd.Run()
}

// satExecOut runs a script on the satellite and returns trimmed stdout.
func satExecOut(ctx *harness.Context, script string) (string, error) {
	out, err := satEndpoint(ctx).Exec(script)
	return strings.TrimSpace(out), err
}

// vmRepoHead reads the satellite-side HEAD sha of a mirrored repo.
func vmRepoHead(ctx *harness.Context, repo string) (string, error) {
	code := remoteCodeDirExpr(satEndpoint(ctx).RemoteCodeDir())
	return satExecOut(ctx, fmt.Sprintf(`git -C "%s/%s" rev-parse HEAD`, code, repo))
}

// vmRepoBranch reads the satellite-side checked-out branch of a mirrored repo.
func vmRepoBranch(ctx *harness.Context, repo string) (string, error) {
	code := remoteCodeDirExpr(satEndpoint(ctx).RemoteCodeDir())
	return satExecOut(ctx, fmt.Sprintf(`git -C "%s/%s" rev-parse --abbrev-ref HEAD`, code, repo))
}

// reposPushArgs is the canonical `repos push` invocation for the fixtures.
func reposPushArgs(ctx *harness.Context, extra ...string) []string {
	ep := satEndpoint(ctx)
	args := []string{
		"repos", "push", ep.Name(),
		"--repos", strings.Join(satFixtureRepos, ","),
		"--source-dir", ctx.GetString(satKeyCodeDir),
		"--remote-code-dir", ep.RemoteCodeDir(),
		"--yes",
	}
	return append(args, extra...)
}

// pushFixtureRepos pushes the fixture repos and asserts success.
func pushFixtureRepos(ctx *harness.Context) error {
	result := groveSatellite(ctx, reposPushArgs(ctx)...)
	ctx.ShowCommandOutput("grove satellite repos push", result.Stdout, result.Stderr)
	return ctx.Check("initial repos push succeeds", result.AssertSuccess())
}

// --- plan worktree fixture ---

// createPlanWorktreeFixture hand-rolls the minimal plan-worktree shape the
// satellite worktree verbs resolve (cmd/satellite_worktree.go): a container
// dir holding one linked `git worktree` per repo on a plan branch, the
// wildcard container grove.toml, the .grove/workspace marker, and a live
// worktree registry entry (Plan set) in the sandbox state dir — the same
// artifacts `flow plan init <plan> --worktree` produces via core
// workspace.Prepare, without depending on the flow binary.
func createPlanWorktreeFixture(ctx *harness.Context, repos []string) error {
	planName := "tendplan"
	if !satEndpoint(ctx).IsSim() {
		// Real VMs persist worktree containers across runs — keep names unique.
		var suffix [2]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return err
		}
		planName += "-" + hex.EncodeToString(suffix[:])
	}
	ctx.Set(satKeyPlan, planName)

	codeDir := ctx.GetString(satKeyCodeDir)
	container := filepath.Join(ctx.HomeDir(), "wt", planName)
	if err := os.MkdirAll(container, 0o755); err != nil {
		return err
	}
	for _, repo := range repos {
		base := filepath.Join(codeDir, repo)
		if _, err := gitOut(ctx, base, "worktree", "add", "-q", "-b", planName, filepath.Join(container, repo)); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(container, "grove.toml"), []byte("workspaces = [\"*\"]\n"), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(container, ".grove"), 0o755); err != nil {
		return err
	}
	marker := fmt.Sprintf("branch: %s\nplan: %s\ncreated_at: %s\nowner: %s\necosystem: true\nrepos:\n",
		planName, planName, time.Now().UTC().Format(time.RFC3339), codeDir)
	for _, repo := range repos {
		marker += "  - " + repo + "\n"
	}
	if err := os.WriteFile(filepath.Join(container, ".grove", "workspace"), []byte(marker), 0o644); err != nil {
		return err
	}

	entry := worktreeregistry.Entry{
		AbsPath:   container,
		Owner:     codeDir,
		Repos:     repos,
		Plan:      planName,
		CreatedAt: time.Now().UTC(),
	}
	data, err := json.MarshalIndent(&entry, "", "  ")
	if err != nil {
		return err
	}
	regDir := filepath.Join(ctx.StateDir(), "grove", "worktrees")
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(regDir, pathutil.WorktreeID(container)+".json"), data, 0o644)
}

// planContainer returns the laptop plan worktree container path.
func planContainer(ctx *harness.Context) string {
	return filepath.Join(ctx.HomeDir(), "wt", ctx.GetString(satKeyPlan))
}

// vmWorktreePath asks the satellite's own grove binary where the plan
// worktree container lives — the exact plumbing handshake the worktree verbs
// use (buildSatelliteWorktreePathScript).
func vmWorktreePath(ctx *harness.Context) (string, error) {
	ep := satEndpoint(ctx)
	script := fmt.Sprintf(`export PATH="/usr/local/go/bin:$HOME/.local/share/grove/bin:$PATH"
CODE=%s
grove internal worktree-path --git-root "$CODE" --name %q`,
		remoteCodeDirExpr(ep.RemoteCodeDir()), ctx.GetString(satKeyPlan))
	return satExecOut(ctx, script)
}

// --- scenarios ---

// SatelliteReposPushScenario: mirror the laptop repos onto the satellite and
// verify VM heads, then re-push and verify the idempotent no-op path.
func SatelliteReposPushScenario() *harness.Scenario {
	steps := satelliteSetupSteps(true, false)
	steps = append(steps,
		satStep("Push fixture repos to the satellite", func(ctx *harness.Context) error {
			result := groveSatellite(ctx, reposPushArgs(ctx)...)
			ctx.ShowCommandOutput("grove satellite repos push", result.Stdout, result.Stderr)
			if err := ctx.Check("repos push succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("push reports the mirrored repos", result.Stdout, "mirrored (alpha, beta)")
				v.Contains("push ships bundles", result.Stdout, "Shipping 2 bundle(s)")
			})
		}),
		satStep("Verify satellite-side repos match the laptop tips", func(ctx *harness.Context) error {
			codeDir := ctx.GetString(satKeyCodeDir)
			type repoState struct{ local, vmHead, vmBranch string }
			states := map[string]repoState{}
			for _, repo := range satFixtureRepos {
				localHead, err := gitOut(ctx, filepath.Join(codeDir, repo), "rev-parse", "HEAD")
				if err != nil {
					return err
				}
				vmHead, err := vmRepoHead(ctx, repo)
				if err != nil {
					return err
				}
				vmBranch, err := vmRepoBranch(ctx, repo)
				if err != nil {
					return err
				}
				states[repo] = repoState{localHead, vmHead, vmBranch}
			}
			return ctx.Verify(func(v *verify.Collector) {
				for _, repo := range satFixtureRepos {
					s := states[repo]
					v.Equal(repo+": VM HEAD matches laptop tip", s.local, s.vmHead)
					v.Equal(repo+": VM checkout is on main", "main", s.vmBranch)
				}
			})
		}),
		satStep("Re-push is idempotent (no bundles shipped)", func(ctx *harness.Context) error {
			result := groveSatellite(ctx, reposPushArgs(ctx)...)
			ctx.ShowCommandOutput("grove satellite repos push (re-run)", result.Stdout, result.Stderr)
			if err := ctx.Check("re-push succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("re-push reports up to date", result.Stdout, "mirror is up to date with")
				v.NotContains("re-push ships no bundles", result.Stdout, "Shipping")
				v.NotContains("re-push mirrors nothing", result.Stdout, "mirrored (")
			})
		}),
	)
	return harness.NewScenario(
		"satellite-repos-push-roundtrip",
		"Mirrors laptop git repos onto a satellite over real ssh/scp and verifies idempotent re-push",
		[]string{"satellite"},
		steps,
	).WithTeardown(satelliteTeardownSteps()...)
}

// SatelliteReposInterlockScenario: a satellite-side commit holds the push
// (per-repo isolated, nonzero exit) until `repos pull` fetches it into
// refs/satellite/<name>/…, after which push proceeds (diverged-fetched).
func SatelliteReposInterlockScenario() *harness.Scenario {
	steps := satelliteSetupSteps(true, false)
	steps = append(steps,
		satStep("Initial repos push", pushFixtureRepos),
		satStep("Make a satellite-side commit in alpha", func(ctx *harness.Context) error {
			code := remoteCodeDirExpr(satEndpoint(ctx).RemoteCodeDir())
			sha, err := satExecOut(ctx, fmt.Sprintf(`cd "%s/alpha" || exit 1
echo "vm work" > vm.txt
git add -A
git -c user.name="VM Agent" -c user.email="vm@example.com" commit -q -m "feat: vm-side work"
git rev-parse HEAD`, code))
			if err != nil {
				return err
			}
			ctx.Set("vm_alpha_sha", sha)
			return nil
		}),
		satStep("Make a laptop commit in beta", func(ctx *harness.Context) error {
			dir := filepath.Join(ctx.GetString(satKeyCodeDir), "beta")
			if err := os.WriteFile(filepath.Join(dir, "laptop.txt"), []byte("laptop work\n"), 0o644); err != nil {
				return err
			}
			if _, err := gitOut(ctx, dir, "add", "-A"); err != nil {
				return err
			}
			if _, err := gitOut(ctx, dir, "commit", "-q", "-m", "feat: laptop-side work"); err != nil {
				return err
			}
			return nil
		}),
		satStep("Push holds alpha, ships beta, exits nonzero", func(ctx *harness.Context) error {
			result := groveSatellite(ctx, reposPushArgs(ctx)...)
			ctx.ShowCommandOutput("grove satellite repos push (interlocked)", result.Stdout, result.Stderr)
			if err := ctx.Check("push exits nonzero while a repo is held", result.AssertFailure()); err != nil {
				return err
			}
			codeDir := ctx.GetString(satKeyCodeDir)
			localBeta, err := gitOut(ctx, filepath.Join(codeDir, "beta"), "rev-parse", "HEAD")
			if err != nil {
				return err
			}
			vmBeta, err := vmRepoHead(ctx, "beta")
			if err != nil {
				return err
			}
			vmAlpha, err := vmRepoHead(ctx, "alpha")
			if err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("push announces the hold", result.Stdout, "HELD (unfetched VM commits): alpha")
				v.Contains("push points at repos pull", result.Stdout+result.Stderr, "grove satellite repos pull")
				v.Equal("beta still shipped (per-repo isolation)", localBeta, vmBeta)
				v.Equal("alpha untouched on the VM", ctx.GetString("vm_alpha_sha"), vmAlpha)
			})
		}),
		satStep("Pull fetches VM commits into refs/satellite without touching local state", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			codeDir := ctx.GetString(satKeyCodeDir)
			alphaDir := filepath.Join(codeDir, "alpha")
			localBefore, err := gitOut(ctx, alphaDir, "rev-parse", "main")
			if err != nil {
				return err
			}
			result := groveSatellite(ctx, "repos", "pull", ep.Name(),
				"--repos", strings.Join(satFixtureRepos, ","),
				"--source-dir", codeDir,
				"--remote-code-dir", ep.RemoteCodeDir())
			ctx.ShowCommandOutput("grove satellite repos pull", result.Stdout, result.Stderr)
			if err := ctx.Check("repos pull succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			satRef, err := gitOut(ctx, alphaDir, "rev-parse", "refs/satellite/"+ep.Name()+"/main")
			if err != nil {
				return fmt.Errorf("satellite ref missing after pull: %w", err)
			}
			localAfter, err := gitOut(ctx, alphaDir, "rev-parse", "main")
			if err != nil {
				return err
			}
			_, statErr := os.Stat(filepath.Join(alphaDir, "vm.txt"))
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("pull reports the fetched repo", result.Stdout, "local branches untouched")
				v.Equal("satellite ref holds the VM sha", ctx.GetString("vm_alpha_sha"), satRef)
				v.Equal("local branch head untouched", localBefore, localAfter)
				v.True("local worktree untouched (no vm.txt)", os.IsNotExist(statErr))
			})
		}),
		satStep("Push proceeds after the pull (diverged-fetched)", func(ctx *harness.Context) error {
			result := groveSatellite(ctx, reposPushArgs(ctx)...)
			ctx.ShowCommandOutput("grove satellite repos push (post-pull)", result.Stdout, result.Stderr)
			if err := ctx.Check("push succeeds after pull", result.AssertSuccess()); err != nil {
				return err
			}
			localAlpha, err := gitOut(ctx, filepath.Join(ctx.GetString(satKeyCodeDir), "alpha"), "rev-parse", "HEAD")
			if err != nil {
				return err
			}
			vmAlpha, err := vmRepoHead(ctx, "alpha")
			if err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("push warns loudly about the force-checkout", result.Stdout, "diverged but already fetched: alpha")
				v.Equal("VM alpha force-checkouted to the laptop tip", localAlpha, vmAlpha)
			})
		}),
	)
	return harness.NewScenario(
		"satellite-repos-interlock-pull",
		"VM-side commits hold the push until repos pull fetches them into refs/satellite/<name>/…",
		[]string{"satellite"},
		steps,
	).WithTeardown(satelliteTeardownSteps()...)
}

// SatelliteWorktreeScenario: ship a plan worktree to the satellite, let an
// "agent" commit there, and fast-forward the laptop worktree back with
// `worktree pull --ff`.
func SatelliteWorktreeScenario() *harness.Scenario {
	steps := satelliteSetupSteps(true, false)
	steps = append(steps,
		satStep("Initial repos push (worktrees attach to base mirrors)", pushFixtureRepos),
		satStep("Create the laptop plan worktree", func(ctx *harness.Context) error {
			return createPlanWorktreeFixture(ctx, satFixtureRepos)
		}),
		// Every repo gets a plan commit: a worktree branch sitting exactly at
		// the mirrored base tip trips a known production edge ("Refusing to
		// create empty bundle" — worktreeBundleBaseSHA assumes the plan tip
		// moved past the base).
		satStep("Commit laptop plan work in each repo", func(ctx *harness.Context) error {
			for _, repo := range satFixtureRepos {
				dir := filepath.Join(planContainer(ctx), repo)
				if err := os.WriteFile(filepath.Join(dir, "plan.txt"), []byte("plan work in "+repo+"\n"), 0o644); err != nil {
					return err
				}
				if _, err := gitOut(ctx, dir, "add", "-A"); err != nil {
					return err
				}
				if _, err := gitOut(ctx, dir, "commit", "-q", "-m", "feat: laptop plan work"); err != nil {
					return err
				}
			}
			return nil
		}),
		satStep("Push the plan worktree to the satellite", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			result := groveSatellite(ctx, "worktree", "push", ep.Name(),
				"--plan", ctx.GetString(satKeyPlan),
				"--remote-code-dir", ep.RemoteCodeDir(),
				"--yes")
			ctx.ShowCommandOutput("grove satellite worktree push", result.Stdout, result.Stderr)
			if err := ctx.Check("worktree push succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("push confirms the plan worktree", result.Stdout,
					fmt.Sprintf("Plan worktree %q pushed", ctx.GetString(satKeyPlan)))
			})
		}),
		satStep("Verify the VM container at the plumbing-reported path", func(ctx *harness.Context) error {
			vmWT, err := vmWorktreePath(ctx)
			if err != nil {
				return fmt.Errorf("VM worktree-path plumbing: %w", err)
			}
			ctx.Set("vm_wt", vmWT)
			localAlpha, err := gitOut(ctx, filepath.Join(planContainer(ctx), "alpha"), "rev-parse", "HEAD")
			if err != nil {
				return err
			}
			vmStat, err := satExecOut(ctx, fmt.Sprintf(`test -d "%s/alpha" && test -f "%s/.grove/workspace" && echo present`, vmWT, vmWT))
			if err != nil {
				return err
			}
			vmHead, err := satExecOut(ctx, fmt.Sprintf(`git -C "%s/alpha" rev-parse HEAD`, vmWT))
			if err != nil {
				return err
			}
			vmBranch, err := satExecOut(ctx, fmt.Sprintf(`git -C "%s/alpha" rev-parse --abbrev-ref HEAD`, vmWT))
			if err != nil {
				return err
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.True("plumbing path is absolute", strings.HasPrefix(vmWT, "/"))
				v.Equal("VM container + marker exist at the plumbing path", "present", vmStat)
				v.Equal("VM worktree alpha at the laptop plan tip", localAlpha, vmHead)
				v.Equal("VM worktree alpha on the plan branch", ctx.GetString(satKeyPlan), vmBranch)
			})
		}),
		satStep("Make an agent commit in the VM worktree", func(ctx *harness.Context) error {
			sha, err := satExecOut(ctx, fmt.Sprintf(`cd "%s/alpha" || exit 1
echo "agent work" > agent.txt
git add -A
git -c user.name="VM Agent" -c user.email="vm@example.com" commit -q -m "feat: agent work on the satellite"
git rev-parse HEAD`, ctx.GetString("vm_wt")))
			if err != nil {
				return err
			}
			ctx.Set("agent_sha", sha)
			return nil
		}),
		satStep("Pull --ff fast-forwards the laptop worktree to the agent commit", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			result := groveSatellite(ctx, "worktree", "pull", ep.Name(),
				"--plan", ctx.GetString(satKeyPlan),
				"--remote-code-dir", ep.RemoteCodeDir(),
				"--ff")
			ctx.ShowCommandOutput("grove satellite worktree pull --ff", result.Stdout, result.Stderr)
			if err := ctx.Check("worktree pull --ff succeeds", result.AssertSuccess()); err != nil {
				return err
			}
			alphaDir := filepath.Join(planContainer(ctx), "alpha")
			localHead, err := gitOut(ctx, alphaDir, "rev-parse", "HEAD")
			if err != nil {
				return err
			}
			localBranch, err := gitOut(ctx, alphaDir, "rev-parse", "--abbrev-ref", "HEAD")
			if err != nil {
				return err
			}
			satRef, err := gitOut(ctx, alphaDir, "rev-parse",
				"refs/satellite/"+ep.Name()+"/"+ctx.GetString(satKeyPlan))
			if err != nil {
				return fmt.Errorf("satellite worktree ref missing after pull: %w", err)
			}
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("pull reports the fast-forward", result.Stdout, "alpha: fast-forwarded")
				v.Equal("laptop worktree at the agent commit", ctx.GetString("agent_sha"), localHead)
				v.Equal("laptop worktree still on the plan branch", ctx.GetString(satKeyPlan), localBranch)
				v.Equal("satellite ref holds the agent sha", ctx.GetString("agent_sha"), satRef)
			})
		}),
	)
	return harness.NewScenario(
		"satellite-worktree-push-pull-ff",
		"Ships a plan worktree to the satellite, commits there, and fast-forwards the laptop with pull --ff",
		[]string{"satellite"},
		steps,
	).WithTeardown(satelliteTeardownSteps()...)
}

// SatelliteVintageGuardScenario: a satellite without a grove binary on its
// PATH fails the whole worktree push with the upgrade pointer (sim-only —
// a real VM always has grove installed).
func SatelliteVintageGuardScenario() *harness.Scenario {
	steps := satelliteSetupSteps(false, true)
	steps = append(steps,
		satStep("Create the laptop plan worktree", func(ctx *harness.Context) error {
			return createPlanWorktreeFixture(ctx, satFixtureRepos)
		}),
		satStep("Worktree push fails whole-run with the upgrade pointer", func(ctx *harness.Context) error {
			ep := satEndpoint(ctx)
			result := groveSatellite(ctx, "worktree", "push", ep.Name(),
				"--plan", ctx.GetString(satKeyPlan),
				"--remote-code-dir", ep.RemoteCodeDir(),
				"--yes")
			ctx.ShowCommandOutput("grove satellite worktree push (no VM grove)", result.Stdout, result.Stderr)
			if err := ctx.Check("worktree push fails", result.AssertFailure()); err != nil {
				return err
			}
			combined := result.Stdout + result.Stderr
			return ctx.Verify(func(v *verify.Collector) {
				v.Contains("vintage guard names the missing binary", combined, "has no grove binary on the VM's PATH")
				v.Contains("vintage guard points at the upgrade", combined, "grove satellite upgrade")
				v.NotContains("no bundles were shipped", result.Stdout, "Shipping")
			})
		}),
	)
	return harness.NewScenario(
		"satellite-worktree-vintage-guard",
		"A satellite without grove on its PATH fails worktree push whole-run with an upgrade pointer",
		[]string{"satellite"},
		steps,
	).WithTeardown(satelliteTeardownSteps()...)
}
