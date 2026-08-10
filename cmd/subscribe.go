package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
)

func init() {
	rootCmd.AddCommand(newSubscribeCmd())
}

// `grove subscribe` is deliberately the thinnest verb in the set: it writes a
// specific [roots.<name>] table and stops.
//
// The split matters. A SUBSCRIPTION is a standing statement of intent ("this
// machine wants grovetools at ~/code/grovetools") and is dotfiles-portable;
// MATERIALIZATION is the act of making the disk agree with it, and is not.
// Keeping them apart is what makes "declared but missing" a computable,
// actionable state rather than an error — and what lets a restored dotfiles
// repo declare five ecosystems on a brand-new laptop that has none of them yet.
func newSubscribeCmd() *cobra.Command {
	var (
		path     string
		notebook string
		disabled bool
		repos    []string
		exclude  []string
	)
	cmd := &cobra.Command{
		Use:   "subscribe <ecosystem>",
		Short: "Declare that this machine wants an ecosystem",
		Long: `Record an ecosystem subscription in ~/.config/grove/roots.toml.

This writes intent, not code. Nothing is cloned; the recorded root simply
becomes visible to every surface
that reconciles intent against disk:

  grove machine status              shows it as present or declared-missing
  grove ecosystem materialize <n>   makes the disk agree with it
  grove machines                    publishes it in this machine's registry note

--path defaults to an existing subscription's path if there is one, otherwise
beside the ecosystems this machine already has, otherwise ~/code/<name>.

The edit is surgical: only this ecosystem's table is written, and every other
byte of roots.toml — comments, ordering, and unrelated root tables — survives
unchanged.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSubscribeWithFilters(cmd.OutOrStdout(), args[0], path, notebook, disabled, repos, exclude)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "Where this machine keeps the ecosystem (default: alongside the others, else ~/code/<name>)")
	cmd.Flags().StringVar(&notebook, "notebook", "", "Override the ecosystem card's default notebook binding on this machine")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "Record the subscription but do not scan it")
	cmd.Flags().StringSliceVar(&repos, "repo", nil, "Materialize only this member repository (repeatable)")
	cmd.Flags().StringSliceVar(&exclude, "exclude-repo", nil, "Omit this member repository (repeatable; cannot be combined with --repo)")
	return cmd
}

func runSubscribe(out io.Writer, name, path, notebook string, disabled bool) error {
	return runSubscribeWithFilters(out, name, path, notebook, disabled, nil, nil)
}

func runSubscribeWithFilters(out io.Writer, name, path, notebook string, disabled bool, repos, exclude []string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("an ecosystem name is required")
	}

	resolved := strings.TrimSpace(path)
	if resolved == "" {
		resolved = defaultEcosystemPath(name)
	}
	resolved = expandUserPath(resolved)
	if abs, err := filepath.Abs(resolved); err == nil {
		resolved = abs
	}

	entry := coderoot.Root{
		Path: resolved, Notebook: strings.TrimSpace(notebook),
		Repos: cleanRepoFilter(repos), Exclude: cleanRepoFilter(exclude),
	}
	if disabled {
		off := false
		entry.Enabled = &off
	}
	if len(entry.Repos) > 0 && len(entry.Exclude) > 0 {
		return fmt.Errorf("--repo and --exclude-repo cannot be combined")
	}

	cfgPath, changed, err := writeEcosystemSubscription(name, entry)
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintf(out, "✓ subscribed to %q at %s (%s)\n", name, resolved, cfgPath)
	} else {
		fmt.Fprintf(out, "· %q is already declared exactly this way in %s\n", name, cfgPath)
	}

	if info, statErr := os.Stat(resolved); statErr == nil && info.IsDir() {
		if manifest := config.FindEcosystemManifest(resolved); manifest != "" {
			fmt.Fprintf(out, "· present on disk (%s)\n", manifest)
			return nil
		}
		fmt.Fprintf(out, "! %s exists but carries no grove manifest — `grove ecosystem adopt` can give it one.\n", resolved)
		return nil
	}
	fmt.Fprintf(out, "\nDeclared but not present. Materialize it with:\n  grove ecosystem materialize %s\n", name)
	return nil
}

func cleanRepoFilter(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
