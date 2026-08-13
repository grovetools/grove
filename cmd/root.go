package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/core/version"
	"github.com/spf13/cobra"

	coreplugin "github.com/grovetools/core/pkg/plugin"

	"github.com/grovetools/grove/cmd/internal"
	"github.com/grovetools/grove/pkg/delegation"
	"github.com/grovetools/grove/pkg/overrides"
	"github.com/grovetools/grove/pkg/plugin"
	"github.com/grovetools/grove/pkg/sdk"
	meta_workspace "github.com/grovetools/grove/pkg/workspace"
)

var rootCmd = cli.NewStandardCommand("grove", "Grove workspace orchestrator and tool manager")

func init() {
	// Set long description (don't repeat Short - grove-core help shows both)
	rootCmd.Long = `Run 'grove <tool>' to delegate to installed tools, or use subcommands below.`

	// Support --version on the root command (same pattern as nav)
	vInfo := version.GetInfo()
	rootCmd.Version = vInfo.Version
	cli.SetVersionTemplate(rootCmd, cli.VersionInfo{
		Version:   vInfo.Version,
		Commit:    vInfo.Commit,
		BuildDate: vInfo.BuildDate,
		BuildArch: vInfo.Platform,
	})

	// Add subcommands
	rootCmd.AddCommand(newBootstrapCmd())
	rootCmd.AddCommand(newBuildCmd())
	rootCmd.AddCommand(newCheckCmd())
	rootCmd.AddCommand(newDepsCmd())
	rootCmd.AddCommand(newExposeCmd())
	rootCmd.AddCommand(newFmtCmd())
	rootCmd.AddCommand(newHideCmd())
	// No newForgeCmd(): `grove forge` is no longer compiled in. Provisioning a
	// forge is a REFERENCE DEPLOYMENT (GCP + WireGuard + Forgejo/syncd), not
	// the product, so it ships as the grove-plugin-forge-gcp recipe building a
	// binary named `forge`. The RunE fallback below already delegates any
	// unrecognized first argument to <bin dir>/<name>, so `grove forge up`
	// resolves it with no dispatch code here. What grove KEEPS is the client
	// contract: [forge]/[forge.poll] config, core/pkg/forge, the daemon poller
	// and every read surface over it.
	rootCmd.AddCommand(newKeysCmd())
	rootCmd.AddCommand(newLintCmd())
	rootCmd.AddCommand(newMuxCmd())
	rootCmd.AddCommand(newPluginCmd())
	rootCmd.AddCommand(newRecordCmd())
	rootCmd.AddCommand(newSatelliteCmd())
	rootCmd.AddCommand(newSchemaCmd())
	rootCmd.AddCommand(newSetupCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newTestCmd())
	rootCmd.AddCommand(newTestReportCmd())
	rootCmd.AddCommand(newVetCmd())
	rootCmd.AddCommand(internal.NewInternalCmd())

	// Register deprecated command shims for backwards compatibility
	registerDeprecatedCommands(rootCmd)

	// Set up the root command's RunE to handle tool delegation
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}

		// If it's not a known command, try to delegate to an installed tool
		toolName := args[0]
		toolArgs := args[1:]

		return delegateToTool(toolName, toolArgs)
	}

	// Allow arbitrary args for tool delegation
	rootCmd.FParseErrWhitelist.UnknownFlags = true
	rootCmd.Args = cobra.ArbitraryArgs // Allow any arguments to be passed

	// Use core styled help with custom "AVAILABLE TOOLS" section
	cli.SetStyledHelpWithExtras(rootCmd, printAvailableTools)
}

