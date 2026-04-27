package orchestrator

import (
	"strings"

	"github.com/grovetools/core/config"
)

// ResolveCommand determines the command to run for a given verb and config.
// Resolution order: cfg.Commands[verb] > cfg.BuildCmd (for "build" only) > make <verb>.
func ResolveCommand(cfg *config.Config, verb string) []string {
	if cfg != nil && len(cfg.Commands) > 0 {
		if cmd, ok := cfg.Commands[verb]; ok && cmd != "" {
			return strings.Fields(cmd)
		}
	}
	if verb == "build" && cfg != nil && cfg.BuildCmd != "" {
		return strings.Fields(cfg.BuildCmd)
	}
	return []string{"make", verb}
}
