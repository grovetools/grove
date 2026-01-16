package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/grovetools/core/starship"
	"github.com/grovetools/core/util/delegation"
	"github.com/spf13/cobra"
)

func newStarshipCmd() *cobra.Command {
	cmd := starship.NewStarshipCmd("grove")

	// Override the status command to aggregate from all Grove tools
	for _, subcmd := range cmd.Commands() {
		if subcmd.Name() == "status" {
			subcmd.RunE = runAggregatedStarshipStatus
		}
	}

	return cmd
}

func runAggregatedStarshipStatus(cmd *cobra.Command, args []string) error {
	// List of Grove tools that might have starship status
	tools := []string{"flow", "notebook", "hooks"}

	var outputs []string
	for _, tool := range tools {
		// Check if tool exists
		if _, err := exec.LookPath(tool); err != nil {
			continue
		}

		// Call tool's starship status via 'grove' for workspace-awareness
		out, err := delegation.Command(tool, "starship", "status").Output()
		if err != nil {
			continue
		}

		result := strings.TrimSpace(string(out))
		if result != "" {
			outputs = append(outputs, result)
		}
	}

	// Print aggregated output
	if len(outputs) > 0 {
		fmt.Print(strings.Join(outputs, " | "))
	}

	return nil
}
