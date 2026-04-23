package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/env"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/core/state"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func init() {
	rootCmd.AddCommand(newEnvCmd())
	rootCmd.AddCommand(newEnvExecCmd())
}

func newEnvCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Manage and inspect project environments",
	}

	cmd.AddCommand(newEnvListCmd())
	cmd.AddCommand(newEnvShowCmd())
	cmd.AddCommand(newEnvDefaultCmd())
	cmd.AddCommand(newEnvCmdRunCmd())
	cmd.AddCommand(newEnvUpCmd())
	cmd.AddCommand(newEnvDownCmd())
	cmd.AddCommand(newEnvStatusCmd())
	cmd.AddCommand(newEnvRestartCmd())
	cmd.AddCommand(newEnvVarsCmd())
	cmd.AddCommand(newEnvTUICmd())
	cmd.AddCommand(newEnvDriftCmd())
	cmd.AddCommand(newEnvEcosystemCmd())
	cmd.AddCommand(newEnvPruneCmd())

	return cmd
}

// envStateDir returns the path to .grove/env/ in the current working directory.
func envStateDir() string {
	return filepath.Join(".", ".grove", "env")
}

// envStatePath returns the path to .grove/env/state.json in the current working directory.
func envStatePath() string {
	return filepath.Join(envStateDir(), "state.json")
}

// envLastProfilePath returns the path to the sidecar file that persists the
// most recently used profile across `env down`. Consulted by `grove env cmd`
// when no env is active so `env cmd <name>` resolves against the profile the
// user just ran instead of silently falling back to the `default` provider.
func envLastProfilePath() string {
	return filepath.Join(envStateDir(), "last_profile")
}

// writeLastProfile persists the most recently used profile so `env cmd` can
// fall back to it after `env down`. Errors are non-fatal — the sidecar only
// improves UX; env up/down should not fail because of it.
func writeLastProfile(profile string) {
	if profile == "" {
		profile = "default"
	}
	if err := os.MkdirAll(envStateDir(), 0755); err != nil {
		return
	}
	_ = os.WriteFile(envLastProfilePath(), []byte(profile+"\n"), 0644)
}

