package cmd

import (
	"github.com/mattsolo1/grove-core/starship"
	"github.com/spf13/cobra"
)

func newStarshipCmd() *cobra.Command {
	return starship.NewStarshipCmd("grove")
}
