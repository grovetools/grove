package tests

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/tend/pkg/command"
	"github.com/grovetools/tend/pkg/harness"
	"github.com/grovetools/tend/pkg/verify"
)

// The unified-identity full-loop E2E.
//
// It closes the workspace-identity plan's loop end to end, against real
// processes and no fakes:
//
//	join → subscribe → materialize → the machine's presence note appears on a
//	SECOND origin → `satellite up`'s config seed renders from that same config
//	→ satellite worktree push/pull is still green.
//
// Everything is real: a real `grove-syncd serve` on loopback, two real
// `groved` daemons with distinct machine identities and distinct sync.dbs, the
// production `grove` binary driving the production verbs, and (for the last
// leg) the in-process SSH satellite endpoint the other satellite scenarios
// use. The only thing simulated is the network topology — two "machines" are
// two sandboxed GROVE_HOMEs on one host.
//
// It is the acceptance criterion for job 23 scope item 2, and the automated
// counterpart to job 21's hand-run walkthrough.
//
// Environmental prerequisites (missing ones pass with a NOTICE rather than
// fail, matching the satellite scenarios' posture): built `grove-syncd` and
// `groved` binaries beside the grove repo, i.e. a `make build` in sync/ and
// daemon/.

const (
	loopKeySkip     = "idloop_skip"
	loopKeyServer   = "idloop_server"
	loopKeyToken    = "idloop_token"
	loopKeyMachineA = "idloop_a"
	loopKeyMachineB = "idloop_b"
	loopKeyCleanup  = "idloop_cleanup"
	loopKeySourceA  = "idloop_source"
	loopKeyEndpoint = "idloop_ep"

	// loopEcosystem is the ecosystem the two machines subscribe to. It is
	// deliberately NOT "grovetools": the source is a fixture repo, and reusing
	// the real name would make a stray real config look like a passing test.
	loopEcosystem = "loopeco"
	// loopNotebookWorkspace is the workspace materialize subscribes with
	// role=peer.
	loopNotebookWorkspace = "loopnotes"
	// loopRegistryWorkspace is the reserved registry workspace. Identified by
	// ROLE, not by name — the name only has to agree between the two machines.
	loopRegistryWorkspace = "registry"
)

// loopMachine is one sandboxed grove "machine": its own GROVE_HOME (config,
// data, state, cache in one move), its own HOME, and its own groved.
type loopMachine struct {
	Label  string
	Root   string
	Home   string
	Grove  string // GROVE_HOME
	Socket string
	daemon *command.Process
}

// daemonTail returns the last few KiB of this machine's groved output — the
// only thing that explains a note that never converged.
func (m *loopMachine) daemonTail() string {
	if m.daemon == nil {
		return "(no daemon)"
	}
	out := m.daemon.Stdout() + m.daemon.Stderr()
	if len(out) > 4000 {
		out = out[len(out)-4000:]
	}
	return out
}

// Env is the environment every command for this machine runs with.
//
// GROVE_SCOPE is pinned EMPTY on purpose: with a non-ecosystem cwd the CLI
// otherwise resolves a scoped socket while groved listens on the one named by
// --socket, and every daemon-touching verb silently reports "no daemon".
// Job 21 hit this by hand; it is item 15 on the plan's ticket roster.
func (m *loopMachine) Env() []string {
	return []string{
		"HOME=" + m.Home,
		"GROVE_HOME=" + m.Grove,
		"GROVE_SCOPE=",
		"GROVE_SOCKET=" + m.Socket,
		"XDG_RUNTIME_DIR=" + filepath.Dir(m.Socket),
		// Neutralize the harness's own sandbox vars: GROVE_HOME wins over XDG
		// in core/pkg/paths, but leaving them set makes the resolution order
		// the thing under test rather than the loop.
		"XDG_CONFIG_HOME=", "XDG_DATA_HOME=", "XDG_STATE_HOME=", "XDG_CACHE_HOME=",
		"GROVE_BIN=",
	}
}

func (m *loopMachine) ConfigDir() string { return filepath.Join(m.Grove, "config", "grove") }

