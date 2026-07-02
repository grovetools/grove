package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/grovetools/core/pkg/doctor"
	_ "github.com/grovetools/core/pkg/doctor/checks" // register built-in checks
	"github.com/grovetools/core/tui/theme"
	"github.com/spf13/cobra"

	_ "github.com/grovetools/grove/pkg/doctorchecks" // register grove-specific checks
)

func init() {
	rootCmd.AddCommand(newDoctorCmd())
}

var (
	doctorFix     bool
	doctorCheckID string
	doctorJSON    bool
	doctorVerbose bool
)

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose grove environment health and optionally apply safe fixes",
		Long: `grove doctor runs a suite of environment diagnostics (stale daemon binary,
orphan sockets, GROVE_SCOPE vs cwd mismatch, etc.) and reports their status.

Use --fix to apply safe auto-fixes, --check <id> to run a single diagnostic,
and --json for machine-readable output.`,
		RunE: runDoctor,
	}
	cmd.Flags().BoolVar(&doctorFix, "fix", false, "apply safe auto-fixes for failing checks")
	cmd.Flags().StringVar(&doctorCheckID, "check", "", "run only the check with this ID")
	cmd.Flags().BoolVar(&doctorJSON, "json", false, "emit JSON output")
	cmd.Flags().BoolVarP(&doctorVerbose, "verbose", "v", false, "verbose diagnostics output")
	return cmd
}

func runDoctor(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	opts := doctor.RunOptions{Verbose: doctorVerbose}

	var results []doctor.CheckResult
	if doctorCheckID != "" {
		var res doctor.CheckResult
		var ok bool
		if doctorFix {
			res, ok = doctor.RunOneWithFix(ctx, doctorCheckID, opts)
		} else {
			res, ok = doctor.RunOne(ctx, doctorCheckID, opts)
		}
		if !ok {
			return fmt.Errorf("no check with id %q", doctorCheckID)
		}
		results = []doctor.CheckResult{res}
	} else if doctorFix {
		results = doctor.RunAllWithFix(ctx, opts)
	} else {
		results = doctor.RunAll(ctx, opts)
	}

	if doctorJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(toDoctorJSON(results)); err != nil {
			return err
		}
	} else {
		renderDoctorResults(cmd.OutOrStdout(), results)
	}

	if hasFailure(results) {
		os.Exit(1)
	}
	return nil
}

// doctorJSONResult is the machine-readable post-install contract for
// `grove doctor --json`: [{check, status: pass|warn|fail, detail}].
type doctorJSONResult struct {
	Check      string `json:"check"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
	Resolution string `json:"resolution,omitempty"`
	Error      string `json:"error,omitempty"`
	FixApplied bool   `json:"fix_applied,omitempty"`
}

func toDoctorJSON(results []doctor.CheckResult) []doctorJSONResult {
	out := make([]doctorJSONResult, 0, len(results))
	for _, r := range results {
		status := string(r.Status)
		if r.Status == doctor.StatusOK {
			status = "pass"
		}
		out = append(out, doctorJSONResult{
			Check:      r.ID,
			Status:     status,
			Detail:     r.Message,
			Resolution: r.Resolution,
			Error:      r.Error,
			FixApplied: r.FixApplied,
		})
	}
	return out
}

func hasFailure(results []doctor.CheckResult) bool {
	for _, r := range results {
		if r.Status == doctor.StatusFail && !r.FixApplied {
			return true
		}
	}
	return false
}

func renderDoctorResults(out io.Writer, results []doctor.CheckResult) {
	t := theme.DefaultTheme
	var fails, warns int
	for _, r := range results {
		var glyph string
		switch r.Status {
		case doctor.StatusOK:
			glyph = t.Success.Render("✓")
		case doctor.StatusWarn:
			glyph = t.Warning.Render("⚠")
			warns++
		case doctor.StatusFail:
			glyph = t.Error.Render("✗")
			fails++
		default:
			glyph = "?"
		}
		fmt.Fprintf(out, "%s %s: %s\n", glyph, r.ID, r.Message)
		if r.FixApplied {
			fmt.Fprintf(out, "  %s\n", t.Muted.Render("→ fix applied"))
		} else if r.Resolution != "" {
			fmt.Fprintf(out, "  %s\n", t.Muted.Render("→ "+r.Resolution))
		}
		if r.Error != "" {
			fmt.Fprintf(out, "  %s\n", t.Muted.Render("error: "+r.Error))
		}
	}
	if fails+warns == 0 {
		fmt.Fprintln(out, t.Success.Render("all checks passed"))
		return
	}
	fmt.Fprintf(out, "\n%d failure(s), %d warning(s). Run 'grove doctor --fix' to apply safe fixits.\n", fails, warns)
}
