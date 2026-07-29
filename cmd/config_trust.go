package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/exectrust"
	"github.com/spf13/cobra"
)

// newConfigTrustCmd builds the `grove config trust` subcommand: the review-
// and-allow UX for the exec-provenance gate in core/config. The gate strips
// shell commands that reach the merged config from a cloned repository's
// grove.toml; this command shows exactly what is being withheld and lets the
// user allow it after reading it.
func newConfigTrustCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trust",
		Short: "Review and trust a workspace's exec-bearing config",
		Long: `Show every shell command a workspace's config layers would run, and
decide whether to allow them.

Grove's config cascade merges grove.toml files that come out of cloned
repositories. Keys like [[hooks.on_stop]], [tui.plugins], [tui.panels.bindings]
and [[daemon.hooks.on_skill_sync]] carry commands grove executes on its own —
when a session stops, when the TUI boots, on a keypress — so grove withholds
them until you have read them.

Bare 'grove config trust' is report-only. --yes records the decision, keyed to
a digest of exactly the commands shown: if the repo later edits or adds one,
the gate closes again and you are asked about the new content.

  grove config trust            review what would be enabled
  grove config trust --yes      allow the config files in scope
  grove config trust --revoke   withdraw trust for the files in scope
  grove config trust --list     list every config file you have trusted`,
		RunE: runConfigTrust,
	}
	cmd.Flags().Bool("yes", false, "Trust the config files in scope")
	cmd.Flags().Bool("revoke", false, "Withdraw trust for the config files in scope")
	cmd.Flags().Bool("list", false, "List every trusted config file")
	cmd.Flags().Bool("json", false, "Output as JSON")
	return cmd
}

func runConfigTrust(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	listOnly, _ := cmd.Flags().GetBool("list")
	doTrust, _ := cmd.Flags().GetBool("yes")
	doRevoke, _ := cmd.Flags().GetBool("revoke")

	if doTrust && doRevoke {
		return fmt.Errorf("--yes and --revoke are mutually exclusive")
	}
	if listOnly {
		return runConfigTrustList(jsonOutput)
	}

	cwd, _ := os.Getwd()
	layered, err := config.LoadLayered(cwd)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	var report *config.ExecGateReport
	if layered.Final != nil {
		report = layered.Final.ExecGate
	}
	if report == nil || len(report.Files) == 0 {
		if jsonOutput {
			return printJSON(map[string]interface{}{"files": []config.ExecGateFile{}, "findings": []config.ExecFinding{}})
		}
		fmt.Println("No exec-bearing config from repo-controlled layers here — nothing to trust.")
		return nil
	}

	switch {
	case doTrust:
		return applyConfigTrust(report, jsonOutput)
	case doRevoke:
		return revokeConfigTrust(report, jsonOutput)
	case jsonOutput:
		return printJSON(report)
	default:
		printExecGateReport(report)
		return nil
	}
}

// runConfigTrustList prints the whole trust store, which is machine-wide
// rather than scoped to the current directory.
func runConfigTrustList(jsonOutput bool) error {
	entries := exectrust.Load().Entries()
	if jsonOutput {
		out := make([]map[string]string, 0, len(entries))
		for _, e := range entries {
			out = append(out, map[string]string{"path": e.Path, "digest": e.Digest, "trusted_at": e.TrustedAt})
		}
		return printJSON(out)
	}
	if len(entries) == 0 {
		fmt.Println("No config files trusted yet.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TRUSTED AT\tFILE")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\n", e.TrustedAt, e.Path)
	}
	return w.Flush()
}

