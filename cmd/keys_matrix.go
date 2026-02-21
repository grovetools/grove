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
		reservedOKCount := 0
		reservedViolationCount := 0
		freeCount := 0
		consistentCount := 0
		conflictCount := 0

		for _, row := range matrix.Rows {
			// Determine key status
			expectedAction, isReserved := keys.ReservedKeys[row.Key]
			isFree := keys.IsFreeKey(row.Key)

			var status string
			var skipRow bool

			if isReserved {
				// Check if all usages match the expected action
				allMatch := true
				for _, action := range row.TUIs {
					normAction := keys.NormalizeAction(action)
					normExpected := keys.NormalizeAction(expectedAction)
					if normAction != normExpected {
						allMatch = false
						break
					}
				}
				if allMatch {
					status = t.Success.Render("✓ RESERVED")
					reservedOKCount++
					if conflictsOnly {
						skipRow = true
					}
				} else {
					status = t.Error.Render("⚠ RESERVED VIOLATION")
					reservedViolationCount++
				}
			} else if isFree {
				if !row.Consistent && len(row.TUIs) > 1 {
					// Free key with different meanings - that's OK, just note it
					status = t.Muted.Render("FREE (varies)")
					freeCount++
					if conflictsOnly {
						skipRow = true
					}
				} else if len(row.TUIs) == 1 {
					status = t.Muted.Render("FREE")
					freeCount++
					if conflictsOnly {
						skipRow = true
					}
				} else {
					status = t.Success.Render("✓ FREE")
					freeCount++
					if conflictsOnly {
						skipRow = true
					}
				}
			} else {
				// Not reserved, not in free list - check consistency
				if !row.Consistent {
					status = t.Warning.Render("⚠ CONFLICT")
					conflictCount++
				} else if len(row.TUIs) == 1 {
					status = t.Muted.Render("TUI SPECIFIC")
					freeCount++
					if conflictsOnly {
						skipRow = true
					}
				} else {
					status = t.Success.Render("✓ CONSISTENT")
					consistentCount++
					if conflictsOnly {
						skipRow = true
					}
				}
			}

			if skipRow {
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
			rowCells = append(rowCells, status)

			fmt.Fprintln(w, strings.Join(rowCells, "\t"))
		}

		w.Flush()

		// Print summary
		fmt.Println()
		fmt.Printf("%s  Reserved OK: %d  │  Reserved Violations: %d  │  Free: %d  │  Conflicts: %d\n",
			t.Muted.Render("Summary:"),
			reservedOKCount,
			reservedViolationCount,
			freeCount,
			conflictCount)

		return nil
	}

	return cmd
}
