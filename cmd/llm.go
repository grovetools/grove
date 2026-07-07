package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/flow/pkg/model"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type LLMConfig struct {
	DefaultModel string `yaml:"default_model"`

	// Changelog-generation tuning (grove changelog / release plan LLM changelog).
	// All optional; empty/zero values fall back to the built-in defaults.
	ChangelogModel    string `yaml:"changelog_model"`     // model override just for changelog gen (claude-* recommended)
	ChangelogDiff     string `yaml:"changelog_diff"`      // diff depth fed to the LLM: none|stat|full (default stat)
	ChangelogTokenCap int    `yaml:"changelog_token_cap"` // full-diff token budget; over this, full auto-falls back to stat (default 60000)
	ChangelogCacheTTL string `yaml:"changelog_cache_ttl"` // prefix cache TTL for claude models: 5m|1h (default 5m)

	// CheckCommand is an optional local check command recorded by `grove release
	// gen` per repo (e.g. "go test ./..."). This is an OPENING only: gen records
	// CheckStatus="skipped" and never runs it — no tend/test wiring in this
	// effort. A later phase may execute it.
	CheckCommand string `yaml:"check_command"`
}

func init() {
	rootCmd.AddCommand(newLlmCmd())
}

func newLlmCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("llm", "Unified interface for LLM providers")
	cmd.Long = `The 'grove llm' command provides a single, consistent entry point for all LLM interactions, regardless of the underlying provider (Anthropic, Gemini, OpenRouter, etc.).

It intelligently delegates to the appropriate provider-specific tool based on the model name.`

	cmd.AddCommand(newLlmRequestCmd())
	return cmd
}

func newLlmRequestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "request [prompt...]",
		Short: "Make a request to an LLM provider",
		Long: `Acts as a facade, delegating to the appropriate tool (grove-anthropic, grove-gemini, grove-openrouter) based on the model.

Model determination precedence:
1. --model flag
2. 'llm.default_model' in grove.yml

Supported model families: claude-*, gemini-*, openrouter/* (run 'flow models' for the full list).
`,
		RunE: runLlmRequest,
	}

	// Superset of flags from grove-anthropic and grove-gemini
	cmd.Flags().StringP("model", "m", "", "LLM model to use (e.g., claude-opus-4-8, gemini-2.5-pro, openrouter/openai/gpt-5.2)")
	cmd.Flags().StringP("prompt", "p", "", "Prompt text")
	cmd.Flags().StringP("file", "f", "", "Read prompt from file")
	cmd.Flags().StringP("workdir", "w", "", "Working directory (defaults to current)")
	cmd.Flags().StringP("output", "o", "", "Write response to file instead of stdout")
	cmd.Flags().Bool("regenerate", false, "Regenerate context before request")
	cmd.Flags().StringSlice("context", nil, "Additional context files or directories to include")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompts")

	// Generation parameters
	cmd.Flags().Float32("temperature", -1, "Temperature for randomness (-1 to use default)")
	cmd.Flags().Float32("top-p", -1, "Top-p nucleus sampling (-1 to use default)")
	cmd.Flags().Int32("top-k", -1, "Top-k sampling (-1 to use default)")
	cmd.Flags().Int32("max-output-tokens", -1, "Maximum tokens in response (-1 to use default)")

	// Gemini-specific caching flags
	cmd.Flags().String("cache-ttl", "", "Cache TTL for Gemini (e.g., 1h, 30m)")
	cmd.Flags().Bool("no-cache", false, "Disable context caching for Gemini")
	cmd.Flags().Bool("recache", false, "Force recreation of the Gemini cache")
	cmd.Flags().String("use-cache", "", "Specify a Gemini cache name to use")

	return cmd
}