// IdentityUnifiedLoopScenario is the full-loop acceptance scenario.
func IdentityUnifiedLoopScenario() *harness.Scenario {
	steps := []harness.Step{
		harness.NewStep("Resolve prerequisites (grove-syncd, groved)", loopResolvePrereqs),
		loopStep("Start a real grove-syncd on loopback and mint a token", loopStartServer),
		loopStep("Seed a source ecosystem carrying an [ecosystem] card", loopSeedSource),
		loopStep("Machine A: join the server", loopMachineAJoin),
		loopStep("Machine A: subscribe to the ecosystem", loopMachineASubscribe),
		loopStep("Machine A: materialize it", loopMachineAMaterialize),
		loopStep("Machine A: publish its presence note", loopMachineAPublish),
		loopStep("Machine B: join, and see A's note replicate to its origin", loopMachineBSeesA),
		loopStep("Machine A: render the satellite config seed from the same config", loopSatelliteSeed),
		loopStep("Machine A: satellite repos push/pull is still green", loopWorktreeRoundTrip),
	}
	return harness.NewScenario(
		"identity-unified-loop",
		"Full loop: join → subscribe → materialize → presence on a second origin → satellite config seed → satellite round trip",
		[]string{"identity", "sync", "satellite"},
		steps,
	).WithTeardown(harness.NewStep("Stop daemons and server", loopTeardown))
}

// loopStep is the pass-with-notice wrapper (tend has no runtime skip).
func loopStep(name string, fn harness.StepFunc) harness.Step {
	return harness.NewStep(name, func(ctx *harness.Context) error {
		if ctx.HasKey(loopKeySkip) {
			return nil
		}
		return fn(ctx)
	})
}

// --- setup ---

func loopResolvePrereqs(ctx *harness.Context) error {
	eco := filepath.Dir(ctx.ProjectRoot) // <ecosystem>/grove → <ecosystem>
	syncd := filepath.Join(eco, "sync", "bin", "grove-syncd")
	groved := filepath.Join(eco, "daemon", "bin", "groved")
	for _, bin := range []string{syncd, groved} {
		if _, err := os.Stat(bin); err != nil {
			ctx.Set(loopKeySkip, bin+" is not built")
			fmt.Printf("NOTICE: identity-unified-loop skipped — %s not built (run `make build` in its repo)\n", bin)
			return nil
		}
	}
	ctx.Set("idloop_syncd", syncd)
	ctx.Set("idloop_groved", groved)

	// Short root: macOS caps unix socket paths at ~104 bytes and the harness
	// sandbox lives well below that limit's reach. Everything path-length
	// sensitive (the groved sockets) hangs off here.
	root, err := os.MkdirTemp("/tmp", "gvloop")
	if err != nil {
		return err
	}
	ctx.Set(loopKeyCleanup, root)
	return nil
}

func loopRoot(ctx *harness.Context) string { return ctx.GetString(loopKeyCleanup) }

func loopStartServer(ctx *harness.Context) error {
	syncd := ctx.GetString("idloop_syncd")
	dataDir := filepath.Join(loopRoot(ctx), "syncd")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	bind := fmt.Sprintf("127.0.0.1:%d", port)

	// The server is a bare process: it must not read the developer's config.
	env := []string{"HOME=" + dataDir, "GROVE_HOME=" + dataDir}

	if out, err := command.New(syncd, "--data-dir", dataDir, "migrate").Env(env...).CombinedOutput(); err != nil {
		return fmt.Errorf("syncd migrate: %v\n%s", err, out)
	}
	proc, err := command.New(syncd, "--data-dir", dataDir, "serve", "--bind", bind).Env(env...).Start()
	if err != nil {
		return err
	}
	ctx.Set("idloop_serverproc", proc)

	url := "http://" + bind
	if err := waitFor(20*time.Second, func() bool {
		resp, err := http.Get(url + "/healthz") //nolint:gosec // loopback test server
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}); err != nil {
		return fmt.Errorf("grove-syncd did not become healthy at %s: %w", url, err)
	}
	ctx.Set(loopKeyServer, url)

	token, err := command.New(syncd, "--data-dir", dataDir, "token", "create", "loop").Env(env...).Output()
	if err != nil {
		return fmt.Errorf("token create: %w", err)
	}
	token = strings.TrimSpace(lastNonEmptyLine(token))
	if token == "" {
		return fmt.Errorf("token create produced no token")
	}
	ctx.Set(loopKeyToken, token)
	ctx.ShowCommandOutput("grove-syncd serve --bind "+bind, "healthy; token minted", "")
	return nil
}

