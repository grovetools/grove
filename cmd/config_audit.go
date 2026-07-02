package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/grovetools/core/config"
	"github.com/spf13/cobra"
)

// newConfigAuditCmd builds the `grove config audit` subcommand. It is
// attached in newConfigCmd (config_edit.go); the parent keeps its own RunE,
// so bare `grove config` still opens the TUI and cobra only routes here for
// `grove config audit`.
func newConfigAuditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Audit config layers for stale and unknown keys",
		Long: `Classify every key set in every config layer file.

Each key is checked against the core Config struct and the extension
registry, then reported with its layer and source file:

  known-core        read by the core config decoder
  known-extension   registered extension namespace (code owns its shape)
  deprecated        still read, but should be migrated
  unknown-nested    nested key the decoder silently drops
  orphan            top-level key nothing reads

This is report-only: nothing is modified and the exit code is always 0.
With --json, the findings array is printed to stdout.`,
		RunE: runConfigAudit,
	}
	cmd.Flags().Bool("json", false, "Output findings as JSON")
	return cmd
}

func runConfigAudit(cmd *cobra.Command, args []string) error {
	cwd, _ := os.Getwd()

	findings, err := config.Audit(cwd)
	if err != nil {
		return fmt.Errorf("failed to audit configuration: %w", err)
	}

	if jsonOutput, _ := cmd.Flags().GetBool("json"); jsonOutput {
		return printAuditJSON(findings)
	}
	printAuditTable(findings)
	return nil
}

// printAuditJSON emits the findings array as indented JSON.
func printAuditJSON(findings []config.AuditFinding) error {
	if findings == nil {
		findings = []config.AuditFinding{}
	}
	data, err := json.MarshalIndent(findings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode audit findings: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// printAuditTable renders an aligned table of findings: healthy keys first,
// then orphans and unknown-nested keys grouped under a warning header, and a
// summary line.
func printAuditTable(findings []config.AuditFinding) {
	var healthy, problems []config.AuditFinding
	counts := make(map[config.AuditClass]int)
	for _, f := range findings {
		counts[f.Class]++
		switch f.Class {
		case config.AuditOrphan, config.AuditUnknownNested:
			problems = append(problems, f)
		default:
			healthy = append(healthy, f)
		}
	}

	if len(findings) == 0 {
		fmt.Println("No config layer files found — nothing to audit.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tCLASS\tLAYER\tFILE")
	for _, f := range healthy {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", f.Key, f.Class, f.Layer, f.File)
	}
	w.Flush()

	if len(problems) > 0 {
		fmt.Println()
		fmt.Println("⚠ Keys nothing reads (candidates for removal):")
		fmt.Println()
		w = tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "KEY\tCLASS\tLAYER\tFILE")
		for _, f := range problems {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", f.Key, f.Class, f.Layer, f.File)
		}
		w.Flush()
	}

	fmt.Println()
	fmt.Printf("%d keys audited: %d known-core, %d known-extension, %d deprecated, %d unknown-nested, %d orphan\n",
		len(findings),
		counts[config.AuditKnownCore],
		counts[config.AuditKnownExtension],
		counts[config.AuditDeprecated],
		counts[config.AuditUnknownNested],
		counts[config.AuditOrphan])
}
