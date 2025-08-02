package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	
	"github.com/mattsolo1/grove-core/cli"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newListCmd())
}

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available Grove tools",
		Long:  "Display all available Grove tools from the registry",
		Args:  cobra.NoArgs,
		RunE:  runList,
	}
	
	return cmd
}

func runList(cmd *cobra.Command, args []string) error {
	opts := cli.GetOptions(cmd)
	
	registry, err := LoadRegistry()
	if err != nil {
		return fmt.Errorf("failed to load registry: %w", err)
	}
	
	if opts.JSONOutput {
		// Output as JSON
		jsonData, err := json.MarshalIndent(registry.Tools, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		fmt.Println(string(jsonData))
		return nil
	}
	
	// Output as table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tALIAS\tDESCRIPTION\tVERSION")
	fmt.Fprintln(w, "----\t-----\t-----------\t-------")
	
	for _, tool := range registry.Tools {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", 
			tool.Name, 
			tool.Alias, 
			tool.Description,
			tool.Version,
		)
	}
	
	return w.Flush()
}