// applyConfigTrust records the current digest for every file in scope that is
// not already trusted at it.
func applyConfigTrust(report *config.ExecGateReport, jsonOutput bool) error {
	store := exectrust.Load()
	now := time.Now()
	var changed []string
	for _, f := range report.Files {
		if f.Trusted {
			continue
		}
		store.Trust(f.Path, f.Digest, now)
		changed = append(changed, f.Path)
	}
	if len(changed) == 0 {
		if jsonOutput {
			return printJSON(map[string]interface{}{"trusted": []string{}, "already_trusted": true})
		}
		fmt.Println("Every config file in scope is already trusted at its current contents.")
		return nil
	}
	if err := store.Save(); err != nil {
		return fmt.Errorf("failed to record trust: %w", err)
	}
	if jsonOutput {
		return printJSON(map[string]interface{}{"trusted": changed})
	}
	for _, p := range changed {
		fmt.Printf("Trusted %s\n", p)
	}
	fmt.Println()
	fmt.Println("Its exec-bearing config is now honored. If the file's commands change,")
	fmt.Println("grove will withhold them again until you review the new ones.")
	return nil
}

// revokeConfigTrust drops the records for every file in scope.
func revokeConfigTrust(report *config.ExecGateReport, jsonOutput bool) error {
	store := exectrust.Load()
	var changed []string
	for _, f := range report.Files {
		if store.Revoke(f.Path) {
			changed = append(changed, f.Path)
		}
	}
	if len(changed) == 0 {
		if jsonOutput {
			return printJSON(map[string]interface{}{"revoked": []string{}})
		}
		fmt.Println("No config file in scope was trusted — nothing to revoke.")
		return nil
	}
	if err := store.Save(); err != nil {
		return fmt.Errorf("failed to record revocation: %w", err)
	}
	if jsonOutput {
		return printJSON(map[string]interface{}{"revoked": changed})
	}
	for _, p := range changed {
		fmt.Printf("Revoked trust for %s\n", p)
	}
	return nil
}

// printExecGateReport renders the report diff-style: every command the config
// would run, grouped by the file that declares it, marked with whether it is
// currently withheld.
func printExecGateReport(report *config.ExecGateReport) {
	byFile := map[string][]config.ExecFinding{}
	for _, f := range report.Findings {
		byFile[f.File] = append(byFile[f.File], f)
	}

	fmt.Printf("Exec-bearing config from repo-controlled layers (policy: %s)\n", report.Mode)

	var ignored, honored int
	for _, file := range report.Files {
		state := "UNTRUSTED"
		if file.Trusted {
			state = "trusted"
		}
		fmt.Println()
		fmt.Printf("  %s  (%s, %s)\n", file.Path, file.Layer, state)

		findings := byFile[file.Path]
		sort.SliceStable(findings, func(i, j int) bool { return findings[i].Key < findings[j].Key })
		for _, f := range findings {
			marker, status := "+", "honored"
			if f.Quarantined {
				marker, status = "-", "IGNORED"
				ignored++
			} else {
				honored++
			}
			fmt.Printf("    %s %s  [%s, %s]\n", marker, f.Key, f.Risk, status)
			fmt.Printf("        %s\n", f.Description)
			for _, line := range strings.Split(f.Value, "\n") {
				fmt.Printf("        %s\n", line)
			}
		}
	}

	fmt.Println()
	fmt.Printf("%d value(s) ignored, %d honored.\n", ignored, honored)
	if ignored > 0 {
		fmt.Println()
		fmt.Println("Read the commands above before allowing them — they run on your machine")
		fmt.Println("with your privileges. To allow them:")
		fmt.Println()
		fmt.Println("    grove config trust --yes")
	}
}

// warnUntrustedExecConfig prints a one-line notice to stderr when the gate
// withheld something from the loaded config. Used by the commands that show
// the merged/audited config, where the withheld keys are invisible by
// construction and their absence would otherwise read as a bug.
func warnUntrustedExecConfig(layered *config.LayeredConfig) {
	if layered == nil || layered.Final == nil {
		return
	}
	report := layered.Final.ExecGate
	if !report.HasQuarantined() {
		return
	}
	fmt.Fprintf(os.Stderr,
		"⚠ %d exec-bearing config value(s) are being ignored because their workspace is not trusted.\n"+
			"  Review them with `grove config trust`.\n\n",
		len(report.Quarantined()))
}

// printJSON is the shared indented-JSON emitter for this command.
func printJSON(v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode output: %w", err)
	}
	fmt.Println(string(data))
	return nil
}