// printAvailableTools prints the available ecosystem tools in table format
func printAvailableTools(t *theme.Theme) {
	// Get all tools and their info
	type toolRow struct {
		binary, description, repo string
	}
	var tools []toolRow
	maxBinaryLen := len("BINARY")
	maxDescLen := len("DESCRIPTION")
	for repo, info := range sdk.GetToolRegistry() {
		// Skip grove itself - it's self-referential
		if info.Alias == "grove" {
			continue
		}
		desc := info.Description
		if desc == "" {
			desc = "-"
		}
		tools = append(tools, toolRow{info.Alias, desc, repo})
		if len(info.Alias) > maxBinaryLen {
			maxBinaryLen = len(info.Alias)
		}
		if len(desc) > maxDescLen {
			maxDescLen = len(desc)
		}
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].binary < tools[j].binary
	})

	// Helper to pad string to width
	pad := func(s string, width int) string {
		if len(s) >= width {
			return s
		}
		return s + strings.Repeat(" ", width-len(s))
	}

	// Use a blue style for tool names
	blue := t.Bold.Foreground(t.Colors.Blue)

	fmt.Println("\n " + t.Bold.Render("AVAILABLE TOOLS"))
	fmt.Printf(" %s  %s  %s\n",
		t.Muted.Render(pad("BINARY", maxBinaryLen)),
		t.Muted.Render(pad("DESCRIPTION", maxDescLen)),
		t.Muted.Render("REPO"))
	for _, row := range tools {
		fmt.Printf(" %s  %s  %s\n",
			blue.Render(pad(row.binary, maxBinaryLen)),
			pad(row.description, maxDescLen),
			t.Muted.Render(row.repo))
	}

	// Examples
	cyan := t.Bold.Foreground(t.Colors.Cyan)
	fmt.Println("\n " + t.Muted.Render("Command examples:"))
	fmt.Printf("   %s %s  %s\n", cyan.Render("grove"), blue.Render(pad("mux", 16)), t.Muted.Render("# Open the treemux cockpit"))
	fmt.Printf("   %s %s  %s\n", cyan.Render("grove"), blue.Render(pad("install cx", 16)), t.Muted.Render("# Install a tool"))
	fmt.Printf("   %s %s  %s\n", cyan.Render("grove"), blue.Render(pad("setup", 16)), t.Muted.Render("# Run setup wizard"))
	fmt.Println("\n " + t.Muted.Render("Tool examples:"))
	fmt.Printf("   %s %s  %s\n", cyan.Render("grove"), blue.Render(pad("cx stats", 16)), t.Muted.Render("# Show context statistics"))
	fmt.Printf("   %s %s  %s\n", cyan.Render("grove"), blue.Render(pad("nb tui", 16)), t.Muted.Render("# Open notebook TUI"))
	fmt.Printf("   %s %s  %s\n", cyan.Render("grove"), blue.Render(pad("flow status", 16)), t.Muted.Render("# Show flow plan status"))
	fmt.Printf("   %s %s  %s\n", cyan.Render("grove"), blue.Render(pad("gmux sessionize", 16)), t.Muted.Render("# Create tmux session"))
}

