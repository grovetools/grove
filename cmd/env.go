package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/state"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func init() {
	rootCmd.AddCommand(newEnvCmd())
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
