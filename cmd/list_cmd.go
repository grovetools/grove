package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/grovepm/grove/pkg/sdk"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newListCmd())
}

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available Grove tools",
		Long:  "Display all available Grove tools and their installation status",
		Args:  cobra.NoArgs,
		RunE:  runList,
	}

	return cmd
}

type ToolInfo struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
	Versions  []string `json:"versions,omitempty"`
}

func runList(cmd *cobra.Command, args []string) error {
	opts := cli.GetOptions(cmd)
	logger := cli.GetLogger(cmd)

	// Create SDK manager
	manager, err := sdk.NewManager()
	if err != nil {
		return fmt.Errorf("failed to create SDK manager: %w", err)
	}

	// Get installed versions
	installedVersions, err := manager.ListInstalledVersions()
	if err != nil {
		logger.WithError(err).Warn("Failed to list installed versions")
		installedVersions = []string{}
	}

	// Get active version
	activeVersion, err := manager.GetActiveVersion()
	if err != nil {
		logger.WithError(err).Warn("Failed to get active version")
		activeVersion = ""
	}

	// Get all available tools
	allTools := sdk.GetAllTools()
	
	// Build tool info
	var toolInfos []ToolInfo
	for _, toolName := range allTools {
		info := ToolInfo{
			Name:      toolName,
			Installed: false,
			Active:    false,
			Versions:  []string{},
		}

		// Check if tool is installed in any version
		for _, version := range installedVersions {
			versionBinPath := fmt.Sprintf("%s/.grove/versions/%s/bin/%s", os.Getenv("HOME"), version, toolName)
			if _, err := os.Stat(versionBinPath); err == nil {
				info.Installed = true
				info.Versions = append(info.Versions, version)
				
				// Check if this version is active
				if version == activeVersion {
					info.Active = true
				}
			}
		}

		toolInfos = append(toolInfos, info)
	}

	if opts.JSONOutput {
		// Output as JSON
		jsonData, err := json.MarshalIndent(toolInfos, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		fmt.Println(string(jsonData))
		return nil
	}

	// Output as table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TOOL\tSTATUS\tINSTALLED VERSIONS")
	fmt.Fprintln(w, "----\t------\t-----------------")

	for _, info := range toolInfos {
		status := "Not installed"
		if info.Active {
			status = "Active"
		} else if info.Installed {
			status = "Installed"
		}
		
		versions := "-"
		if len(info.Versions) > 0 {
			versions = strings.Join(info.Versions, ", ")
		}
		
		fmt.Fprintf(w, "%s\t%s\t%s\n", info.Name, status, versions)
	}

	w.Flush()

	if activeVersion != "" {
		fmt.Printf("\nActive version: %s\n", activeVersion)
	}

	return nil
}