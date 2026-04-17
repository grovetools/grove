package cmd

import (
	"context"
	"os"

	"github.com/grovetools/grove/pkg/envdrift"
	"github.com/spf13/cobra"
)

func newEnvDriftCmd() *cobra.Command {
	var jsonOutput bool

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

Exit codes match Terraform semantics:
  0 = Succeeded, no drift detected
  1 = Error occurred
  2 = Succeeded, drift detected`,
		Example: `  # Check drift for a specific profile
  grove env drift terraform

  # Machine-readable summary for a CI script
  grove env drift terraform --json | jq '{add, change, destroy}'

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

			summary, err := envdrift.RunEnvDrift(context.Background(), profile)
			if err != nil {
				return err
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
	return cmd
}