// loopMemberRepos are the fixture ecosystem's member repos. They are what the
// satellite leg mirrors, so the last step pushes repos that this machine
// genuinely materialized rather than repos a fixture pre-planted.
var loopMemberRepos = []string{"alpha", "beta"}

// loopSeedSource builds the ecosystem the machines materialize: bare git repos
// on local disk, plus a card-carrying root repo. Everything materialize does
// is then a REAL clone from a REAL git remote — R10/R11's rule that the clone
// source is always a git remote, never a bundle.
func loopSeedSource(ctx *harness.Context) error {
	root := loopRoot(ctx)
	remotes := filepath.Join(root, "remote")
	if err := os.MkdirAll(remotes, 0o755); err != nil {
		return err
	}

	seed := func(name string, files map[string]string) (string, error) {
		bare := filepath.Join(remotes, name+".git")
		if out, err := exec.Command("git", "init", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
			return "", fmt.Errorf("git init --bare %s: %v\n%s", name, err, out)
		}
		work := filepath.Join(root, "source", name)
		if err := os.MkdirAll(work, 0o755); err != nil {
			return "", err
		}
		for rel, body := range files {
			if err := os.WriteFile(filepath.Join(work, rel), []byte(body), 0o644); err != nil {
				return "", err
			}
		}
		for _, args := range [][]string{
			{"init", "-b", "main"},
			{"add", "."},
			{"-c", "user.name=loop", "-c", "user.email=loop@example.com", "commit", "-m", "fixture " + name},
			{"remote", "add", "origin", bare},
			{"push", "-u", "origin", "main"},
		} {
			cmd := exec.Command("git", args...)
			cmd.Dir = work
			if out, err := cmd.CombinedOutput(); err != nil {
				return "", fmt.Errorf("git %v in %s: %v\n%s", args, name, err, out)
			}
		}
		return bare, nil
	}

	// The member repos first — the card has to name their URLs.
	var remoteBlocks strings.Builder
	for _, name := range loopMemberRepos {
		bare, err := seed(name, map[string]string{"README.md": "# " + name + "\n"})
		if err != nil {
			return err
		}
		fmt.Fprintf(&remoteBlocks, "\n[[ecosystem.remotes]]\nname = %q\nurl = %q\n", name, bare)
	}

	// Then the ecosystem root, whose manifest IS the card. `flat` is the
	// honest layout here: these repos never were submodules of a superrepo.
	manifest := fmt.Sprintf(`workspaces = ["*"]

[ecosystem]
id = "01LOOPECOSYSTEMFIXTURE001"
layout = "flat"
%s
[ecosystem.notebooks.%s]
default = true
`, remoteBlocks.String(), loopNotebookWorkspace)

	bare, err := seed(loopEcosystem, map[string]string{"grove.toml": manifest})
	if err != nil {
		return err
	}
	ctx.Set(loopKeySourceA, bare)
	return nil
}

