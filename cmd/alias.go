package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/reconciler"
	"github.com/mattsolo1/grove-meta/pkg/sdk"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newAliasCmd())
}

func newAliasCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("alias", "Manage custom aliases for Grove tools")
	cmd.Long = `Manage custom aliases for Grove tools to resolve PATH conflicts or for personal preference.

When an alias is set, the tool's binary/symlink in ~/.grove/bin will be renamed.
This allows you to customize how you call Grove tools from your shell.`

	cmd.Example = `  # Set a new alias for grove-context
  grove alias set grove-context ctx

  # List all custom and default aliases
  grove alias

  # Remove a custom alias to revert to the default
  grove alias unset grove-context`

	cmd.RunE = runAliasList

	// Add subcommands for set and unset
	cmd.AddCommand(newAliasSetCmd())
	cmd.AddCommand(newAliasUnsetCmd())

	return cmd
}

func newAliasSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <tool> <new-alias>",
		Short: "Set a custom alias for a tool",
		Args:  cobra.ExactArgs(2),
		RunE:  runAliasSet,
	}
	return cmd
}

func newAliasUnsetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unset <tool>",
		Short: "Remove a custom alias for a tool",
		Args:  cobra.ExactArgs(1),
		RunE:  runAliasUnset,
	}
	return cmd
}

func runAliasList(cmd *cobra.Command, args []string) error {
	allTools := sdk.GetAllTools() // This gets RepoNames
	sort.Strings(allTools)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "REPOSITORY\tEFFECTIVE ALIAS\tDEFAULT ALIAS")
	fmt.Fprintln(w, "----------\t---------------\t-------------")

	for _, repoName := range allTools {
		_, info, effectiveAlias, found := sdk.FindTool(repoName)
		if !found {
			continue
		}

		customMarker := ""
		if effectiveAlias != info.Alias {
			customMarker = " (custom)"
		}

		fmt.Fprintf(w, "%s\t%s%s\t%s\n", repoName, effectiveAlias, customMarker, info.Alias)
	}

	return w.Flush()
}

func runAliasSet(cmd *cobra.Command, args []string) error {
	toolIdentifier := args[0]
	newAlias := args[1]

	repoName, _, oldAlias, found := sdk.FindTool(toolIdentifier)
	if !found {
		return fmt.Errorf("tool '%s' not found", toolIdentifier)
	}

	groveHome := filepath.Join(os.Getenv("HOME"), ".grove")
	config, _ := sdk.LoadAliases(groveHome)
	if config.Aliases == nil {
		config.Aliases = make(map[string]string)
	}
	config.Aliases[repoName] = newAlias

	if err := sdk.SaveAliases(groveHome, config); err != nil {
		return fmt.Errorf("failed to save aliases: %w", err)
	}

	// Remove old symlink
	oldSymlink := filepath.Join(groveHome, "bin", oldAlias)
	os.Remove(oldSymlink)

	// Reconcile to create new symlink
	tv, _ := sdk.LoadToolVersions(groveHome)
	r, _ := reconciler.NewWithToolVersions(tv)
	if err := r.Reconcile(repoName); err != nil {
		return fmt.Errorf("failed to update symlink: %w", err)
	}

	fmt.Printf("✅ Alias for '%s' set to '%s'. You can now use '%s' to run the tool.\n", repoName, newAlias, newAlias)
	return nil
}

func runAliasUnset(cmd *cobra.Command, args []string) error {
	toolIdentifier := args[0]
	repoName, info, oldAlias, found := sdk.FindTool(toolIdentifier)
	if !found {
		return fmt.Errorf("tool '%s' not found", toolIdentifier)
	}

	groveHome := filepath.Join(os.Getenv("HOME"), ".grove")
	config, _ := sdk.LoadAliases(groveHome)

	if config.Aliases == nil || config.Aliases[repoName] == "" {
		fmt.Printf("No custom alias set for '%s'. It is already using the default.\n", repoName)
		return nil
	}

	delete(config.Aliases, repoName)

	if err := sdk.SaveAliases(groveHome, config); err != nil {
		return fmt.Errorf("failed to save aliases: %w", err)
	}

	// Remove old custom symlink
	oldSymlink := filepath.Join(groveHome, "bin", oldAlias)
	os.Remove(oldSymlink)

	// Reconcile to create default symlink
	tv, _ := sdk.LoadToolVersions(groveHome)
	r, _ := reconciler.NewWithToolVersions(tv)
	if err := r.Reconcile(repoName); err != nil {
		return fmt.Errorf("failed to update symlink: %w", err)
	}

	fmt.Printf("✅ Custom alias for '%s' removed. It now uses the default alias '%s'.\n", repoName, info.Alias)
	return nil
}