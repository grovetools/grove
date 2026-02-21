package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/keys"
	"github.com/spf13/cobra"
)

func newKeysMatrixCmd() *cobra.Command {
	var jsonOutput bool
	var conflictsOnly bool

	cmd := cli.NewStandardCommand("matrix", "View a matrix of all keys across TUIs")
	cmd.Long = `Display a spreadsheet-style matrix showing what each key does in each TUI.

This provides a quick overview of key usage across the Grove ecosystem,
highlighting conflicts where the same key means different things.

Use --conflicts to show only rows with semantic conflicts.
Use --json for machine-readable output.`

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output matrix in JSON format")
	cmd.Flags().BoolVar(&conflictsOnly, "conflicts", false, "Show only conflicting rows")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadDefault()
		if err != nil {
			cfg = &config.Config{}
		}

		bindings, err := keys.Aggregate(cfg)
		if err != nil {
			return err
		}

		matrix := keys.BuildMatrix(bindings)

		if jsonOutput {
			out, _ := json.MarshalIndent(matrix, "", "  ")
			fmt.Println(string(out))
			return nil
		}

		t := theme.DefaultTheme
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

		// Print Header
		header := []string{"KEY"}
		for _, tui := range matrix.TUINames {
			// Extract short name from source (e.g., "flow-status (github.com/...)" -> "flow-status")
			cleanName := strings.Split(tui, " ")[0]
			// Further shorten if needed
			if len(cleanName) > 15 {
				cleanName = cleanName[:12] + "..."
			}
			header = append(header, cleanName)
		}
		header = append(header, "STATUS")
		fmt.Fprintln(w, t.Bold.Render(strings.Join(header, "\t")))

		// Print separator
		sep := make([]string, len(header))
		for i := range sep {
			sep[i] = "─────"
		}
		fmt.Fprintln(w, t.Muted.Render(strings.Join(sep, "\t")))

		// Track statistics
		consistentCount := 0
		conflictCount := 0
		tuiSpecificCount := 0

		for _, row := range matrix.Rows {
			if conflictsOnly && row.Consistent {
				continue
			}

			rowCells := []string{t.Highlight.Render(row.Key)}
			for _, tui := range matrix.TUINames {
				val := "-"
				if action, ok := row.TUIs[tui]; ok {
					val = action
				}
				rowCells = append(rowCells, val)
			}

			var status string
			if !row.Consistent {
				status = t.Warning.Render("⚠ CONFLICT")
				conflictCount++
			} else if len(row.TUIs) == 1 {
				status = t.Muted.Render("TUI SPECIFIC")
				tuiSpecificCount++
			} else {
				status = t.Success.Render("✓ CONSISTENT")
				consistentCount++
			}
			rowCells = append(rowCells, status)

			fmt.Fprintln(w, strings.Join(rowCells, "\t"))
		}

		w.Flush()

		// Print summary
		fmt.Println()
		fmt.Printf("%s  Consistent: %d  │  Conflicts: %d  │  TUI-specific: %d\n",
			t.Muted.Render("Summary:"),
			consistentCount,
			conflictCount,
			tuiSpecificCount)

		return nil
	}

	return cmd
}