// newLoopMachine creates one sandboxed machine and starts its groved.
func newLoopMachine(ctx *harness.Context, label string) (*loopMachine, error) {
	root := filepath.Join(loopRoot(ctx), label)
	m := &loopMachine{
		Label:  label,
		Root:   root,
		Home:   filepath.Join(root, "home"),
		Grove:  filepath.Join(root, "grove"),
		Socket: filepath.Join(root, "d.sock"),
	}
	for _, d := range []string{m.Home, m.Grove, m.ConfigDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(filepath.Join(m.Home, ".gitconfig"), []byte(testGitConfig), 0o644); err != nil {
		return nil, err
	}
	// A notebook root the registry workspace and the peer workspace hang off.
	notebookRoot := filepath.Join(m.Home, "notebooks", "nb")
	if err := os.MkdirAll(filepath.Join(notebookRoot, "workspaces"), 0o755); err != nil {
		return nil, err
	}
	base := fmt.Sprintf(`[notebooks.definitions.nb]
root_dir = %q
`, notebookRoot)
	if err := os.WriteFile(filepath.Join(m.ConfigDir(), "grove.toml"), []byte(base), 0o644); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *loopMachine) start(ctx *harness.Context) error {
	groved := ctx.GetString("idloop_groved")
	proc, err := command.New(groved, "start", "--socket", m.Socket).Env(m.Env()...).Start()
	if err != nil {
		return err
	}
	m.daemon = proc
	return waitFor(30*time.Second, func() bool {
		_, err := os.Stat(m.Socket)
		return err == nil
	})
}

func (m *loopMachine) stop() {
	if m.daemon != nil {
		_ = m.daemon.Kill()
		m.daemon = nil
	}
}

// grove runs the production grove binary as this machine.
func (m *loopMachine) grove(ctx *harness.Context, args ...string) *command.Result {
	return ctx.Bin(args...).Env(m.Env()...).Dir(m.Home).Timeout(90 * time.Second).Run()
}

// --- the loop ---

func loopMachineAJoin(ctx *harness.Context) error {
	a, err := newLoopMachine(ctx, "a")
	if err != nil {
		return err
	}
	if err := a.start(ctx); err != nil {
		return fmt.Errorf("machine A groved: %w", err)
	}
	ctx.Set(loopKeyMachineA, a)

	res := a.grove(ctx, "join", ctx.GetString(loopKeyServer),
		"--token", ctx.GetString(loopKeyToken),
		"--registry-workspace", loopRegistryWorkspace,
		"--wait", "30s")
	ctx.ShowCommandOutput("grove join (A)", res.Stdout, res.Stderr)
	if res.Error != nil {
		return fmt.Errorf("join failed: %v", res.Error)
	}

	return ctx.Verify(func(v *verify.Collector) {
		syncToml := readFileString(filepath.Join(a.ConfigDir(), "sync.toml"))
		v.Contains("join writes a registry-role subscription", syncToml, `role = "registry"`)
		v.Contains("the registry subscription pulls", syncToml, "pull = true")
		v.True("the token was persisted", statErr(filepath.Join(a.ConfigDir(), "sync.token")) == nil)
		v.True("the registry workspace root exists so syntheticNodeFor resolves", statErr(filepath.Join(a.Home, "notebooks", "nb", "workspaces", loopRegistryWorkspace)) == nil)
	})
}

func loopMachineASubscribe(ctx *harness.Context) error {
	a := ctx.Get(loopKeyMachineA).(*loopMachine)
	dest := filepath.Join(a.Home, "code", loopEcosystem)

	res := a.grove(ctx, "subscribe", loopEcosystem, "--path", dest)
	ctx.ShowCommandOutput("grove subscribe (A)", res.Stdout, res.Stderr)
	if res.Error != nil {
		return fmt.Errorf("subscribe failed: %v", res.Error)
	}
	return ctx.Verify(func(v *verify.Collector) {
		machineToml := readFileString(filepath.Join(a.ConfigDir(), "machine.toml"))
		v.Contains("subscribe records the intent in machine.toml", machineToml, "[machine.ecosystems."+loopEcosystem+"]")
		v.Contains("a declared-but-missing subscription names the materialize command", res.Stdout+res.Stderr, "materialize")
	})
}

func loopMachineAMaterialize(ctx *harness.Context) error {
	a := ctx.Get(loopKeyMachineA).(*loopMachine)

	res := a.grove(ctx, "ecosystem", "materialize", loopEcosystem,
		"--url", ctx.GetString(loopKeySourceA),
		"--notebook-workspace", loopNotebookWorkspace,
		"--yes", "--wait", "30s")
	ctx.ShowCommandOutput("grove ecosystem materialize (A)", res.Stdout, res.Stderr)
	if res.Error != nil {
		return fmt.Errorf("materialize failed: %v", res.Error)
	}

	// Idempotent convergence: a second run repairs, never re-does.
	again := a.grove(ctx, "ecosystem", "materialize", loopEcosystem,
		"--url", ctx.GetString(loopKeySourceA),
		"--notebook-workspace", loopNotebookWorkspace,
		"--yes", "--wait", "0")
	ctx.ShowCommandOutput("grove ecosystem materialize (A, re-run)", again.Stdout, again.Stderr)
	if again.Error != nil {
		return fmt.Errorf("materialize is not idempotent: %v", again.Error)
	}

	return ctx.Verify(func(v *verify.Collector) {
		v.True("the clone carries the manifest with its card", statErr(filepath.Join(a.Home, "code", loopEcosystem, "grove.toml")) == nil)
		for _, repo := range loopMemberRepos {
			v.True("member repo "+repo+" was cloned from the card's remotes", statErr(filepath.Join(a.Home, "code", loopEcosystem, repo, ".git")) == nil)
		}
		syncToml := readFileString(filepath.Join(a.ConfigDir(), "sync.toml"))
		v.Contains("the notebook workspace is subscribed with role=peer", syncToml, `role = "peer"`)
		v.Contains("the peer subscription names the card's notebook workspace", syncToml, `name = "`+loopNotebookWorkspace+`"`)
	})
}

// loopMachineAPublish waits for A's daemon to write its own presence note.
// The note writer fires on daemon start, on structural change, and daily; the
// config edits above are the structural change.
func loopMachineAPublish(ctx *harness.Context) error {
	a := ctx.Get(loopKeyMachineA).(*loopMachine)
	dir := filepath.Join(a.Home, "notebooks", "nb", "workspaces", loopRegistryWorkspace, "machines")

	// Poll for CONVERGENCE, not merely for a file. The writer fires on daemon
	// start too, so the first note on disk predates materialize and reports
	// the subscription as declared-missing. What has to be true is the note
	// that reflects the materialized tree: state present, and the embedded
	// card a peer would materialize from.
	var note, body string
	err := waitFor(120*time.Second, func() bool {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			note = filepath.Join(dir, e.Name())
			body = readFileString(note)
			if strings.Contains(body, "state: present") && strings.Contains(body, "card:") {
				return true
			}
		}
		return false
	})
	ctx.ShowCommandOutput("machine A presence note", body, "")
	if err != nil {
		return fmt.Errorf("machine A's presence note never converged on the materialized ecosystem in %s: %w\n--- groved (A) ---\n%s", dir, err, a.daemonTail())
	}

	return ctx.Verify(func(v *verify.Collector) {
		v.Contains("the note is keyed by the state-held machine id", body, "machine_id:")
		v.Contains("the note reports the materialized ecosystem", body, loopEcosystem)
		v.Contains("the note embeds the ecosystem's card for peers to materialize from", body, "card:")
		v.Contains("the note records the peer notebook subscription", body, loopNotebookWorkspace)
		v.Contains("the note is one document per machine", filepath.Base(note), ".md")
	})
}

