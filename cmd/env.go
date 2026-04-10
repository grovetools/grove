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

// writeEnvState writes the state file to .grove/env/state.json.
func writeEnvState(stateFile *env.EnvStateFile) error {
	if err := os.MkdirAll(envStateDir(), 0755); err != nil {
		return fmt.Errorf("failed to create .grove/env directory: %w", err)
	}
	data, err := json.MarshalIndent(stateFile, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(envStatePath(), data, 0644)
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
func getWorkspaceNode() *workspace.WorkspaceNode {
	cwd, _ := os.Getwd()
	node, err := workspace.GetProjectByPath(cwd)
	if err != nil {
		return nil
	}
	return node
}

func newEnvUpCmd() *cobra.Command {
	var envProfile string
	var jsonOutput bool

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
				StateDir:  stateDir,
				PlanDir:   stateDir, // backward compat
				Config:    resolved.Config,
				ManagedBy: "user",
			}

			// Attach workspace node
			if node := getWorkspaceNode(); node != nil {
				req.Workspace = node
			}

			// Phase 4: Prepare shared_backend_config if shared_env is configured
			if sharedEnv, ok := resolved.Config["shared_env"].(string); ok && sharedEnv != "" {
				sharedRef, _ := resolved.Config["shared_ref"].(string)
				if sharedRef == "" {
					sharedRef = "main"
				}
				stateBackend, _ := resolved.Config["state_backend"].(string)
				stateBucket, _ := resolved.Config["state_bucket"].(string)

				if stateBackend == "gcs" && stateBucket != "" {
					// Determine ecosystem name from workspace
					ecosystem := ""
					if req.Workspace != nil && req.Workspace.ParentEcosystemPath != "" {
						ecosystem = filepath.Base(req.Workspace.ParentEcosystemPath)
					}
					if ecosystem == "" && req.Workspace != nil && req.Workspace.RootEcosystemPath != "" {
						ecosystem = filepath.Base(req.Workspace.RootEcosystemPath)
					}
					prefix := sharedEnv + "/" + sharedRef
					if ecosystem != "" {
						prefix = ecosystem + "/" + sharedEnv + "/" + sharedRef
					}

					req.Config["shared_backend_config"] = map[string]interface{}{
						"state_backend": stateBackend,
						"state_bucket":  stateBucket,
						"state_prefix":  prefix,
					}
				}
			}

			// Resolve provider
			var client env.DaemonEnvClient
			if resolved.Provider == "native" || resolved.Provider == "docker" || resolved.Provider == "terraform" {
				client = daemon.New()
			}
			prov := env.ResolveProvider(resolved.Provider, client, resolved.Command)

			if !jsonOutput {
				fmt.Printf("Starting environment via %s...\n", resolved.Provider)
			}

			resp, err := prov.Up(context.Background(), req)
			if err != nil {
				return fmt.Errorf("environment startup failed: %w", err)
			}

			// Build service states from response
			var services []env.ServiceState
			if resp.State != nil {
				for k, v := range resp.State {
					services = append(services, env.ServiceState{Name: k, Status: v})
				}
			}

			// Collect port info from env vars (convention: FOO_PORT=1234)
			ports := make(map[string]int)
			for k, v := range resp.EnvVars {
				if strings.HasSuffix(k, "_PORT") {
					var port int
					if _, err := fmt.Sscanf(v, "%d", &port); err == nil {
						ports[k] = port
					}
				}
			}

			// Write state
			stateFile := &env.EnvStateFile{
				Provider:     resolved.Provider,
				Command:      resolved.Command,
				Environment:  profile,
				ManagedBy:    "user",
				Ports:        ports,
				Services:     services,
				EnvVars:      resp.EnvVars,
				CleanupPaths: resp.CleanupPaths,
				Volumes:      resp.Volumes,
				State:        resp.State,
			}
			if err := writeEnvState(stateFile); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not write state: %v\n", err)
			}

			// Write .env.local
			if err := writeEnvLocal(resp.EnvVars); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not write .env.local: %v\n", err)
			}

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
	return cmd
}

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
				client = daemon.New()
			}
			prov := env.ResolveProvider(stateFile.Provider, client, stateFile.Command)

			if !jsonOutput {
				fmt.Printf("Stopping %s environment...\n", stateFile.Provider)
			}

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

			// Clean up state files
			os.Remove(envStatePath())
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
		Use:   "status",
		Short: "Show the current environment status",
		RunE: func(cmd *cobra.Command, args []string) error {
			stateFile, err := readEnvState()
			if err != nil {
				if os.IsNotExist(err) {
					if jsonOutput {
						json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "stopped"})
					} else {
						fmt.Println("No environment running")
					}
					return nil
				}
				return err
			}

			if jsonOutput {
				json.NewEncoder(os.Stdout).Encode(stateFile)
				return nil
			}

			fmt.Printf("Provider:    %s\n", stateFile.Provider)
			if stateFile.Environment != "" {
				fmt.Printf("Profile:     %s\n", stateFile.Environment)
			}
			fmt.Printf("Managed by:  %s\n", stateFile.ManagedBy)

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
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
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
					client = daemon.New()
				}
				prov := env.ResolveProvider(stateFile.Provider, client, stateFile.Command)
				if err := prov.Down(context.Background(), req); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: teardown failed: %v\n", err)
				}

				// Clean up state
				os.Remove(envStatePath())
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
				StateDir:  stateDir,
				PlanDir:   stateDir,
				Config:    resolved.Config,
				ManagedBy: "user",
			}
			if node := getWorkspaceNode(); node != nil {
				req.Workspace = node
			}

			var client env.DaemonEnvClient
			if resolved.Provider == "native" || resolved.Provider == "docker" || resolved.Provider == "terraform" {
				client = daemon.New()
			}
			prov := env.ResolveProvider(resolved.Provider, client, resolved.Command)

			if !jsonOutput {
				fmt.Printf("Restarting environment via %s...\n", resolved.Provider)
			}

			resp, err := prov.Up(context.Background(), req)
			if err != nil {
				return fmt.Errorf("environment startup failed: %w", err)
			}

			// Write state and env vars
			sf := &env.EnvStateFile{
				Provider:     resolved.Provider,
				Command:      resolved.Command,
				Environment:  profile,
				ManagedBy:    "user",
				EnvVars:      resp.EnvVars,
				CleanupPaths: resp.CleanupPaths,
				Volumes:      resp.Volumes,
				State:        resp.State,
			}
			writeEnvState(sf)
			writeEnvLocal(resp.EnvVars)

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
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadDefault()
			if err != nil {
				return err
			}

			active, _ := state.GetString("environment")

			// Print default environment
			if cfg.Environment != nil {
				marker := "  "
				if active == "" {
					marker = "* "
				}
				provider := cfg.Environment.Provider
				if provider == "" {
					provider = "(no provider)"
				}
				fmt.Printf("%sdefault (%s)\n", marker, provider)
			}

			// Print named environments sorted
			var names []string
			for k := range cfg.Environments {
				names = append(names, k)
			}
			sort.Strings(names)

			for _, name := range names {
				marker := "  "
				if name == active {
					marker = "* "
				}
				provider := cfg.Environments[name].Provider
				if provider == "" {
					provider = "(inherits)"
				}
				fmt.Printf("%s%s (%s)\n", marker, name, provider)
			}

			return nil
		},
	}
}

func newEnvShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show resolved configuration for a named environment profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadDefault()
			if err != nil {
				return err
			}

			profileName := args[0]
			if profileName == "default" {
				profileName = ""
			}

			resolved, err := config.ResolveEnvironment(cfg, profileName)
			if err != nil {
				return err
			}

			data, err := yaml.Marshal(resolved)
			if err != nil {
				return err
			}
			fmt.Print(string(data))
			return nil
		},
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
			cfg, err := config.LoadDefault()
			if err != nil {
				return err
			}

			// Determine active profile
			profile := envProfile
			if profile == "" {
				profile, _ = state.GetString("environment")
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

