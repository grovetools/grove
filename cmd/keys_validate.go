package cmd

import (
	"fmt"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/keys"
	"github.com/spf13/cobra"
)

// newKeysValidateCmd creates the 'grove keys validate' command.
func newKeysValidateCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("validate", "Validate user keybinding config against the registry")

	cmd.Long = `Validate user keybinding overrides against the TUI registry.

Checks that all configured action names in [tui.keybindings.overrides] exist
in the keybinding registry. Reports typos with suggestions for the closest match.

Example:
  grove keys validate

  ✗ [tui.keybindings.overrides.flow.status]
    Unknown action 'runn_job'. Did you mean 'run_job'?`

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runKeysValidate()
	}

	return cmd
}

func runKeysValidate() error {
	cfg, err := config.LoadDefault()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if cfg == nil || cfg.TUI == nil || cfg.TUI.Keybindings == nil {
		fmt.Println("No TUI keybinding overrides found in configuration.")
		return nil
	}

	tuiOverrides := cfg.TUI.Keybindings.GetTUIOverrides()
	if len(tuiOverrides) == 0 {
		fmt.Println("No TUI keybinding overrides found in configuration.")
		return nil
	}

	t := theme.DefaultTheme
	errorsFound := 0

	fmt.Println(t.Header.Render(theme.IconGear + " Validating TUI keybinding overrides..."))
	fmt.Println()

	for pkgName, pkgOverrides := range tuiOverrides {
		for tuiName, overrides := range pkgOverrides {
			registryName := pkgName + "-" + tuiName
			tui := keys.GetTUIByName(registryName)

			if tui == nil {
				fmt.Printf("%s [tui.keybindings.%s.%s]\n", t.Error.Render(theme.IconError), pkgName, tuiName)
				fmt.Printf("  Unknown TUI identifier. Check package and TUI name.\n\n")
				errorsFound++
				continue
			}

			// Collect valid config keys for this TUI
			validKeys := make(map[string]bool)
			var validKeyList []string
			for _, sec := range tui.Sections {
				for _, b := range sec.Bindings {
					if b.ConfigKey != "" {
						validKeys[b.ConfigKey] = true
						validKeyList = append(validKeyList, b.ConfigKey)
					}
				}
			}

			// Validate user's defined overrides
			hasErrors := false
			for configuredKey := range overrides {
				if !validKeys[configuredKey] {
					if !hasErrors {
						fmt.Printf("%s [tui.keybindings.%s.%s]\n", t.Error.Render(theme.IconError), pkgName, tuiName)
						hasErrors = true
					}
					suggestion := findClosestMatch(configuredKey, validKeyList)
					msg := fmt.Sprintf("Unknown action '%s'.", configuredKey)
					if suggestion != "" {
						msg += fmt.Sprintf(" Did you mean '%s'?", suggestion)
					}
					fmt.Printf("  %s\n", msg)
					errorsFound++
				}
			}
			if hasErrors {
				fmt.Println()
			}
		}
	}

	if errorsFound == 0 {
		fmt.Println(t.Success.Render(theme.IconSuccess + " All configured keybindings are valid."))
	} else {
		fmt.Printf("\n%s validation error(s) found.\n", t.Bold.Render(fmt.Sprintf("%d", errorsFound)))
	}

	return nil
}

// findClosestMatch finds the closest match to target in the validKeys list using Levenshtein distance.
func findClosestMatch(target string, validKeys []string) string {
	bestDistance := 999
	bestMatch := ""

	for _, key := range validKeys {
		dist := levenshtein(target, key)
		if dist < bestDistance && dist <= 3 { // Threshold for "close enough"
			bestDistance = dist
			bestMatch = key
		}
	}
	return bestMatch
}

// levenshtein calculates the Levenshtein distance between two strings.
func levenshtein(s1, s2 string) int {
	r1, r2 := []rune(s1), []rune(s2)
	n, m := len(r1), len(r2)
	if n == 0 {
		return m
	}
	if m == 0 {
		return n
	}

	d := make([][]int, n+1)
	for i := range d {
		d[i] = make([]int, m+1)
		d[i][0] = i
	}
	for j := 0; j <= m; j++ {
		d[0][j] = j
	}

	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			cost := 1
			if r1[i-1] == r2[j-1] {
				cost = 0
			}
			minCost := d[i-1][j] + 1
			if d[i][j-1]+1 < minCost {
				minCost = d[i][j-1] + 1
			}
			if d[i-1][j-1]+cost < minCost {
				minCost = d[i-1][j-1] + cost
			}
			d[i][j] = minCost
		}
	}
	return d[n][m]
}