// loopMachineBSeesA is the replication assertion: a SECOND origin, with its own
// machine identity and its own sync.db, joins the same server and receives A's
// presence note. This is what makes the registry an inventory rather than a
// local file.
func loopMachineBSeesA(ctx *harness.Context) error {
	a := ctx.Get(loopKeyMachineA).(*loopMachine)
	b, err := newLoopMachine(ctx, "b")
	if err != nil {
		return err
	}
	if err := b.start(ctx); err != nil {
		return fmt.Errorf("machine B groved: %w", err)
	}
	ctx.Set(loopKeyMachineB, b)

	res := b.grove(ctx, "join", ctx.GetString(loopKeyServer),
		"--token", ctx.GetString(loopKeyToken),
		"--registry-workspace", loopRegistryWorkspace,
		"--wait", "30s")
	ctx.ShowCommandOutput("grove join (B)", res.Stdout, res.Stderr)
	if res.Error != nil {
		return fmt.Errorf("machine B join failed: %v", res.Error)
	}

	aID := machineIDOf(a)
	if aID == "" {
		return fmt.Errorf("could not read machine A's id from its state dir")
	}
	target := filepath.Join(b.Home, "notebooks", "nb", "workspaces", loopRegistryWorkspace, "machines", aID+".md")
	if err := waitFor(90*time.Second, func() bool { return statErr(target) == nil }); err != nil {
		listing := b.grove(ctx, "machines", "--all")
		return fmt.Errorf("machine A's note never replicated to machine B at %s: %w\n(grove machines on B: %s %s)\n--- groved (A) ---\n%s\n--- groved (B) ---\n%s",
			target, err, listing.Stdout, listing.Stderr, a.daemonTail(), b.daemonTail())
	}

	listing := b.grove(ctx, "machines", "--all")
	ctx.ShowCommandOutput("grove machines (B)", listing.Stdout, listing.Stderr)

	return ctx.Verify(func(v *verify.Collector) {
		v.True("`grove machines` runs against the replicated registry", listing.Error == nil)
		v.Contains("B's fleet view lists A by its short id", listing.Stdout, aID[len(aID)-8:])
		v.Contains("the replicated note carries A's ecosystem, which is what B would materialize from", readFileString(target), loopEcosystem)
	})
}

