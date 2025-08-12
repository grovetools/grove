package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

// Options configures the runner behavior
type Options struct {
	Workspaces []string       // List of workspace paths to run in
	Command    string         // Command to execute
	Args       []string       // Arguments for the command
	JSONMode   bool           // Whether to aggregate JSON output
	Logger     *logrus.Logger // Logger for output
	RootDir    string         // Root directory for relative path display
}

// Result represents the output from a single workspace execution
type Result struct {
	Workspace string      `json:"workspace"`
	Success   bool        `json:"success"`
	Output    interface{} `json:"output,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// Run executes the command across all workspaces
func Run(opts Options) error {
	if opts.JSONMode {
		return runJSONMode(opts)
	}
	return runStreamingMode(opts)
}

// runStreamingMode executes commands with direct output streaming
func runStreamingMode(opts Options) error {
	var hasError bool

	for _, workspace := range opts.Workspaces {
		// Print header
		workspaceName := getWorkspaceName(workspace, opts.RootDir)
		header := fmt.Sprintf("--- Running in '%s' ---", workspaceName)
		fmt.Println(strings.Repeat("-", len(header)))
		fmt.Println(header)
		fmt.Println(strings.Repeat("-", len(header)))

		// Create command
		cmd := exec.Command(opts.Command, opts.Args...)
		cmd.Dir = workspace
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		// Execute
		if err := cmd.Run(); err != nil {
			opts.Logger.WithError(err).Errorf("Command failed in %s", workspaceName)
			hasError = true
		}

		fmt.Println() // Add blank line between workspaces
	}

	if hasError {
		return fmt.Errorf("command failed in one or more workspaces")
	}
	return nil
}

// runJSONMode executes commands and aggregates JSON output
func runJSONMode(opts Options) error {
	var results []interface{}

	for _, workspace := range opts.Workspaces {
		workspaceName := getWorkspaceName(workspace, opts.RootDir)

		// Create command with --json flag
		args := append(opts.Args, "--json")
		cmd := exec.Command(opts.Command, args...)
		cmd.Dir = workspace

		// Capture output
		output, err := cmd.Output()
		if err != nil {
			// Add error result
			results = append(results, Result{
				Workspace: workspaceName,
				Success:   false,
				Error:     err.Error(),
			})
			continue
		}

		// Parse JSON output
		var toolOutput []map[string]interface{}
		if err := json.Unmarshal(output, &toolOutput); err != nil {
			// Try parsing as single object
			var singleOutput map[string]interface{}
			if err2 := json.Unmarshal(output, &singleOutput); err2 != nil {
				// Add error result
				results = append(results, Result{
					Workspace: workspaceName,
					Success:   false,
					Error:     fmt.Sprintf("failed to parse JSON: %v", err),
				})
				continue
			}
			toolOutput = []map[string]interface{}{singleOutput}
		}

		// Add workspace key to each item
		for _, item := range toolOutput {
			item["workspace"] = workspaceName
			results = append(results, item)
		}
	}

	// Output aggregated results
	jsonData, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal results: %w", err)
	}

	fmt.Println(string(jsonData))
	return nil
}

// getWorkspaceName returns a display name for the workspace
func getWorkspaceName(workspacePath, rootDir string) string {
	if rootDir != "" {
		if rel, err := filepath.Rel(rootDir, workspacePath); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return filepath.Base(workspacePath)
}

// RunScript executes a shell script in each workspace
func RunScript(opts Options, script string) error {
	// For scripts, we'll use sh -c
	scriptOpts := opts
	scriptOpts.Command = "sh"
	scriptOpts.Args = []string{"-c", script}

	return Run(scriptOpts)
}

// StreamOutput streams command output with workspace prefixes
func StreamOutput(cmd *exec.Cmd, prefix string, output io.Writer) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	// Stream stdout
	go prefixLines(stdout, prefix, output)
	go prefixLines(stderr, prefix, output)

	return cmd.Wait()
}

func prefixLines(input io.Reader, prefix string, output io.Writer) {
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		fmt.Fprintf(output, "[%s] %s\n", prefix, scanner.Text())
	}
}