// readLastProfile returns the persisted last-used profile or "" if none was
// recorded.
func readLastProfile() string {
	data, err := os.ReadFile(envLastProfilePath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// resolveEnvCmdProfile picks the profile name `grove env cmd` should use.
// Precedence:
//  1. explicit --env flag
//  2. active env's profile (state.json from a running env up)
//  3. last-used profile sidecar (survives env down; prevents silent
//     fall-through to the `default` provider after teardown)
//  4. clear error listing available profiles
//
// Pre-sidecar, `grove env cmd load` after `env down` silently resolved to the
// default profile. For profiles that expect ports only the previous profile
// allocated (CLICKHOUSE_PORT from docker-local, for example), that emitted
// cobra usage + "connection refused" instead of a useful error — which is
// what triggered adding this helper.
//
// Returns (profile, optional note to print to stderr, error).
func resolveEnvCmdProfile(envFlag string, cfg *config.Config) (string, string, error) {
	if envFlag != "" {
		return envFlag, "", nil
	}
	if sf, err := readEnvState(); err == nil && sf != nil {
		return sf.Environment, "", nil
	}
	if last := readLastProfile(); last != "" {
		note := fmt.Sprintf("grove: using last-active profile %q (no active env; pass --env to override)\n", last)
		return last, note, nil
	}
	profiles := availableProfiles(cfg)
	profilesStr := "(none configured)"
	if len(profiles) > 0 {
		profilesStr = strings.Join(profiles, ", ")
	}
	return "", "", fmt.Errorf("no active environment in this worktree and no last-used profile recorded.\n  hint: pass --env <profile> or run 'grove env up' first.\n  available profiles: %s", profilesStr)
}

// availableProfiles returns the names of configured profiles from grove.toml
// (including the unnamed default), sorted for stable error-message output.
func availableProfiles(cfg *config.Config) []string {
	names := []string{}
	if cfg != nil && cfg.Environment != nil {
		names = append(names, "default")
	}
	if cfg != nil {
		for k := range cfg.Environments {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	return names
}

// readEnvState reads and parses the state file from .grove/env/state.json.
func readEnvState() (*env.EnvStateFile, error) {
	data, err := os.ReadFile(envStatePath())
	if err != nil {
		return nil, err
	}
	var stateFile env.EnvStateFile
	if err := json.Unmarshal(data, &stateFile); err != nil {
		return nil, fmt.Errorf("failed to parse state.json: %w", err)
	}
	return &stateFile, nil
}

// writeEnvLocal writes env vars to .grove/env/.env.local and .env.local (symlink or copy).
func writeEnvLocal(envVars map[string]string) error {
	if len(envVars) == 0 {
		return nil
	}

	keys := make([]string, 0, len(envVars))
	for k := range envVars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var content strings.Builder
	for _, k := range keys {
		content.WriteString(fmt.Sprintf("%s=%s\n", k, envVars[k]))
	}

	// Write to .grove/env/.env.local
	if err := os.MkdirAll(envStateDir(), 0755); err != nil {
		return err
	}
	envPath := filepath.Join(envStateDir(), ".env.local")
	if err := os.WriteFile(envPath, []byte(content.String()), 0644); err != nil {
		return err
	}

	// Also write to project root .env.local for tool compatibility
	return os.WriteFile(filepath.Join(".", ".env.local"), []byte(content.String()), 0644)
}

// resolveEnvConfig resolves the environment config for the active/specified profile.
func resolveEnvConfig(envProfile string) (*config.EnvironmentConfig, string, error) {
	cfg, err := config.LoadDefault()
	if err != nil {
		return nil, "", err
	}

	profile := envProfile
	if profile == "" {
		profile, _ = state.GetString("environment")
	}

	resolved, err := config.ResolveEnvironment(cfg, profile)
	if err != nil {
		return nil, "", err
	}

	return resolved, profile, nil
}

// getWorkspaceNode returns the workspace node for the current directory.
//
// When called from inside a worktree (a directory under `.grove-worktrees/`),
// the daemon keys its RunningEnv map by Workspace.Name and resolves relative
// volume host_paths against Workspace.Path. Without this patch, an `env down`
// call from `.grove-worktrees/<name>/` can send the ecosystem name/path to
// the daemon, which looks up the wrong map entry and silently no-ops — the
// same bug class fixed in flow/cmd/plan_init.go (commit b935ae4).
//
// Patch in two cases: (1) the returned node is already a worktree kind (its
// Name should be the directory basename, which we normalize), and (2) the
// returned node is the parent ecosystem because the cwd happens to sit in a
// `.grove-worktrees/<name>/` subtree. In both cases, force Name and Path to
// the concrete worktree directory.
func getWorkspaceNode() *workspace.WorkspaceNode {
	cwd, _ := os.Getwd()
	node, err := workspace.GetProjectByPath(cwd)
	if err != nil || node == nil {
		return nil
	}
	if wtRoot := worktreeRootFromPath(cwd); wtRoot != "" {
		if node.Name != filepath.Base(wtRoot) || node.Path != wtRoot {
			patched := *node
			patched.Name = filepath.Base(wtRoot)
			patched.Path = wtRoot
			return &patched
		}
		return node
	}
	if node.IsWorktree() {
		patched := *node
		patched.Name = filepath.Base(node.Path)
		return &patched
	}
	return node
}

// worktreeRootFromPath returns the absolute path to the nearest ancestor that
// lives directly under a `.grove-worktrees` directory, or "" if there is none.
func worktreeRootFromPath(cwd string) string {
	cur, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		if filepath.Base(parent) == ".grove-worktrees" {
			return cur
		}
		cur = parent
	}
}

func newEnvUpCmd() *cobra.Command {
	var envProfile string
	var jsonOutput bool
	var rebuildFlag string
	var rebuildSet bool

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start the project environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, profile, err := resolveEnvConfig(envProfile)
			if err != nil {
				return err
			}
			if resolved.Provider == "" {
				return fmt.Errorf("no provider configured for environment")
			}

			// Check if already running
			if _, err := readEnvState(); err == nil {
				return fmt.Errorf("environment already running; use 'grove env restart' or 'grove env down' first")
			}

			cwd, _ := os.Getwd()
			stateDir, _ := filepath.Abs(envStateDir())
			if err := os.MkdirAll(stateDir, 0755); err != nil {
				return fmt.Errorf("failed to create state directory: %w", err)
			}

			req := env.EnvRequest{
				Provider:  resolved.Provider,
				Profile:   profile,
				StateDir:  stateDir,
				PlanDir:   stateDir, // backward compat
				Config:    resolved.Config,
				ManagedBy: "user",
			}

			// --rebuild plumbing:
			//   (no flag)           → Rebuild = nil (respect fingerprint cache)
			//   --rebuild           → Rebuild = ["all"]
			//   --rebuild=api,web   → Rebuild = ["api", "web"]
			if rebuildSet {
				if rebuildFlag == "" {
					req.Rebuild = []string{"all"}
				} else {
					parts := strings.Split(rebuildFlag, ",")
					for i := range parts {
						parts[i] = strings.TrimSpace(parts[i])
					}
					req.Rebuild = parts
				}
			}

			// Attach workspace node
			if node := getWorkspaceNode(); node != nil {
				req.Workspace = node
			}

			// Forward display_endpoints into the provider config so the daemon
			// can honor the filter when mapping terraform outputs.
			if len(resolved.DisplayEndpoints) > 0 {
				if req.Config == nil {
					req.Config = make(map[string]interface{})
				}
				list := make([]interface{}, 0, len(resolved.DisplayEndpoints))
				for _, name := range resolved.DisplayEndpoints {
					list = append(list, name)
				}
				req.Config["display_endpoints"] = list
			}

			// Phase 4: Prepare shared_backend_config if shared_env is configured.
			// Logic lives in core/pkg/env so flow/cmd/plan_init.go reuses it.
			env.ApplySharedBackendConfig(&req)

			// Resolve provider
			var client env.DaemonEnvClient
			if resolved.Provider == "native" || resolved.Provider == "docker" || resolved.Provider == "terraform" {
				client = daemon.NewWithAutoStart()
			}
			prov := env.ResolveProvider(resolved.Provider, client, resolved.Command)

			if !jsonOutput {
				fmt.Printf("Starting environment via %s...\n", resolved.Provider)
			}

			resp, err := prov.Up(context.Background(), req)
			if err != nil {
				return fmt.Errorf("environment startup failed: %w", err)
			}

			// state.json is now written by the daemon (Manager.Up). The client
			// only writes .env.local for tooling that reads it directly.
			if err := writeEnvLocal(resp.EnvVars); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not write .env.local: %v\n", err)
			}
			// Sidecar: remember this profile so post-`env down` `env cmd`
			// calls fall back to it rather than silently resolving default.
			writeLastProfile(profile)

			if jsonOutput {
				json.NewEncoder(os.Stdout).Encode(resp)
			} else {
				fmt.Printf("Environment started (%s)\n", resp.Status)
				if len(resp.EnvVars) > 0 {
					fmt.Println("Environment variables written to .env.local")
				}
				for _, ep := range resp.Endpoints {
					fmt.Printf("  %s\n", ep)
				}
				_ = cwd // suppress unused
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&envProfile, "env", "", "Override the active environment profile")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	// --rebuild / --rebuild=svc,svc: force rebuild of all or selected images,
	// bypassing the fingerprint cache. Declared as an optional-argument flag
	// so `--rebuild` (no value) also works.
	rebuildFlagObj := cmd.Flags().VarPF(&rebuildValue{v: &rebuildFlag, set: &rebuildSet}, "rebuild", "", "Force rebuild of images (all if no value, or comma-separated service names)")
	rebuildFlagObj.NoOptDefVal = " "
	return cmd
}

// rebuildValue implements pflag.Value so --rebuild can be used either as
// a bare flag (force rebuild everything) or with a value (--rebuild=api,web).
type rebuildValue struct {
	v   *string
	set *bool
}

func (r *rebuildValue) String() string { return *r.v }
func (r *rebuildValue) Set(s string) error {
	// NoOptDefVal passes " " when the flag is bare; normalize to empty.
	if s == " " {
		s = ""
	}
	*r.v = s
	*r.set = true
	return nil
}
func (r *rebuildValue) Type() string { return "string" }

func newEnvDownCmd() *cobra.Command {
	var jsonOutput bool
	var force bool
	var clean bool

	cmd := &cobra.Command{
		Use:   "down",
		Short: "Stop the project environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			stateFile, err := readEnvState()
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println("No environment running")
					return nil
				}
				return err
			}

			absStateDir, _ := filepath.Abs(envStateDir())

			// Re-resolve config from stored profile so terraform down has backend/vars info
			downConfig := make(map[string]interface{})
			if stateFile.Environment != "" || stateFile.Provider == "terraform" {
				if resolved, _, err := resolveEnvConfig(stateFile.Environment); err == nil && resolved.Config != nil {
					downConfig = resolved.Config
				}
			}

			req := env.EnvRequest{
				Provider:  stateFile.Provider,
				StateDir:  absStateDir,
				PlanDir:   absStateDir,
				Config:    downConfig,
				ManagedBy: stateFile.ManagedBy,
				Force:     force,
				Clean:     clean,
			}

			if node := getWorkspaceNode(); node != nil {
				req.Workspace = node
			}

			var client env.DaemonEnvClient
			if stateFile.Provider == "native" || stateFile.Provider == "docker" || stateFile.Provider == "terraform" {
				client = daemon.NewWithAutoStart()
			}
			prov := env.ResolveProvider(stateFile.Provider, client, stateFile.Command)

			if !jsonOutput {
				fmt.Printf("Stopping %s environment...\n", stateFile.Provider)
			}

			// Snapshot the profile to a sidecar BEFORE teardown so
			// `grove env cmd` can fall back to it later. state.json is
			// removed by the daemon on a successful Down, which would
			// otherwise lose this information.
			snapshotProfile := stateFile.LastProfile
			if snapshotProfile == "" {
				snapshotProfile = stateFile.Environment
			}
			if snapshotProfile == "" {
				snapshotProfile = "default"
			}
			writeLastProfile(snapshotProfile)

			if err := prov.Down(context.Background(), req); err != nil {
				return fmt.Errorf("environment teardown failed: %w", err)
			}

			// Clean up volumes based on persistence and --clean flag
			for _, vol := range stateFile.EffectiveVolumes() {
				if clean || !vol.Persist {
					target := filepath.Join(".", vol.Path)
					if err := os.RemoveAll(target); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", vol.Path, err)
					}
				}
			}

			// state.json is removed by the daemon on successful Down. The
			// client only cleans up .env.local artifacts.
			os.Remove(filepath.Join(envStateDir(), ".env.local"))
			os.Remove(filepath.Join(".", ".env.local"))

			// Remove empty .grove/env/ directory
			os.Remove(envStateDir())

			if jsonOutput {
				json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "stopped"})
			} else {
				fmt.Println("Environment stopped")
			}

			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&force, "force", false, "Force teardown even if managed by a plan")
	cmd.Flags().BoolVar(&clean, "clean", false, "Remove all volumes including persistent ones")
	return cmd
}

func newEnvStatusCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "status [worktree]",
		Short: "Show the current environment status",
		Long: `Show the status of the environment for the current worktree, or — if a
worktree name is given — the status the daemon is tracking for that worktree.

Output labels:
  [Active (Running)]  — the profile currently running in this worktree
  [Sticky Default]    — the profile set via 'grove env default' (.grove/state.yml)
  [Config Default]    — the '[environment]' block from grove.toml`,
		Args: cobra.MaximumNArgs(1),
		Example: `  grove env status
  grove env status my-feature
  grove env status --json | jq '{status, state}'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return runEnvStatusForWorktree(args[0], jsonOutput)
			}
			return runEnvStatusLocal(jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

// runEnvStatusLocal prints the status of the environment running in the
// current worktree by reading .grove/env/state.json. It also surfaces the
// sticky default and config default so the caller can distinguish those
// from "currently running".
func runEnvStatusLocal(jsonOutput bool) error {
	stateFile, err := readEnvState()
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	sticky, _ := state.GetString("environment")
	cfg, _ := config.LoadDefault()
	configDefault := ""
	if cfg != nil && cfg.Environment != nil {
		configDefault = "default"
	}

	if stateFile == nil {
		if jsonOutput {
			payload := map[string]interface{}{
				"status":         "stopped",
				"sticky_default": sticky,
				"config_default": configDefault,
			}
			json.NewEncoder(os.Stdout).Encode(payload)
			return nil
		}
		fmt.Println("No environment running")
		if sticky != "" {
			fmt.Printf("Sticky Default:  %s\n", sticky)
		}
		if configDefault != "" {
			fmt.Printf("Config Default:  %s\n", configDefault)
		}
		return nil
	}

	if jsonOutput {
		payload := map[string]interface{}{
			"status":         "running",
			"state":          stateFile,
			"sticky_default": sticky,
			"config_default": configDefault,
		}
		json.NewEncoder(os.Stdout).Encode(payload)
		return nil
	}

	fmt.Printf("Provider:        %s\n", stateFile.Provider)
	activeProfile := stateFile.Environment
	if activeProfile == "" {
		activeProfile = "default"
	}
	fmt.Printf("Active Profile:  %s [Active (Running)]\n", activeProfile)
	if sticky != "" {
		fmt.Printf("Sticky Default:  %s\n", sticky)
	}
	if configDefault != "" {
		fmt.Printf("Config Default:  %s\n", configDefault)
	}
	fmt.Printf("Managed by:      %s\n", stateFile.ManagedBy)

	if len(stateFile.Services) > 0 {
		fmt.Println("\nServices:")
		for _, svc := range stateFile.Services {
			portStr := ""
			if svc.Port > 0 {
				portStr = fmt.Sprintf(" (port %d)", svc.Port)
			}
			fmt.Printf("  %-20s %s%s\n", svc.Name, svc.Status, portStr)
		}
	}

	if len(stateFile.Ports) > 0 {
		fmt.Println("\nPorts:")
		for name, port := range stateFile.Ports {
			fmt.Printf("  %-20s %d\n", name, port)
		}
	}

	return nil
}

// runEnvStatusForWorktree queries the daemon for the environment status of a
// specific worktree (e.g. a sibling worktree managed by a plan), rather than
// the local worktree's on-disk state.
func runEnvStatusForWorktree(worktree string, jsonOutput bool) error {
	client := daemon.New()
	resp, err := client.EnvStatus(context.Background(), worktree)
	if err != nil {
		return fmt.Errorf("failed to query daemon for worktree %q: %w", worktree, err)
	}

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(resp)
		return nil
	}

	if resp == nil || resp.Status == "stopped" || resp.Status == "" {
		fmt.Printf("No environment running for worktree %q\n", worktree)
		return nil
	}

	fmt.Printf("Worktree:        %s\n", worktree)
	if prov, ok := resp.State["provider"]; ok && prov != "" {
		fmt.Printf("Provider:        %s\n", prov)
	}
	if envProfile, ok := resp.State["environment"]; ok && envProfile != "" {
		fmt.Printf("Active Profile:  %s [Active (Running)]\n", envProfile)
	}
	if mb, ok := resp.State["managed_by"]; ok && mb != "" {
		fmt.Printf("Managed by:      %s\n", mb)
	}
	fmt.Printf("Status:          %s\n", resp.Status)

	return nil
}

func newEnvRestartCmd() *cobra.Command {
	var envProfile string
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the project environment (down + up)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Tear down if running
			if stateFile, err := readEnvState(); err == nil {
				req := env.EnvRequest{
					Provider: stateFile.Provider,
					StateDir: envStateDir(),
					PlanDir:  envStateDir(),
					Config:   make(map[string]interface{}),
					Force:    true,
				}
				if node := getWorkspaceNode(); node != nil {
					req.Workspace = node
				}

				var client env.DaemonEnvClient
				if stateFile.Provider == "native" || stateFile.Provider == "docker" || stateFile.Provider == "terraform" {
					client = daemon.NewWithAutoStart()
				}
				prov := env.ResolveProvider(stateFile.Provider, client, stateFile.Command)
				if err := prov.Down(context.Background(), req); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: teardown failed: %v\n", err)
				}

				// state.json is removed by the daemon on successful Down. The
				// client only cleans up .env.local artifacts.
				os.Remove(filepath.Join(envStateDir(), ".env.local"))
				os.Remove(filepath.Join(".", ".env.local"))
			}

			// Now start fresh — delegate to up logic
			resolved, profile, err := resolveEnvConfig(envProfile)
			if err != nil {
				return err
			}
			if resolved.Provider == "" {
				return fmt.Errorf("no provider configured for environment")
			}

			stateDir := envStateDir()
			if err := os.MkdirAll(stateDir, 0755); err != nil {
				return fmt.Errorf("failed to create state directory: %w", err)
			}

			req := env.EnvRequest{
				Provider:  resolved.Provider,
				Profile:   profile,
				StateDir:  stateDir,
				PlanDir:   stateDir,
				Config:    resolved.Config,
				ManagedBy: "user",
			}
			if node := getWorkspaceNode(); node != nil {
				req.Workspace = node
			}

			if len(resolved.DisplayEndpoints) > 0 {
				if req.Config == nil {
					req.Config = make(map[string]interface{})
				}
				list := make([]interface{}, 0, len(resolved.DisplayEndpoints))
				for _, name := range resolved.DisplayEndpoints {
					list = append(list, name)
				}
				req.Config["display_endpoints"] = list
			}

			var client env.DaemonEnvClient
			if resolved.Provider == "native" || resolved.Provider == "docker" || resolved.Provider == "terraform" {
				client = daemon.NewWithAutoStart()
			}
			prov := env.ResolveProvider(resolved.Provider, client, resolved.Command)

			if !jsonOutput {
				fmt.Printf("Restarting environment via %s...\n", resolved.Provider)
			}

			resp, err := prov.Up(context.Background(), req)
			if err != nil {
				return fmt.Errorf("environment startup failed: %w", err)
			}

			// state.json is now written by the daemon (Manager.Up). The client
			// only writes .env.local for tooling that reads it directly.
			writeEnvLocal(resp.EnvVars)
			writeLastProfile(profile)

			if jsonOutput {
				json.NewEncoder(os.Stdout).Encode(resp)
			} else {
				fmt.Printf("Environment restarted (%s)\n", resp.Status)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&envProfile, "env", "", "Override the active environment profile")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func newEnvVarsCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "vars",
		Short: "Print the environment variables from the active environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			stateFile, err := readEnvState()
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("no environment running")
				}
				return err
			}

			if jsonOutput {
				json.NewEncoder(os.Stdout).Encode(stateFile.EnvVars)
				return nil
			}

			keys := make([]string, 0, len(stateFile.EnvVars))
			for k := range stateFile.EnvVars {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			for _, k := range keys {
				fmt.Printf("%s=%s\n", k, stateFile.EnvVars[k])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

// newEnvExecCmd creates `grove exec <cmd>` which injects env vars into commands.
func newEnvExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "exec <command> [args...]",
		Short:              "Run a command with environment variables injected",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("usage: grove run <command> [args...]")
			}

			// Load env vars from .env.local
			envVars := os.Environ()
			envLocalPath := filepath.Join(".", ".env.local")
			if data, err := os.Open(envLocalPath); err == nil {
				defer data.Close()
				scanner := bufio.NewScanner(data)
				for scanner.Scan() {
					line := strings.TrimSpace(scanner.Text())
					if line == "" || strings.HasPrefix(line, "#") {
						continue
					}
					envVars = append(envVars, line)
				}
			}

			execCmd := exec.Command(args[0], args[1:]...)
			execCmd.Stdout = os.Stdout
			execCmd.Stderr = os.Stderr
			execCmd.Stdin = os.Stdin
			execCmd.Env = envVars

			return execCmd.Run()
		},
	}
	return cmd
}

func newEnvListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available environment profiles",
		Long: `List configured environment profiles. Tags next to each name indicate:

  [Config Default]    — the '[environment]' block from grove.toml
  [Sticky Default]    — the profile set via 'grove env default'
  [Active (Running)]  — the profile currently running in this worktree`,
		Example: `  grove env list
  grove env default hybrid-api    # promote 'hybrid-api' to sticky default`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadDefault()
			if err != nil {
				return err
			}

			sticky, _ := state.GetString("environment")

			// Determine currently running profile by reading local state.
			// Treat an empty Environment field as "default" for tagging.
			running := ""
			if sf, err := readEnvState(); err == nil && sf != nil {
				running = sf.Environment
				if running == "" {
					running = "default"
				}
			}

			// Print default environment
			if cfg.Environment != nil {
				provider := cfg.Environment.Provider
				if provider == "" {
					provider = "(no provider)"
				}
				fmt.Printf("  default (%s)%s\n", provider, profileTags("default", sticky, running, true))
			}

			// Print named environments sorted
			var names []string
			for k := range cfg.Environments {
				names = append(names, k)
			}
			sort.Strings(names)

			for _, name := range names {
				provider := cfg.Environments[name].Provider
				if provider == "" {
					provider = "(inherits)"
				}
				fmt.Printf("  %s (%s)%s\n", name, provider, profileTags(name, sticky, running, false))
			}

			return nil
		},
	}
}