func runLlmRequest(cmd *cobra.Command, args []string) error {
	// 0. Get request ID from environment for tracing
	requestID := os.Getenv("GROVE_REQUEST_ID")

	// 1. Determine model
	modelVar, _ := cmd.Flags().GetString("model")
	if modelVar == "" {
		// Try to load from grove.yml
		cfg, err := config.LoadDefault()
		if err == nil {
			var llmCfg LLMConfig
			if cfg.UnmarshalExtension("llm", &llmCfg) == nil && llmCfg.DefaultModel != "" {
				modelVar = llmCfg.DefaultModel
			}
		}
	}
	if modelVar == "" {
		return fmt.Errorf("no model specified. Use --model or set 'llm.default_model' in grove.yml")
	}

	// 2. Determine target binary from the canonical model->provider registry.
	provider, ok := model.LookupModelProvider(modelVar)
	if !ok {
		return fmt.Errorf("unrecognized model %q; run 'flow models' for supported models (claude-*, gemini-*, openrouter/*)", modelVar)
	}
	var targetBinary string
	switch provider {
	case model.ProviderAnthropic:
		targetBinary = "grove-anthropic"
	case model.ProviderGoogle:
		targetBinary = "grove-gemini"
	case model.ProviderOpenRouter:
		targetBinary = "grove-openrouter"
	default:
		return fmt.Errorf("unrecognized model provider %q for '%s'", provider, modelVar)
	}

	// Log delegation decision (debug-level to avoid polluting stdout when piping)
	log := cli.GetLogger(cmd)
	log.WithFields(logrus.Fields{
		"request_id":   requestID,
		"model":        modelVar,
		"delegated_to": targetBinary,
	}).Debug("Delegating LLM request to provider")

	// 3. Construct arguments for delegation
	var delegateArgs []string
	delegateArgs = append(delegateArgs, "request") // All tools use the 'request' subcommand

	// grove-anthropic and grove-openrouter support narrower flag sets than
	// grove-gemini; drop flags they don't understand and translate the
	// max-tokens flag name. grove-openrouter additionally has no --no-cache
	// flag (its request supports model/prompt/file/workdir/output/context/
	// regenerate/max-tokens/system).
	anthropicUnsupported := map[string]bool{
		"yes":         true,
		"temperature": true, "top-p": true, "top-k": true,
		"cache-ttl": true, "recache": true, "use-cache": true,
	}
	openrouterUnsupported := map[string]bool{
		"yes":         true,
		"temperature": true, "top-p": true, "top-k": true,
		"cache-ttl": true, "recache": true, "use-cache": true,
		"no-cache": true,
	}

	// Select the drop-map for the target binary. grove-gemini accepts the
	// full flag superset, so it has no drop-map. Both grove-anthropic and
	// grove-openrouter want the max-output-tokens -> max-tokens rename.
	var dropFlags map[string]bool
	renameMaxTokens := false
	switch targetBinary {
	case "grove-anthropic":
		dropFlags = anthropicUnsupported
		renameMaxTokens = true
	case "grove-openrouter":
		dropFlags = openrouterUnsupported
		renameMaxTokens = true
	}

	// Add all flags that were explicitly set by the user
	cmd.Flags().Visit(func(f *pflag.Flag) {
		// Handle special cases
		if f.Name == "model" { // Pass the resolved model
			delegateArgs = append(delegateArgs, "--model", modelVar)
			return
		}

		name := f.Name
		if dropFlags[name] {
			log.WithField("flag", name).WithField("binary", targetBinary).Debug("Dropping flag not supported by target binary")
			return
		}
		if renameMaxTokens && name == "max-output-tokens" {
			name = "max-tokens"
		}

		// For slice flags, we need to append them correctly
		if f.Value.Type() == "stringSlice" {
			slice, _ := cmd.Flags().GetStringSlice(f.Name)
			for _, val := range slice {
				delegateArgs = append(delegateArgs, fmt.Sprintf("--%s", name), val)
			}
		} else {
			delegateArgs = append(delegateArgs, fmt.Sprintf("--%s", name), f.Value.String())
		}
	})

	// Add positional arguments
	delegateArgs = append(delegateArgs, args...)

	// 4. Execute the target binary directly
	execCmd := exec.Command(targetBinary, delegateArgs...)
	execCmd.Stdin = os.Stdin
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr

	// Propagate request ID to the child process
	if requestID != "" {
		execCmd.Env = append(os.Environ(), "GROVE_REQUEST_ID="+requestID)
	}

	return execCmd.Run()
}
