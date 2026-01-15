package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// registerDeprecatedCommands adds hidden shims for commands that have been moved.
// These shims print deprecation warnings to help users discover the new locations.
func registerDeprecatedCommands(rootCmd *cobra.Command) {
	// Deprecated: starship moved to setup starship
	rootCmd.AddCommand(&cobra.Command{
		Use:    "starship",
		Short:  "DEPRECATED: Use 'grove setup starship' instead",
		Hidden: true,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(os.Stderr, "Warning: 'grove starship' is deprecated and will be removed in a future version.")
			fmt.Fprintln(os.Stderr, "Please use 'grove setup starship' instead.")
		},
	})

	// Deprecated: git-hooks moved to setup git-hooks
	rootCmd.AddCommand(&cobra.Command{
		Use:    "git-hooks",
		Short:  "DEPRECATED: Use 'grove setup git-hooks' instead",
		Hidden: true,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(os.Stderr, "Warning: 'grove git-hooks' is deprecated and will be removed in a future version.")
			fmt.Fprintln(os.Stderr, "Please use 'grove setup git-hooks' instead.")
		},
	})

	// Deprecated: changelog moved to release changelog
	rootCmd.AddCommand(&cobra.Command{
		Use:    "changelog",
		Short:  "DEPRECATED: Use 'grove release changelog' instead",
		Hidden: true,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(os.Stderr, "Warning: 'grove changelog' is deprecated and will be removed in a future version.")
			fmt.Fprintln(os.Stderr, "Please use 'grove release changelog' instead.")
		},
	})
}