// profileTags renders the trailing "[Config Default] [Sticky Default] [Active (Running)]"
// annotations for a profile name given the current sticky default and running profile.
func profileTags(name, sticky, running string, isConfigDefault bool) string {
	var tags []string
	if isConfigDefault {
		tags = append(tags, "[Config Default]")
	}
	if name != "" && name == sticky {
		tags = append(tags, "[Sticky Default]")
	}
	if name != "" && name == running {
		tags = append(tags, "[Active (Running)]")
	}
	if len(tags) == 0 {
		return ""
	}
	return "  " + strings.Join(tags, " ")
}

func newEnvShowCmd() *cobra.Command {
	var provenance bool
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show resolved configuration for a named environment profile",
		Long: `Show resolved configuration for a named environment profile.

With --provenance, annotate every merged key with the layer that produced
it (global, ecosystem, project-notebook, project, override, …) and list any
keys dropped by a '_delete = true' overlay.`,
		Example: `  # Plain resolved config (YAML)
  grove env show hybrid-api

  # Annotate every key with its source layer
  grove env show hybrid-api --provenance

  # Machine-readable provenance for a filter or agent
  grove env show hybrid-api --provenance --json | jq '.provenance | to_entries | .[] | select(.key | startswith("config.services"))'

  # Inspect which keys the profile overlay dropped
  grove env show hybrid-api --provenance --json | jq '.deleted'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profileName := args[0]
			if profileName == "default" {
				profileName = ""
			}

			if !provenance {
				cfg, err := config.LoadDefault()
				if err != nil {
					return err
				}
				resolved, err := config.ResolveEnvironment(cfg, profileName)
				if err != nil {
					return err
				}
				if jsonOutput {
					return json.NewEncoder(os.Stdout).Encode(resolved)
				}
				data, err := yaml.Marshal(resolved)
				if err != nil {
					return err
				}
				fmt.Print(string(data))
				return nil
			}

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			layered, err := config.LoadLayered(cwd)
			if err != nil {
				return err
			}

			resolved, prov, deleted, err := config.ResolveEnvironmentWithProvenance(layered, profileName)
			if err != nil {
				return err
			}

			if jsonOutput {
				out := struct {
					Config     *config.EnvironmentConfig `json:"config"`
					Provenance map[string]string         `json:"provenance"`
					Deleted    map[string]string         `json:"deleted,omitempty"`
				}{
					Config:     resolved,
					Provenance: prov,
					Deleted:    deleted,
				}
				return json.NewEncoder(os.Stdout).Encode(out)
			}

			annotated, err := annotateEnvYAML(resolved, prov, deleted)
			if err != nil {
				return err
			}
			fmt.Print(annotated)
			return nil
		},
	}
	cmd.Flags().BoolVar(&provenance, "provenance", false, "Annotate each key with its source layer and list '_delete'-dropped keys")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit JSON instead of YAML")
	return cmd
}

// annotateEnvYAML marshals an EnvironmentConfig to YAML and attaches the
// provenance label to each scalar leaf as a line comment. Subtrees that
// descend from the same layer get a single label on their parent key line.
// Deleted keys are emitted as a trailing comment block since they have no
// node in the output tree.
func annotateEnvYAML(envCfg *config.EnvironmentConfig, prov, deleted map[string]string) (string, error) {
	data, err := yaml.Marshal(envCfg)
	if err != nil {
		return "", err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return "", err
	}
	annotateYAMLNode(&root, "", prov)

	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return "", err
	}
	enc.Close()

	if len(deleted) > 0 {
		buf.WriteString("\n# Deleted keys (dropped via _delete = true):\n")
		keys := make([]string, 0, len(deleted))
		for k := range deleted {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			buf.WriteString(fmt.Sprintf("#   %s  (by %s)\n", k, deleted[k]))
		}
	}
	return buf.String(), nil
}

// annotateYAMLNode walks a parsed YAML tree and attaches provenance labels to
// scalar leaves. When every descendant of a mapping/sequence node shares a
// single provenance (recorded against the parent path), the label is attached
// to the key line instead of every leaf.
func annotateYAMLNode(node *yaml.Node, path string, prov map[string]string) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.DocumentNode:
		for _, c := range node.Content {
			annotateYAMLNode(c, path, prov)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valNode := node.Content[i+1]
			childPath := keyNode.Value
			if path != "" {
				childPath = path + "." + keyNode.Value
			}
			switch valNode.Kind {
			case yaml.ScalarNode:
				if src, ok := prov[childPath]; ok {
					valNode.LineComment = src
				}
			case yaml.MappingNode, yaml.SequenceNode:
				if src, ok := prov[childPath]; ok {
					keyNode.LineComment = src
				}
				annotateYAMLNode(valNode, childPath, prov)
			}
		}
	case yaml.SequenceNode:
		// Sequences are replaced wholesale by deepMergeMaps; their provenance
		// is recorded at the parent key path (handled above).
	}
}

func newEnvDefaultCmd() *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "default [name]",
		Short: "Get or set the sticky default environment for this project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if clear {
				if err := state.Delete("environment"); err != nil {
					return err
				}
				fmt.Println("Cleared default environment")
				return nil
			}

			if len(args) == 1 {
				if err := state.Set("environment", args[0]); err != nil {
					return err
				}
				fmt.Printf("Set default environment to: %s\n", args[0])
				return nil
			}

			active, _ := state.GetString("environment")
			if active == "" {
				fmt.Println("default")
			} else {
				fmt.Println(active)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "Clear the sticky default")
	return cmd
}

func newEnvCmdRunCmd() *cobra.Command {
	var envProfile string
	cmd := &cobra.Command{
		Use:   "cmd [command_name]",
		Short: "Run an environment-specific command",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Once flag parsing has succeeded, any error from here on is a
			// runtime failure (profile resolution, script exit) rather than
			// misuse — the flag-help block just drowns out the real message.
			cmd.SilenceUsage = true

			cfg, err := config.LoadDefault()
			if err != nil {
				return err
			}

			profile, note, rerr := resolveEnvCmdProfile(envProfile, cfg)
			if rerr != nil {
				return rerr
			}
			if note != "" {
				fmt.Fprint(os.Stderr, note)
			}

			resolved, err := config.ResolveEnvironment(cfg, profile)
			if err != nil {
				return err
			}

			// If no args, list available commands
			if len(args) == 0 {
				if len(resolved.Commands) == 0 {
					fmt.Println("No commands defined for this environment")
					return nil
				}

				// Sort and print commands
				var names []string
				for k := range resolved.Commands {
					names = append(names, k)
				}
				sort.Strings(names)

				fmt.Println("Available commands:")
				for _, name := range names {
					fmt.Printf("  %-15s %s\n", name, resolved.Commands[name])
				}
				return nil
			}

			cmdName := args[0]
			cmdStr, exists := resolved.Commands[cmdName]
			if !exists {
				profileDesc := "default"
				if profile != "" {
					profileDesc = profile
				}
				return fmt.Errorf("command %q not defined in environment %q", cmdName, profileDesc)
			}

			// Load .env.local if it exists and inject into the command environment
			envVars := os.Environ()
			envLocalPath := filepath.Join(".", ".env.local")
			if data, err := os.Open(envLocalPath); err == nil {
				defer data.Close()
				scanner := bufio.NewScanner(data)
				for scanner.Scan() {
					line := strings.TrimSpace(scanner.Text())
					if line == "" || strings.HasPrefix(line, "#") {
						continue
					}
					envVars = append(envVars, line)
				}
			}

			execCmd := exec.Command("sh", "-c", cmdStr)
			execCmd.Stdout = os.Stdout
			execCmd.Stderr = os.Stderr
			execCmd.Stdin = os.Stdin
			execCmd.Env = envVars

			return execCmd.Run()
		},
	}
	cmd.Flags().StringVar(&envProfile, "env", "", "Override the active environment profile")
	return cmd
}