// loopSatelliteSeed proves the convergence claim: on a machine that has joined
// and materialized — i.e. whose sync.toml now carries pull-enabled peer and
// registry entries — `satellite up`'s config seed still renders, and renders
// the VM's own entries as pull-enabled PEER entries. Before the role model
// this combination was impossible: the satellite writers refused any
// `pull = true` in the file at all.
func loopSatelliteSeed(ctx *harness.Context) error {
	a := ctx.Get(loopKeyMachineA).(*loopMachine)

	res := a.grove(ctx, "satellite", "config", "seed", "loopsat",
		"--sync-workspaces", loopNotebookWorkspace)
	ctx.ShowCommandOutput("grove satellite config seed (A)", res.Stdout, res.Stderr)
	if res.Error != nil {
		return fmt.Errorf("satellite config seed failed on a joined+materialized machine: %v\n%s", res.Error, res.Stderr)
	}

	return ctx.Verify(func(v *verify.Collector) {
		v.Contains("the seed is a v1 bundle", res.Stdout, "#!grove-config-seed v1")
		v.Contains("the VM declares its ecosystem as machine intent", res.Stdout, "#!file machine.toml")
		v.Contains("the VM gets a sync config", res.Stdout, "#!file sync.toml")
		v.Contains("the VM adopts its registry name", res.Stdout, `name = "loopsat"`)
		v.Contains("the VM's own entries are peer-role", res.Stdout, `role = "peer"`)
		v.Contains("the VM materializes its own notebook", res.Stdout, "pull = true")
		v.NotContains("a satellite-role entry is push-only and can never appear in the VM's own file", res.Stdout, `role = "satellite"`)
		v.NotContains("the seed does not invent ecosystems", res.Stdout, "[groves.loopsat]")
		// The laptop's own config was not touched by rendering a seed.
		v.Contains("rendering a seed does not rewrite the laptop's sync.toml", readFileString(filepath.Join(a.ConfigDir(), "sync.toml")), `role = "peer"`)
	})
}

// loopWorktreeRoundTrip re-runs the satellite worktree round trip on machine A
// — the machine that has just joined, subscribed and materialized — so the
// last leg proves the adoption verbs did not break the satellite transport.
func loopWorktreeRoundTrip(ctx *harness.Context) error {
	a := ctx.Get(loopKeyMachineA).(*loopMachine)
	codeDir := filepath.Join(a.Home, "code", loopEcosystem)

	// The sim endpoint is serialized against the other satellite scenarios —
	// they share fixed VM-side stage paths.
	lock, err := acquireSatelliteLock()
	if err != nil {
		return err
	}
	defer releaseSatelliteLock(lock)

	ep, skip, err := newSimSatellite(ctx, false)
	if err != nil {
		return err
	}
	if skip != "" {
		fmt.Printf("NOTICE: satellite leg skipped — %s\n", skip)
		return nil
	}
	ctx.Set(loopKeyEndpoint, ep)

	// The registry entry goes in THIS machine's state dir, not the harness
	// sandbox's: machine A resolves its paths through GROVE_HOME.
	if err := writeLoopSatelliteRegistry(a, ep.Name(), ep.RegistryEntryJSON()); err != nil {
		return err
	}

	env := append(a.Env(), ep.ExtraGroveEnv()...)
	run := func(args ...string) *command.Result {
		return ctx.Bin(args...).Env(env...).Dir(codeDir).Timeout(3 * time.Minute).Run()
	}

	push := run("satellite", "repos", "push", ep.Name(),
		"--repos", strings.Join(loopMemberRepos, ","),
		"--source-dir", codeDir,
		"--remote-code-dir", ep.RemoteCodeDir(),
		"--yes")
	ctx.ShowCommandOutput("grove satellite repos push (A)", push.Stdout, push.Stderr)
	if push.Error != nil {
		return fmt.Errorf("repos push failed from a joined+materialized machine: %v\n%s", push.Error, push.Stderr)
	}

	// A commit made ON the satellite, pulled back: the round trip, not just
	// the outbound leg.
	if _, err := ep.Exec(fmt.Sprintf(`set -e
cd %s/%s
git config user.name agent
git config user.email agent@satellite
echo satellite-side >> README.md
git commit -aqm "satellite commit"
`, ep.RemoteCodeDir(), loopMemberRepos[0])); err != nil {
		return fmt.Errorf("satellite-side commit: %w", err)
	}

	pull := run("satellite", "repos", "pull", ep.Name(),
		"--repos", loopMemberRepos[0],
		"--source-dir", codeDir,
		"--remote-code-dir", ep.RemoteCodeDir())
	ctx.ShowCommandOutput("grove satellite repos pull (A)", pull.Stdout, pull.Stderr)
	if pull.Error != nil {
		return fmt.Errorf("repos pull failed: %v\nstdout: %s\nstderr: %s", pull.Error, pull.Stdout, pull.Stderr)
	}

	return ctx.Verify(func(v *verify.Collector) {
		v.True("the satellite return path still works from a joined+materialized machine", pull.Error == nil)
		refs, gerr := loopGitOut(filepath.Join(codeDir, loopMemberRepos[0]), "for-each-ref", "--format=%(refname)", "refs/satellite")
		v.True("the laptop repo can be inspected after the pull", gerr == nil)
		v.Contains("the satellite's commit came back under refs/satellite/<name>/", refs, "refs/satellite/"+ep.Name())
	})
}