// Execute runs the root command
func Execute() error {
	// argv[0] dispatch, ahead of everything: this process may BE a tool. `grove
	// expose cx` symlinks ~/.local/bin/cx at the grove binary, so an invocation
	// arriving as `cx stats` is a request for cx, not for grove. Deciding it
	// here — before cobra sees a flag, before the registry short-circuit below —
	// is what lets an exposed name behave exactly like the tool it names,
	// including `cx --help` and `nb -v`.
	if tool, ok := argv0Delegation(os.Args[0]); ok {
		if err := delegateToTool(tool, os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		return nil
	}

	// Check if the first argument is a known tool - delegate BEFORE cobra parses flags
	if len(os.Args) > 1 {
		potentialTool := os.Args[1]

		// Skip if it looks like a flag (let cobra handle it)
		if !strings.HasPrefix(potentialTool, "-") && !builtinClaimsArgs(potentialTool, os.Args[2:]) {
			// Check if it's a registered tool in our ecosystem
			if repoName, _, alias, found := sdk.FindTool(potentialTool); found {
				// Delegate using the tool's effective alias — that is the
				// binary's real name. Delegating the identifier as typed broke
				// every tool whose alias differs from its repo name: `grove
				// daemon status` searched for a binary literally named
				// "daemon" (the binary is "groved") and failed.
				binName := alias
				if binName == "" {
					binName = repoName
				}
				// Delegate immediately with all remaining args (including -h, --help, etc.)
				if err := delegateToTool(binName, os.Args[2:]); err != nil {
					// This path bypasses cobra, so nothing downstream prints
					// the error — surface it here instead of exiting silently.
					fmt.Fprintln(os.Stderr, "Error:", err)
					os.Exit(1)
				}
				return nil
			}
		}
	}

	return rootCmd.Execute()
}

// argv0Delegation is the impure wrapper around argv0Tool: it supplies the
// exposure ledger and the tool registry. The ledger is read only on the path
// where argv[0] is not grove, so a normal `grove ...` invocation pays nothing.
func argv0Delegation(argv0 string) (string, bool) {
	base := strings.TrimSuffix(filepath.Base(argv0), ".exe")
	if base == "grove" || base == "." || base == string(os.PathSeparator) || base == "" {
		return "", false
	}
	return argv0Tool(base, loadExposures(), registryBinary)
}

// argv0Tool decides whether an invocation arriving under the name argv0 should
// be delegated to a tool, and to which binary. It is pure — the exposure ledger
// and the registry come in as arguments — so the decision is testable without
// a filesystem, an exec, or a real ~/.local/bin.
//
// Two ways a name can dispatch, and no third:
//
//   - the ledger maps it (this is how `grove expose nb --as gnb` works; nothing
//     about the name `gnb` says nb), but only if the tool it names is STILL
//     registered — a stale ledger must not become a passthrough;
//   - the name is itself a registered repo name or binary alias.
//
// Everything else returns false and gets grove's own behavior. That is the
// whole safety property: an unrecognized argv[0] never turns grove into a
// launcher for arbitrary binaries.
func argv0Tool(argv0 string, exposures map[string]string, registry func(string) (string, bool)) (string, bool) {
	base := strings.TrimSuffix(filepath.Base(argv0), ".exe")
	if base == "" || base == "grove" || base == "." || base == ".." || base == string(os.PathSeparator) {
		return "", false
	}
	if tool, ok := exposures[base]; ok && tool != "" {
		if binary, known := registry(tool); known {
			return binary, true
		}
		return "", false
	}
	return registry(base)
}

// registryBinary resolves a registered repo name or alias to the binary name
// that actually answers for it (`daemon` → `groved`).
func registryBinary(name string) (string, bool) {
	repoName, _, alias, found := sdk.FindTool(name)
	if !found {
		return "", false
	}
	if alias != "" {
		return alias, true
	}
	return repoName, true
}

// builtinClaimsArgs reports whether grove's OWN command tree handles
// `grove <name> <rest...>`, and delegation must therefore stand down.
//
// The delegation shortcut above runs before cobra parses anything, keyed only
// on the registered tool name. That is right for `grove flow`, `grove nb`,
// `grove treemux` — names grove has no command for. It is wrong when a
// registered REPO name collides with a built-in command: `sync` is both an
// ecosystem repo (alias grove-syncd) and grove's notebook-sync command, and
// the shortcut sent every `grove sync ...` to the server binary. That made
// `grove sync doctor` and `grove sync adopt` unreachable — not shadowed in
// help, unreachable, answering "unknown command for grove-syncd".
//
// The rule is deliberately narrow: the built-in wins only for the subcommands
// it actually declares. `grove sync serve` and `grove sync token create` still
// reach grove-syncd, because grove declares no such subcommands — so no
// existing delegation is taken away.
func builtinClaimsArgs(name string, rest []string) bool {
	var builtin *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == name {
			builtin = c
			break
		}
	}
	if builtin == nil {
		return false
	}
	// A built-in with no subcommands of its own owns the whole invocation.
	if !builtin.HasSubCommands() {
		return true
	}
	// Otherwise the first non-flag token decides. No token at all (`grove sync`,
	// `grove sync --help`) means the built-in's own help, which is the surface
	// that can point at both halves.
	for _, arg := range rest {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		for _, sub := range builtin.Commands() {
			if sub.Name() == arg {
				return true
			}
			for _, alias := range sub.Aliases {
				if alias == arg {
					return true
				}
			}
		}
		return false
	}
	return true
}

// reservedToolVerbs is every name `grove <verb>` already answers to without
// consulting the plugin lockfile: the root command tree (names and aliases)
// and the sdk tool registry (repo names and binary aliases). It is what the
// plugin installer refuses tool verbs against, and what `grove plugin list`
// checks installed verbs against to report shadowing — the same list on both
// ends, so the install-time refusal and the list-time warning can never
// disagree about which names grove owns.
func reservedToolVerbs() []string {
	seen := make(map[string]bool)
	var out []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, c := range rootCmd.Commands() {
		add(c.Name())
		for _, alias := range c.Aliases {
			add(alias)
		}
	}
	for repo, info := range sdk.GetToolRegistry() {
		add(repo)
		add(info.Alias)
	}
	return out
}

// toolPluginBinary resolves toolName against the verbs installed tool plugins
// declared, reading the lockfile only on the miss path — a verb that resolved
// to a real binary never pays for this. A resolvable verb whose binary is
// gone is an ERROR naming the remedy, not a fall-through: the user installed
// something that answers this command, and "unknown tool" would deny it.
func toolPluginBinary(toolName string) (string, error) {
	lock, err := coreplugin.LoadLock()
	if err != nil {
		// A corrupt lockfile must not take down delegation — `grove plugin`
		// is where it gets reported and repaired.
		return "", nil //nolint:nilerr // deliberate: fall through to the other resolutions
	}
	name, pin, ok := plugin.ResolveToolVerb(lock, toolName)
	if !ok {
		return "", nil
	}
	if _, statErr := os.Stat(pin.Binary); statErr != nil {
		return "", fmt.Errorf("`grove %s` is provided by the %s plugin, but its binary is missing (%s) — rebuild it with 'grove plugin update %s --force'", toolName, name, pin.Binary, name)
	}
	return pin.Binary, nil
}

