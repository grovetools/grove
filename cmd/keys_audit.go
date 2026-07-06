package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/theme"
	"github.com/spf13/cobra"

	"github.com/grovetools/grove/pkg/keys"
)

// auditReport is the machine-readable shape emitted by `grove keys audit --json`.
type auditReport struct {
	StructuralErrors   []string                    `json:"structural_errors"`
	ReservedViolations []keys.ReservedKeyViolation `json:"reserved_key_violations"`
	Inconsistent       []string                    `json:"inconsistent_actions"`
	TUIConflicts       []auditConflict             `json:"tui_conflicts"`
	SemanticConflicts  []keys.SemanticConflict     `json:"semantic_conflicts"`
	Strict             bool                        `json:"strict"`
	ErrorCount         int                         `json:"error_count"`
	WarningCount       int                         `json:"warning_count"`
}

type auditConflict struct {
	TUI     string   `json:"tui"`
	Key     string   `json:"key"`
	Actions []string `json:"actions"`
}

// newKeysAuditCmd creates `grove keys audit`: the enforcement gate over the
// generated registry. It promotes the analysis from a report to an exit-code
// check.
//
// Severity model:
//   - ERRORS (always, exit non-zero): structural problems from
//     keys.ValidateRegistry (empty fields, malformed or duplicate ConfigKeys).
//     These are objective invariants the generator must uphold.
//   - WARNINGS (advisory, never gate by default): reserved-key violations,
//     canonical-consistency failures, intra-TUI key conflicts, and semantic
//     conflicts. The ecosystem carries a large population of intentional and
//     context-scoped (page/pane) key reuse that the Phase B–E hotkey review is
//     still working through; gating on it here would block indefinitely.
//   - --strict promotes reserved-key violations, consistency failures, and
//     intra-TUI conflicts to errors (semantic conflicts stay advisory), so a
//     future, cleaned-up ecosystem can enforce them.
//
// Intentional deviations recorded in pkg/keys/deviations.go are already
// suppressed by keys.Analyze, so they never appear here — deliberate choices
// stop being noise.
func newKeysAuditCmd() *cobra.Command {
	var jsonOutput bool
	var strict bool

	cmd := cli.NewStandardCommand("audit", "Audit the keybinding registry and fail on structural problems")
	cmd.Long = `Audit the generated TUI keybinding registry.

Always errors on structural problems (malformed or duplicate ConfigKeys, empty
fields). Reserved-key violations, consistency failures, and key conflicts are
reported as advisory warnings; pass --strict to promote them to errors.

Intentional deviations (pkg/keys/deviations.go) are suppressed, so only
un-sanctioned issues surface. Use --json for machine-readable output.`

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output report in JSON format")
	cmd.Flags().BoolVar(&strict, "strict", false, "Promote reserved-key violations, consistency failures, and conflicts to errors")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadDefault()
		if err != nil {
			cfg = &config.Config{}
		}

		structural := keys.ValidateRegistry()

		bindings, err := keys.Aggregate(cfg)
		if err != nil {
			return err
		}
		report := keys.Analyze(bindings)

		// Inconsistent canonical actions (post-allowlist).
		var inconsistent []string
		for action, res := range report.Consistency {
			if !res.Consistent {
				inconsistent = append(inconsistent, action)
			}
		}
		sort.Strings(inconsistent)

		// Intra-TUI key conflicts (same key, different action within one TUI).
		var tuiConflicts []auditConflict
		for _, c := range keys.DetectConflicts(bindings) {
			if c.Domain != keys.DomainTUI || c.TUI == "" {
				continue
			}
			actions := make([]string, 0, len(c.Bindings))
			for _, b := range c.Bindings {
				actions = append(actions, b.Action)
			}
			sort.Strings(actions)
			tuiConflicts = append(tuiConflicts, auditConflict{TUI: c.TUI, Key: c.Key, Actions: actions})
		}

		errorCount := len(structural)
		warningCount := len(report.SemanticConflicts)
		if strict {
			errorCount += len(report.ReservedKeyViolations) + len(inconsistent) + len(tuiConflicts)
		} else {
			warningCount += len(report.ReservedKeyViolations) + len(inconsistent) + len(tuiConflicts)
		}

		if jsonOutput {
			out, _ := json.MarshalIndent(auditReport{
				StructuralErrors:   structural,
				ReservedViolations: report.ReservedKeyViolations,
				Inconsistent:       inconsistent,
				TUIConflicts:       tuiConflicts,
				SemanticConflicts:  report.SemanticConflicts,
				Strict:             strict,
				ErrorCount:         errorCount,
				WarningCount:       warningCount,
			}, "", "  ")
			fmt.Println(string(out))
			if errorCount > 0 {
				return fmt.Errorf("keys audit failed with %d error(s)", errorCount)
			}
			return nil
		}

		t := theme.DefaultTheme
		errLabel := t.Error.Render
		warnLabel := t.Warning.Render

		// Structural (always errors)
		fmt.Println(t.Header.Render(" REGISTRY STRUCTURE "))
		fmt.Println(t.Muted.Render(strings.Repeat("─", 50)))
		if len(structural) == 0 {
			fmt.Println(t.Success.Render("✓ registry is structurally valid"))
		} else {
			for _, p := range structural {
				fmt.Printf("  %s %s\n", errLabel("✗"), p)
			}
		}

		sev := func(promoted bool) string {
			if promoted {
				return errLabel("ERROR")
			}
			return warnLabel("warn")
		}

		// Reserved-key violations
		if len(report.ReservedKeyViolations) > 0 {
			fmt.Println("\n" + t.Header.Render(" RESERVED KEY VIOLATIONS "))
			fmt.Println(t.Muted.Render(strings.Repeat("─", 50)))
			for _, v := range report.ReservedKeyViolations {
				fmt.Printf("  [%s] %s in %s: expected %q, got %q\n",
					sev(strict), t.Highlight.Render(v.Key), t.Bold.Render(v.TUI), v.ExpectedAction, v.ActualAction)
			}
		}

		// Consistency
		if len(inconsistent) > 0 {
			fmt.Println("\n" + t.Header.Render(" CANONICAL CONSISTENCY "))
			fmt.Println(t.Muted.Render(strings.Repeat("─", 50)))
			for _, a := range inconsistent {
				fmt.Printf("  [%s] %q is not applied consistently across TUIs\n", sev(strict), a)
			}
		}

		// Intra-TUI conflicts
		if len(tuiConflicts) > 0 {
			fmt.Println("\n" + t.Header.Render(" INTRA-TUI KEY CONFLICTS "))
			fmt.Println(t.Muted.Render(strings.Repeat("─", 50)))
			for _, c := range tuiConflicts {
				fmt.Printf("  [%s] %s in %s: %s\n",
					sev(strict), t.Highlight.Render(c.Key), t.Bold.Render(c.TUI), strings.Join(c.Actions, " / "))
			}
		}

		// Semantic conflicts (always advisory)
		if len(report.SemanticConflicts) > 0 {
			fmt.Println("\n" + t.Header.Render(" CROSS-TUI SEMANTIC CONFLICTS "))
			fmt.Println(t.Muted.Render(strings.Repeat("─", 50)))
			fmt.Printf("  %s %d key(s) carry different meanings across TUIs (advisory)\n",
				warnLabel("warn"), len(report.SemanticConflicts))
		}

		fmt.Println("\n" + t.Muted.Render(strings.Repeat("─", 50)))
		fmt.Printf("%d error(s), %d warning(s)", errorCount, warningCount)
		if !strict && warningCount > 0 {
			fmt.Print(t.Muted.Render("  (run with --strict to gate on warnings)"))
		}
		fmt.Println()

		if errorCount > 0 {
			return fmt.Errorf("keys audit failed with %d error(s)", errorCount)
		}
		return nil
	}

	return cmd
}
