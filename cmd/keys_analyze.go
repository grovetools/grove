package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/keys"
	"github.com/spf13/cobra"
)

func newKeysAnalyzeCmd() *cobra.Command {
	var jsonOutput bool

	cmd := cli.NewStandardCommand("analyze", "Analyze keybindings for semantic conflicts and consistency")
	cmd.Long = `Analyze keybindings across all Grove TUIs for consistency issues.

This command checks:
- Canonical action consistency: Do common actions (archive, select all, etc.) use the same keys?
- Semantic conflicts: Does the same key mean different things in different TUIs?

Use --json for machine-readable output suitable for tooling.`

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output report in JSON format")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadDefault()
		if err != nil {
			cfg = &config.Config{}
		}

		bindings, err := keys.Aggregate(cfg)
		if err != nil {
			return err
		}

		report := keys.Analyze(bindings)

		if jsonOutput {
			out, _ := json.MarshalIndent(report, "", "  ")
			fmt.Println(string(out))
			return nil
		}

		t := theme.DefaultTheme

		fmt.Println(t.Header.Render(" CANONICAL ACTION CONSISTENCY "))
		fmt.Println(t.Muted.Render(strings.Repeat("─", 50)))

		for action, res := range report.Consistency {
			if res.Consistent {
				fmt.Printf("%s %s: All TUIs use [%s]\n",
					t.Success.Render("✓"),
					t.Bold.Render(action),
					strings.Join(res.CanonicalKeys, ", "))
			} else {
				fmt.Printf("\n%s %s:\n",
					t.Error.Render("✗"),
					t.Bold.Render(action))
				for tui, ks := range res.TUIs {
					warning := ""
					match := false
					for _, k := range ks {
						for _, ck := range res.CanonicalKeys {
							if k == ck {
								match = true
								break
							}
						}
						if match {
							break
						}
					}
					if !match {
						warning = t.Warning.Render("  ← DIFFERS")
					}
					fmt.Printf("    %-25s [%s]%s\n", tui, strings.Join(ks, ", "), warning)
				}
			}
		}

		if len(report.SemanticConflicts) > 0 {
			fmt.Println("\n" + t.Header.Render(" KEY SEMANTIC ANALYSIS "))
			fmt.Println(t.Muted.Render(strings.Repeat("─", 50)))
			for _, sc := range report.SemanticConflicts {
				fmt.Printf("Key [%s] has different meanings:\n", t.Highlight.Render(sc.Key))
				for tui, meaning := range sc.Meanings {
					fmt.Printf("    %-25s %q\n", tui, meaning)
				}
				fmt.Println()
			}
		} else {
			fmt.Println("\n" + t.Success.Render("✓ No semantic conflicts detected"))
		}

		return nil
	}

	return cmd
}