// findWorkspaceRoot uses grove-core's workspace detection to find the workspace root.
// This properly handles all workspace types: standalone projects, ecosystem roots,
// worktrees, and sub-projects.
func findWorkspaceRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	node, err := workspace.GetProjectByPath(cwd)
	if err != nil {
		return ""
	}

	return node.Path
}

// toolResolution is where a delegated tool's binary was found, plus what the
// child's environment has to say about finding OTHER grove tools. Resolution
// and execution are separate so `grove mux` can reuse the exact same lookup
// (and the same PATH capsule) while replacing the process instead of forking.
type toolResolution struct {
	// Path is the binary to run.
	Path string
	// pathPrepend are directories that go in front of the inherited PATH, in
	// order. Workspace bin dirs land here so a workspace build shadows the
	// managed toolchain; the toolchain dir itself is appended by Env().
	pathPrepend []string
	// extraEnv are additional KEY=VALUE entries for the child.
	extraEnv []string
}

// Env builds the child environment: this process's environment with the PATH
// capsule applied.
//
// The capsule is the reason grove can be the only name on the user's global
// PATH. paths.BinDir() (the private toolchain dir) is prepended for every
// delegated child, so everything grove spawns — and everything THOSE processes
// spawn, since env is inherited verbatim — still resolves bare `nb`, `flow`,
// `cx`, `groved` even when the user's own shell has never heard of them.
func (r toolResolution) Env() []string {
	prepend := r.pathPrepend
	if binDir := paths.BinDir(); binDir != "" {
		prepend = append(append([]string{}, prepend...), binDir)
	}
	if len(prepend) == 0 && len(r.extraEnv) == 0 {
		return nil
	}

	env := os.Environ()
	if len(prepend) > 0 {
		newPath := capsulePATH(os.Getenv("PATH"), prepend)
		pathSet := false
		for i, entry := range env {
			if strings.HasPrefix(entry, "PATH=") {
				env[i] = "PATH=" + newPath
				pathSet = true
				break
			}
		}
		if !pathSet {
			env = append(env, "PATH="+newPath)
		}
	}
	return append(env, r.extraEnv...)
}

