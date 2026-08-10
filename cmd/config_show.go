package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const effectiveConfigSchemaVersion = 1

var errConfigDegraded = errors.New("effective configuration is degraded")

// effectiveConfigEnvelope is the stable machine-readable contract for
// `grove config show --effective --json`. Error is deliberately present as
// null on healthy output so consumers do not need to infer the envelope shape.
type effectiveConfigEnvelope struct {
	SchemaVersion int                    `json:"schema_version"`
	Degraded      bool                   `json:"degraded"`
	Effective     map[string]interface{} `json:"effective"`
	Error         *string                `json:"error"`
}

func newConfigShowCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("show", "Show resolved configuration")
	cmd.Long = `Show Grove configuration after all active layers are merged.

--effective selects the stable effective-config surface. When a config layer is
malformed, the command still emits a defaults-only partial view and the
path-qualified load error, marks the result degraded, and exits nonzero.`
	cmd.SilenceUsage = true
	cmd.Flags().Bool("effective", false, "show the effective merged configuration")
	cmd.RunE = runConfigShow
	return cmd
}

func runConfigShow(cmd *cobra.Command, _ []string) error {
	effective, _ := cmd.Flags().GetBool("effective")
	if !effective {
		return fmt.Errorf("--effective is required (use 'grove config' to edit configuration)")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	envelope, loadErr := loadEffectiveConfig(cwd)

	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(envelope); err != nil {
			return fmt.Errorf("failed to encode effective configuration: %w", err)
		}
	} else if err := renderEffectiveConfig(cmd.OutOrStdout(), envelope); err != nil {
		return err
	}

	if loadErr != nil {
		return fmt.Errorf("%w: %v", errConfigDegraded, loadErr)
	}
	return nil
}

func loadEffectiveConfig(startDir string) (effectiveConfigEnvelope, error) {
	layered, loadErr := config.LoadLayered(startDir)
	var final *config.Config
	if layered != nil {
		final = layered.Final
	}
	if final == nil {
		// LoadLayered cannot currently return its successfully loaded prefix on
		// error. Defaults are still a truthful, useful partial effective view.
		final = &config.Config{}
		final.SetDefaults()
	}

	effective, marshalErr := configAsEffectiveMap(final)
	if marshalErr != nil {
		return effectiveConfigEnvelope{}, marshalErr
	}
	envelope := effectiveConfigEnvelope{
		SchemaVersion: effectiveConfigSchemaVersion,
		Degraded:      loadErr != nil,
		Effective:     effective,
	}
	if loadErr != nil {
		message := loadErr.Error()
		envelope.Error = &message
	}
	return envelope, loadErr
}

// configAsEffectiveMap starts with the source-key representation and adds the
// compiled code-root compatibility view. Config.Groves is intentionally
// yaml:"-" because roots.toml is not an authoring layer, but omitting it from
// config-show would make topology migrations impossible to compare.
func configAsEffectiveMap(cfg *config.Config) (map[string]interface{}, error) {
	asMap, err := configAsMap(cfg)
	if err != nil {
		return nil, err
	}
	groves := map[string]interface{}{}
	if cfg != nil {
		for name, grove := range cfg.Groves {
			entry := map[string]interface{}{"path": grove.Path}
			if grove.Enabled != nil {
				entry["enabled"] = *grove.Enabled
			}
			if grove.Description != "" {
				entry["description"] = grove.Description
			}
			if grove.Notebook != "" {
				entry["notebook"] = grove.Notebook
			}
			if grove.Depth != nil {
				entry["depth"] = *grove.Depth
			}
			if len(grove.IncludeRepos) > 0 {
				entry["include_repos"] = grove.IncludeRepos
			}
			if len(grove.ExcludeRepos) > 0 {
				entry["exclude_repos"] = grove.ExcludeRepos
			}
			groves[name] = entry
		}
	}
	asMap["groves"] = groves
	return asMap, nil
}

func renderEffectiveConfig(out interface{ Write([]byte) (int, error) }, envelope effectiveConfigEnvelope) error {
	if envelope.Degraded {
		fmt.Fprintln(out, "CONFIG DEGRADED — effective configuration is partial")
		fmt.Fprintf(out, "Error: %s\n\n", *envelope.Error)
	}
	fmt.Fprintln(out, "Effective configuration:")
	data, err := yaml.Marshal(envelope.Effective)
	if err != nil {
		return fmt.Errorf("failed to encode effective configuration: %w", err)
	}
	_, err = out.Write(data)
	return err
}
