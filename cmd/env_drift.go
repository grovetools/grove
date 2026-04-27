package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/grovetools/grove/pkg/envdrift"
)

func newEnvDriftCmd() *cobra.Command {
	var jsonOutput bool
	var force bool

	cmd := &cobra.Command{
		Use:   "drift [profile]",
		Short: "Detect drift between local configuration and deployed cloud state",
		Long: `Reconcile the configuration against deployed cloud state.

This command runs 'terraform plan' to detect drift between your local
configuration and the live cloud resources. It does not modify any
infrastructure. It never builds images: if the profile defines 'images',
the URIs built on the last successful 'grove env up' are read from
.grove/env/state.json and passed in as tfvars so an image rebuild does
not masquerade as cloud drift.

Results are cached in .grove/env/drift.json for 24 hours. A second
invocation inside that TTL returns the cached summary without re-running
Terraform and announces the cache hit on stderr ('Loaded cached drift
summary from Nm ago'). Pass --force to bypass the cache and run a fresh
terraform plan.

Exit codes match Terraform semantics:
  0 = Succeeded, no drift detected
  1 = Error occurred
  2 = Succeeded, drift detected`,
		Example: `  # Check drift for a specific profile (uses cache if <24h old)
  grove env drift terraform

  # Machine-readable summary for a CI script
  grove env drift terraform --json | jq '{add, change, destroy}'

  # Force a fresh terraform plan even if the cache is still fresh
  grove env drift terraform --force

  # List the resources that are drifting
  grove env drift hybrid-api --json | jq '.resources[].address'`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := ""
			if len(args) == 1 {
				profile = args[0]
				if profile == "default" {
					profile = ""
				}
			}

			stateDir, err := filepath.Abs(filepath.Join(".", ".grove", "env"))
			if err != nil {
				return fmt.Errorf("failed to resolve state dir: %w", err)
			}

			var summary *envdrift.DriftSummary
			if !force {
				cached, checkedAt, err := envdrift.LoadCache(stateDir)
				if err == nil && cached != nil && !envdrift.IsStale(checkedAt) {
					summary = cached
					// Announce cache hits on stderr so --json stays a clean
					// machine-readable stream on stdout.
					age := time.Since(checkedAt).Round(time.Minute)
					if age == 0 {
						fmt.Fprintln(os.Stderr,
							"Loaded cached drift summary from just now (use --force to re-run)")
					} else {
						fmt.Fprintf(os.Stderr,
							"Loaded cached drift summary from %s ago (use --force to re-run)\n",
							age)
					}
				}
			}

			if summary == nil {
				summary, err = envdrift.RunEnvDrift(context.Background(), profile)
				if err != nil {
					return err
				}
			}

			if err := envdrift.EmitSummary(os.Stdout, summary, jsonOutput); err != nil {
				return err
			}

			if summary.HasDrift {
				os.Exit(envdrift.TerraformDriftExitCode)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit JSON summary instead of human-readable output")
	cmd.Flags().BoolVar(&force, "force", false, "Bypass cached drift summary and re-run terraform plan")
	return cmd
}