// capsulePATH returns current with prepend in front of it, in order, and every
// prepended dir removed from the rest so repeated delegation (grove → tool →
// grove) can't grow PATH without bound. A PATH that already leads with exactly
// these dirs is returned unchanged.
func capsulePATH(current string, prepend []string) string {
	if len(prepend) == 0 {
		return current
	}
	existing := filepath.SplitList(current)

	seen := make(map[string]bool, len(prepend))
	head := make([]string, 0, len(prepend))
	for _, dir := range prepend {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		head = append(head, dir)
	}
	if len(head) == 0 {
		return current
	}

	out := make([]string, 0, len(head)+len(existing))
	out = append(out, head...)
	for _, dir := range existing {
		if seen[dir] {
			continue
		}
		out = append(out, dir)
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// delegateToTool attempts to run an installed Grove tool.
// By default, it uses globally managed binaries (global-first).
// Set GROVE_DELEGATION_MODE=workspace to opt-in to workspace-aware delegation.
func delegateToTool(toolName string, args []string) error {
	res, ok, err := resolveTool(toolName)
	if err != nil {
		return err
	}
	if !ok {
		// A registered tool that simply is not installed gets the install
		// command, not a spelling lecture — that is now the ONLY reason a
		// known name fails to resolve.
		if _, _, _, registered := sdk.FindTool(toolName); registered {
			return fmt.Errorf("%s is not installed — run 'grove install %s'", toolName, toolName)
		}
		return fmt.Errorf("unknown tool: %s. Run 'grove install %s', see 'grove plugin list' for plugin-provided commands, or check spelling.", toolName, toolName)
	}

	// Execute the binary
	cmd := exec.Command(res.Path, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = res.Env()

	if err := cmd.Run(); err != nil {
		// Propagate the child's exit code so callers (including Claude Code's
		// asyncRewake at exit 2) see the real status. Without this, Cobra
		// rewrites every non-zero exit to 1.
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

// resolveTool locates the binary that answers `grove <toolName>`, without
// running it. ok=false means nothing provides the tool at all — the CALLER
// words that, because `grove nb` and `grove mux` want different remedies.
// A non-nil error means a provider was found but is unusable (e.g. a plugin
// whose binary has gone missing), which is never a fall-through.
func resolveTool(toolName string) (toolResolution, bool, error) {
	logger := logging.NewLogger("grove-meta")
	logger.WithField("tool", toolName).Debug("Delegating to tool")

	var res toolResolution
	var toolPath string
	delegationMode := delegation.GetMode()

	// Check if we're in a workspace for potential overrides
	workspaceRoot := findWorkspaceRoot()

	// PRIORITY 1: Check for workspace-specific binary overrides
	if workspaceRoot != "" {
		if overridePath := overrides.GetBinaryOverride(workspaceRoot, toolName); overridePath != "" {
			// Verify the override binary still exists
			if _, err := os.Stat(overridePath); err == nil {
				toolPath = overridePath
				logger.WithField("path", toolPath).Debug("Using workspace override binary")
			} else {
				logger.WithField("path", overridePath).Warn("Workspace override binary not found, ignoring")
			}
		}
	}

	// PRIORITY 2: Check for workspace-local binaries (if delegation mode is workspace)
	if toolPath == "" && delegationMode == delegation.ModeWorkspace {
		logger.Debug("Delegation mode is workspace; attempting workspace-aware binary discovery.")
		if workspaceRoot != "" {
			logger.WithField("workspace", workspaceRoot).Debug("Found workspace root")
			// Try to find the binary in this workspace
			workspaceBinaries, err := meta_workspace.DiscoverLocalBinaries(workspaceRoot)
			if err == nil {
				var foundBinary *meta_workspace.BinaryMeta
				for i, binary := range workspaceBinaries {
					if binary.Name == toolName {
						// Check if the binary actually exists
						if _, err := os.Stat(binary.Path); err == nil {
							foundBinary = &workspaceBinaries[i]
							break
						}
					}
				}

				if foundBinary != nil {
					toolPath = foundBinary.Path
					logger.WithField("path", toolPath).Debug("Using workspace binary")

					// Build PATH with all workspace bin directories first for correct inter-tool calls
					var binDirs []string
					seenDirs := make(map[string]bool)
					for _, b := range workspaceBinaries {
						binDir := filepath.Dir(b.Path)
						if !seenDirs[binDir] {
							binDirs = append(binDirs, binDir)
							seenDirs[binDir] = true
						}
					}
					if len(binDirs) > 0 {
						// Workspace bins go in FRONT of the capsule's
						// toolchain dir (Env() appends that one), so a
						// locally built tool still wins over its released
						// twin while the rest of the toolchain stays
						// reachable by bare name.
						res.pathPrepend = binDirs
						res.extraEnv = append(res.extraEnv, "GROVE_WORKSPACE_ROOT="+workspaceRoot)
					}
				}
			}
		}
	}

	// PRIORITY 3 (DEFAULT): Fall back to the globally managed binary.
	// This block is executed if the opt-in is not active, or if a local binary
	// was not found in the workspace.
	if toolPath == "" {
		toolPath = filepath.Join(paths.BinDir(), toolName)
		logger.WithField("path", toolPath).Debug("Using global binary")

		// Check if the tool exists
		if _, err := os.Stat(toolPath); os.IsNotExist(err) {
			// PRIORITY 4: a verb an installed TOOL PLUGIN provides. Resolved
			// from the lockfile (cheap JSON), and only on this miss path, so
			// delegation to a real binary never pays for it. It runs before
			// the PATH fallback because a verb can differ from the binary's
			// name — `grove forge` may run a binary called something else —
			// and only the lockfile knows the mapping.
			//
			// There is deliberately NO further fallback to a same-named
			// binary on PATH. grove is the only name the install puts in the
			// user's namespace, so a foreign `nb` or `flow` out there is
			// somebody else's tool; running it because grove's own is missing
			// would be a silent substitution. An uninstalled tool says so, and
			// names the install command, instead.
			if pluginBinary, err := toolPluginBinary(toolName); err != nil {
				return res, false, err
			} else if pluginBinary != "" {
				toolPath = pluginBinary
				logger.WithField("path", toolPath).Debug("Using plugin-provided tool binary")
			} else {
				return res, false, nil
			}
		}
	}

	res.Path = toolPath
	return res, true, nil
}
