package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type LLMConfig struct {
	DefaultModel string `yaml:"default_model"`
}

func init() {
	rootCmd.AddCommand(newLlmCmd())
}

func newLlmCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("llm", "Unified interface for LLM providers")
	cmd.Long = `The 'grove llm' command provides a single, consistent entry point for all LLM interactions, regardless of the underlying provider (Anthropic, Gemini, etc.).

It intelligently delegates to the appropriate provider-specific tool based on the model name.`

	cmd.AddCommand(newLlmRequestCmd())
	return cmd
}

func newLlmRequestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "request [prompt...]",
		Short: "Make a request to an LLM provider",
		Long: `Acts as a facade, delegating to the appropriate tool (grove-anthropic, grove-gemini) based on the model.

Model determination precedence:
1. --model flag
2. 'llm.default_model' in grove.yml
`,
		RunE: runLlmRequest,
	}

	// Superset of flags from grove-anthropic and grove-gemini
	cmd.Flags().StringP("model", "m", "", "LLM model to use (e.g., claude-opus-4-8, gemini-2.5-pro)")
	cmd.Flags().StringP("prompt", "p", "", "Prompt text")
	cmd.Flags().StringP("file", "f", "", "Read prompt from file")
	cmd.Flags().StringP("workdir", "w", "", "Working directory (defaults to current)")
	cmd.Flags().StringP("output", "o", "", "Write response to file instead of stdout")
	cmd.Flags().Bool("regenerate", false, "Regenerate context before request")
	cmd.Flags().Bool("stream", false, "Stream the response (if supported by provider)")
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
	model, _ := cmd.Flags().GetString("model")
	if model == "" {
		// Try to load from grove.yml
		cfg, err := config.LoadDefault()
		if err == nil {
			var llmCfg LLMConfig
			if cfg.UnmarshalExtension("llm", &llmCfg) == nil && llmCfg.DefaultModel != "" {
				model = llmCfg.DefaultModel
			}
		}
	}
	if model == "" {
		return fmt.Errorf("no model specified. Use --model or set 'llm.default_model' in grove.yml")
	}

	// 2. Determine target binary
	var targetBinary string
	if strings.HasPrefix(model, "claude-") {
		targetBinary = "grove-anthropic"
	} else if strings.HasPrefix(model, "gemini-") {
		targetBinary = "grove-gemini"
	} else {
		return fmt.Errorf("unrecognized model provider for '%s'. Model must start with 'claude-' or 'gemini-'", model)
	}

	// Log delegation decision (debug-level to avoid polluting stdout when piping)
	log := cli.GetLogger(cmd)
	log.WithFields(logrus.Fields{
		"request_id":   requestID,
		"model":        model,
		"delegated_to": targetBinary,
	}).Debug("Delegating LLM request to provider")

	// 3. Construct arguments for delegation
	var delegateArgs []string
	delegateArgs = append(delegateArgs, "request") // All tools use the 'request' subcommand

	// grove-anthropic supports a narrower flag set than grove-gemini; drop
	// flags it doesn't understand and translate the max-tokens flag name.
	anthropicUnsupported := map[string]bool{
		"stream": true, "yes": true,
		"temperature": true, "top-p": true, "top-k": true,
		"cache-ttl": true, "no-cache": true, "recache": true, "use-cache": true,
	}

	// Add all flags that were explicitly set by the user
	cmd.Flags().Visit(func(f *pflag.Flag) {
		// Handle special cases
		if f.Name == "model" { // Pass the resolved model
			delegateArgs = append(delegateArgs, "--model", model)
			return
		}

		name := f.Name
		if targetBinary == "grove-anthropic" {
			if anthropicUnsupported[name] {
				log.WithField("flag", name).Debug("Dropping flag not supported by grove-anthropic")
				return
			}
			if name == "max-output-tokens" {
				name = "max-tokens"
			}
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
