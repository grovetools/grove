package cmd

import (
	"github.com/mattsolo1/grove-core/starship"
	flowcmd "github.com/mattsolo1/grove-flow/cmd"
	"github.com/spf13/cobra"
)

func init() {
	// Register status providers from Grove tools
	starship.RegisterProvider(flowcmd.FlowStatusProvider)
}

func newStarshipCmd() *cobra.Command {
	return starship.NewStarshipCmd("grove")
}