// writeLoopSatelliteRegistry writes satellites.json into a loop machine's own
// GROVE_HOME state dir (the sandbox equivalent of
// $XDG_STATE_HOME/grove/satellites.json).
func writeLoopSatelliteRegistry(m *loopMachine, name string, entry json.RawMessage) error {
	dir := filepath.Join(m.Grove, "state", "grove")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	payload := map[string]map[string]json.RawMessage{"satellites": {name: entry}}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "satellites.json"), data, 0o600)
}

// loopGitOut runs git in a loop machine's tree. It does not go through
// ctx.Command: a loop machine has its own HOME, not the harness sandbox's.
func loopGitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func loopTeardown(ctx *harness.Context) error {
	if ctx.HasKey(loopKeyEndpoint) {
		if err := ctx.Get(loopKeyEndpoint).(satelliteEndpoint).Close(); err != nil {
			fmt.Printf("(endpoint teardown: %v)\n", err)
		}
	}
	for _, key := range []string{loopKeyMachineA, loopKeyMachineB} {
		if ctx.HasKey(key) {
			ctx.Get(key).(*loopMachine).stop()
		}
	}
	if ctx.HasKey("idloop_serverproc") {
		_ = ctx.Get("idloop_serverproc").(*command.Process).Kill()
	}
	// GROVE_LOOP_KEEP=1 preserves the two machines' trees and the server's
	// data dir for post-mortem — the scenario's state IS the evidence when a
	// leg fails.
	if root := loopRoot(ctx); root != "" && strings.HasPrefix(root, "/tmp/gvloop") {
		if os.Getenv("GROVE_LOOP_KEEP") == "1" {
			fmt.Printf("GROVE_LOOP_KEEP=1 — leaving %s in place\n", root)
		} else {
			_ = os.RemoveAll(root)
		}
	}
	return nil
}

// --- small helpers ---

func waitFor(d time.Duration, cond func() bool) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("condition not met within %s", d)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func readFileString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func statErr(path string) error {
	_, err := os.Stat(path)
	return err
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// machineIDOf reads the ULID a machine minted into its own state dir. The id
// is state, never config — reading it from anywhere else would be testing a
// fiction.
func machineIDOf(m *loopMachine) string {
	data := readFileString(filepath.Join(m.Grove, "state", "grove", "machine.json"))
	if data == "" {
		return ""
	}
	const key = `"id"`
	idx := strings.Index(data, key)
	if idx < 0 {
		return ""
	}
	rest := data[idx+len(key):]
	start := strings.Index(rest, `"`)
	if start < 0 {
		return ""
	}
	rest = rest[start+1:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}
