package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/config"
	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/util/delegation"
)

// Logger for changelog LLM operations
var changelogLog = logging.NewUnifiedLogger("grove-meta.changelog-llm")

// LLMChangelogResult holds the structured response from the LLM.
type LLMChangelogResult struct {
	Suggestion    string `json:"suggestion"`    // "major", "minor", or "patch"
	Justification string `json:"justification"` // A brief reason for the suggestion
	Changelog     string `json:"changelog"`     // The full markdown changelog
}

// runLLMChangelog is the main entry point for LLM-based changelog generation.
func runLLMChangelog(repoPath, newVersion string) error {
	// 1. Get the commit range (from last tag to HEAD).
	lastTag, err := getLastTag(repoPath)
	if err != nil {
		fmt.Printf("Warning: could not get last tag for %s, analyzing all commits: %v\n", repoPath, err)
		lastTag = "" // Will analyze all commits if no tag is found
	}
	commitRange := "HEAD"
	if lastTag != "" {
		commitRange = fmt.Sprintf("%s..HEAD", lastTag)
		fmt.Printf("Analyzing commits from %s to HEAD\n", lastTag)
	} else {
		fmt.Println("Analyzing all commits (no previous tag found)")
	}

	// 2. Gather git context.
	// Get detailed commit log with prominent commit hashes
	// Format includes both full and short hash for easy reference
	logCmd := exec.Command("git", "log", commitRange, "--pretty=format:commit %H (%h)%nAuthor: %an <%ae>%nDate: %ad%nCommit: %cn <%ce>%nCommitDate: %cd%n%n    %s%n%n%b%n")
	logCmd.Dir = repoPath
	logOutput, err := logCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get git log: %w\n%s", err, string(logOutput))
	}

	// Get diff statistics
	diffCmd := exec.Command("git", "diff", "--stat", commitRange)
	diffCmd.Dir = repoPath
	diffOutput, err := diffCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get git diff: %w\n%s", err, string(diffOutput))
	}

	// Combine git context
	gitContext := fmt.Sprintf("GIT LOG:\n%s\n\nGIT DIFF STAT:\n%s", string(logOutput), string(diffOutput))

	// 3. Generate changelog with LLM.
	result, err := generateChangelogWithLLM(gitContext, newVersion, repoPath)
	if err != nil {
		return fmt.Errorf("failed to generate changelog with LLM: %w", err)
	}
	changelogContent := result.Changelog

	// 4. Prepend to CHANGELOG.md.
	changelogPath := filepath.Join(repoPath, "CHANGELOG.md")
	existingContent, _ := os.ReadFile(changelogPath)
	
	// Ensure proper spacing between entries
	newContent := changelogContent
	if len(existingContent) > 0 {
		newContent = changelogContent + "\n" + string(existingContent)
	}

	return os.WriteFile(changelogPath, []byte(newContent), 0644)
}

// getLastTag finds the most recent git tag in a repository.
func getLastTag(repoPath string) (string, error) {
	cmd := exec.Command("git", "describe", "--tags", "--abbrev=0")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// promptForRulesEdit checks if a .grove/rules file exists and asks the user if they want to edit it
// skipPrompt will skip the interactive prompt (useful for TUI mode)
func promptForRulesEdit(repoPath string, skipPrompt bool) error {
	rulesPath := filepath.Join(repoPath, ".grove", "rules")
	
	// In TUI mode, just ensure the rules file exists but don't prompt
	if skipPrompt {
		_, err := os.Stat(rulesPath)
		if os.IsNotExist(err) {
			// Create .grove directory if it doesn't exist
			groveDir := filepath.Join(repoPath, ".grove")
			if err := os.MkdirAll(groveDir, 0755); err != nil {
				return fmt.Errorf("failed to create .grove directory: %w", err)
			}
			
			// Create empty rules file with helpful comments
			content := `# Grove rules file for LLM context
# Add file paths or patterns here, one per line
# Examples:
#   README.md
#   docs/*.md
#   src/main.go
`
			if err := os.WriteFile(rulesPath, []byte(content), 0644); err != nil {
				return fmt.Errorf("failed to create rules file: %w", err)
			}
			fmt.Printf("Created %s (edit to customize LLM context)\n", rulesPath)
		}
		return nil
	}
	
	// Check if rules file exists
	_, err := os.Stat(rulesPath)
	if os.IsNotExist(err) {
		// No rules file, ask if user wants to create one
		fmt.Printf("\nNo .grove/rules file found in %s\n", filepath.Base(repoPath))
		fmt.Print("Would you like to:\n")
		fmt.Print("  1) Auto-populate from files changed since last release\n")
		fmt.Print("  2) Create empty rules file and edit manually\n")
		fmt.Print("  3) Skip (no additional context)\n")
		fmt.Print("Choice [1/2/3]: ")
		
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(response)
		
		switch response {
		case "1":
			// Auto-populate using cx from-git since
			if err := autoPopulateRules(repoPath); err != nil {
				fmt.Printf("Warning: Failed to auto-populate rules: %v\n", err)
				fmt.Print("Would you like to create and edit manually instead? (y/N): ")
				response, _ = reader.ReadString('\n')
				response = strings.TrimSpace(strings.ToLower(response))
				if response == "y" || response == "yes" {
					return createAndEditRules(repoPath)
				}
			} else {
				fmt.Print("Would you like to review/edit the auto-populated rules? (y/N): ")
				response, _ = reader.ReadString('\n')
				response = strings.TrimSpace(strings.ToLower(response))
				if response == "y" || response == "yes" {
					return openInEditor(rulesPath)
				}
			}
		case "2":
			return createAndEditRules(repoPath)
		case "3":
			// Skip
			return nil
		default:
			// Default to skip
			return nil
		}
	} else if err == nil {
		// Rules file exists, ask if user wants to edit it
		fmt.Printf("\nFound .grove/rules file in %s\n", filepath.Base(repoPath))
		fmt.Print("Would you like to:\n")
		fmt.Print("  1) Update from files changed since last release\n") 
		fmt.Print("  2) Edit manually\n")
		fmt.Print("  3) Use as-is\n")
		fmt.Print("Choice [1/2/3]: ")
		
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(response)
		
		switch response {
		case "1":
			// Update using cx from-git since
			if err := autoPopulateRules(repoPath); err != nil {
				fmt.Printf("Warning: Failed to update rules: %v\n", err)
			}
			fmt.Print("Would you like to review/edit the updated rules? (y/N): ")
			response, _ = reader.ReadString('\n')
			response = strings.TrimSpace(strings.ToLower(response))
			if response == "y" || response == "yes" {
				return openInEditor(rulesPath)
			}
		case "2":
			return openInEditor(rulesPath)
		case "3":
			// Use as-is
			return nil
		default:
			// Default to use as-is
			return nil
		}
	}
	
	return nil
}

// createAndEditRules creates a new rules file and opens it in editor
func createAndEditRules(repoPath string) error {
	rulesPath := filepath.Join(repoPath, ".grove", "rules")
	
	// Create .grove directory if it doesn't exist
	groveDir := filepath.Join(repoPath, ".grove")
	if err := os.MkdirAll(groveDir, 0755); err != nil {
		return fmt.Errorf("failed to create .grove directory: %w", err)
	}
	
	// Create rules file with helpful comments
	content := `# Grove rules file for LLM context
# Add file paths or patterns here, one per line
# Examples:
#   README.md
#   docs/*.md
#   src/main.go
`
	if err := os.WriteFile(rulesPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to create rules file: %w", err)
	}
	
	fmt.Printf("Created %s\n", rulesPath)
	return openInEditor(rulesPath)
}

// autoPopulateRules uses cx from-git since to populate the rules file
func autoPopulateRules(repoPath string) error {
	// Get the last tag to use as the --since value
	lastTag, err := getLastTag(repoPath)
	if err != nil || lastTag == "" {
		return fmt.Errorf("no previous tag found to use as reference")
	}
	
	fmt.Printf("Auto-populating rules from files changed since %s...\n", lastTag)

	// Run 'grove cx from-git' for workspace-awareness
	cmd := delegation.Command("cx", "from-git", "--since", lastTag)
	cmd.Dir = repoPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run grove cx from-git: %w", err)
	}
	
	fmt.Printf("Successfully populated rules file with changed files\n")
	return nil
}

// openInEditor opens a file in the user's preferred editor
func openInEditor(filepath string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim" // Default to vim if EDITOR is not set
	}
	
	cmd := exec.Command(editor, filepath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	fmt.Printf("Opening %s in %s...\n", filepath, editor)
	return cmd.Run()
}

// generateChangelogWithLLM constructs a prompt and calls gemapi to generate the changelog.
// It now returns the new LLMChangelogResult struct.
func generateChangelogWithLLM(gitContext, newVersion, repoPath string) (*LLMChangelogResult, error) {
	return generateChangelogWithLLMInteractive(gitContext, newVersion, repoPath, false)
}

// generateChangelogWithLLMInteractive constructs a prompt and calls gemapi to generate the changelog.
// skipPrompt controls whether to skip interactive prompts (useful for TUI mode).
func generateChangelogWithLLMInteractive(gitContext, newVersion, repoPath string, skipPrompt bool) (*LLMChangelogResult, error) {
	// Prompt for rules file editing
	if err := promptForRulesEdit(repoPath, skipPrompt); err != nil {
		fmt.Printf("Warning: Failed to handle rules file: %v\n", err)
		// Continue anyway - rules file is optional
	}
	// Determine the model to use
	model := "gemini-1.5-flash-latest" // Default model

	// Try to load model from grove.yml configuration
	coreCfg, err := config.LoadFrom(repoPath)
	if err == nil {
		var llmCfg LLMConfig
		if err := coreCfg.UnmarshalExtension("llm", &llmCfg); err == nil && llmCfg.DefaultModel != "" {
			model = llmCfg.DefaultModel
			// Only print to stdout if not in TUI mode (skipPrompt = true means TUI mode)
			if !skipPrompt {
				fmt.Printf("Using model from grove.yml: %s\n", model)
			}
		}
	}

	// Get current date for the changelog entry
	currentDate := time.Now().Format("2006-01-02")

	// Construct the NEW prompt asking for a JSON response
	prompt := fmt.Sprintf(`You are a technical writer responsible for creating release notes and suggesting semantic version bumps.
Based on the provided git log and diff stat, analyze the changes and generate a JSON object with three fields: "suggestion", "justification", and "changelog".

**JSON Schema:**
{
  "suggestion": "major|minor|patch",
  "justification": "A brief, one-sentence explanation for the version bump suggestion.",
  "changelog": "The full changelog in Markdown format."
}

**Instructions for Analysis:**
- **major:** Suggest if there are breaking changes (e.g., commits with "!" like "feat!:", or "BREAKING CHANGE:" in the body).
- **minor:** Suggest if new features are added without breaking changes (e.g., "feat:" commits).
- **patch:** Suggest for bug fixes, performance improvements, or chores (e.g., "fix:", "perf:", "chore:").

**Instructions for Changelog:**
1. Format the changelog in Markdown using the "Keep a Changelog" standard.
2. Start with a level 2 heading for the version, including the current date: "## %s (%s)"
3. Begin with a summary section that organizes changes into topic-based paragraphs. Each paragraph should focus on a related set of changes (e.g., one for UI improvements, one for new commands, one for bug fixes). Include commit hash citations in parentheses when referencing specific changes. 
   Example: "The new workspace list command (a1b2c3d) provides convenient access to all discovered workspaces, with support for JSON output (b2c3d4e) for scripting integration."
   Write one paragraph per major topic or theme. No emojis.
4. After the summary, categorize changes into sections (e.g., ### Features, ### Bug Fixes, ### BREAKING CHANGES). Omit empty sections. No emojis in section headers.
5. For each changelog entry, include the short commit hash (first 7 characters) directly at the end without brackets. Format: "Description of change (commit)"
   Example: "Add workspace list command with JSON output support (a1b2c3d)"
   GitHub will automatically convert these 7-character hashes into clickable links.
6. At the very end, include a "### File Changes" section with the provided git diff stat in a code block.
7. Do not include any preamble or explanation.
8. Do not use any emojis anywhere in the changelog.

**Context from Git:**
---
%s
---

Generate the JSON object now:`, newVersion, currentDate, gitContext)

	// Write prompt to a temporary file
	tmpFile, err := os.CreateTemp("", "grove-changelog-prompt-*.md")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary prompt file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(prompt); err != nil {
		return nil, fmt.Errorf("failed to write to temporary prompt file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temporary prompt file: %w", err)
	}

	// Create a temp file for the LLM response output
	outputFile, err := os.CreateTemp("", "grove-changelog-response-*.json")
	if err != nil {
		return nil, fmt.Errorf("failed to create output file: %w", err)
	}
	outputPath := outputFile.Name()
	outputFile.Close()
	defer os.Remove(outputPath)

	// Construct and execute the LLM command via grove llm (which delegates to gemapi)
	// Use --output flag to get clean response without console decoration
	// Use --max-output-tokens to ensure the full changelog can be generated
	args := []string{
		"llm",
		"request",
		"--model", model,
		"--file", tmpFile.Name(),
		"--output", outputPath,
		"--max-output-tokens", "8192",
		"--yes",
	}

	// Create a log file for gemapi console output
	logFile, err := os.CreateTemp("", "grove-gemapi-*.log")
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}
	logPath := logFile.Name()
	defer logFile.Close()

	// Only show this message if not in skip prompt mode (i.e., not in TUI)
	if !skipPrompt {
		fmt.Printf("Calling gemapi with model %s for version suggestion...\n", model)
		fmt.Printf("Logging output to: %s\n", logPath)
	}

	// Use 'grove llm' for workspace-awareness (delegates to gemapi for Gemini models)
	ctx := context.Background()
	changelogLog.Debug("Calling grove llm").
		Field("model", model).
		Field("repo_path", repoPath).
		Field("prompt_file", tmpFile.Name()).
		Field("output_file", outputPath).
		Field("full_command", "llm "+strings.Join(args[1:], " ")).
		Field("args", args).
		StructuredOnly().
		Log(ctx)

	// Verify temp file exists and has content
	if info, err := os.Stat(tmpFile.Name()); err != nil {
		changelogLog.Error("Temp file does not exist").
			Err(err).
			Field("path", tmpFile.Name()).
			StructuredOnly().
			Log(ctx)
	} else {
		changelogLog.Debug("Temp file verified").
			Field("path", tmpFile.Name()).
			Field("size", info.Size()).
			StructuredOnly().
			Log(ctx)
	}

	gemapiCmd := delegation.Command(args[0], args[1:]...)
	gemapiCmd.Dir = repoPath // Set working directory to the repository being analyzed

	// Redirect both stdout and stderr to log file to prevent TUI mangling
	gemapiCmd.Stdout = logFile
	gemapiCmd.Stderr = logFile

	err = gemapiCmd.Run()

	changelogLog.Debug("gemapi command completed").
		Field("has_error", err != nil).
		StructuredOnly().
		Log(ctx)

	if err != nil {
		// Read log file content for error details
		logContent, _ := os.ReadFile(logPath)
		changelogLog.Error("gemapi request failed").
			Err(err).
			Field("log_path", logPath).
			Field("log_content", string(logContent)).
			StructuredOnly().
			Log(ctx)
		return nil, fmt.Errorf("failed to execute 'gemapi request': %w\nLog: %s\nOutput: %s", err, logPath, string(logContent))
	}

	// Read the LLM response from the output file
	output, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read LLM response from output file: %w", err)
	}

	changelogLog.Debug("Read LLM response from file").
		Field("output_len", len(output)).
		StructuredOnly().
		Log(ctx)

	changelogLog.Debug("Raw gemapi output").
		Field("output", string(output)).
		StructuredOnly().
		Log(ctx)

	// Clean the output to ensure it's valid JSON
	// LLMs sometimes wrap JSON in ```json ... ``` blocks
	// The output may also contain console styling from gemapi, so we need to extract the JSON
	jsonString := extractJSONFromOutput(string(output))

	previewLen := len(jsonString)
	if previewLen > 200 {
		previewLen = 200
	}
	changelogLog.Debug("Cleaned JSON string").
		Field("json_len", len(jsonString)).
		Field("json_preview", jsonString[:previewLen]).
		StructuredOnly().
		Log(ctx)

	// Unmarshal the JSON response
	var result LLMChangelogResult
	if err := json.Unmarshal([]byte(jsonString), &result); err != nil {
		changelogLog.Error("Failed to unmarshal JSON").
			Err(err).
			Field("json_string", jsonString).
			StructuredOnly().
			Log(ctx)
		return nil, fmt.Errorf("failed to unmarshal LLM response into JSON: %w\nResponse:\n%s", err, string(output))
	}

	changelogLog.Info("Successfully parsed LLM response").
		Field("suggestion", result.Suggestion).
		Field("justification_len", len(result.Justification)).
		Field("changelog_len", len(result.Changelog)).
		StructuredOnly().
		Log(ctx)

	// Ensure the changelog ends with a newline
	if result.Changelog != "" && !strings.HasSuffix(result.Changelog, "\n") {
		result.Changelog = result.Changelog + "\n"
	}

	// Fix the version header - LLMs often ignore explicit version instructions
	// and hallucinate versions from the git context. Enforce the correct header.
	result.Changelog = fixChangelogHeader(result.Changelog, newVersion, currentDate)

	return &result, nil
}

// fixChangelogHeader ensures the changelog has the correct version and date in the header.
// LLMs frequently ignore explicit instructions and hallucinate version numbers from
// git history, so we deterministically fix the header after generation.
func fixChangelogHeader(changelog, version, date string) string {
	if changelog == "" {
		return changelog
	}

	lines := strings.SplitN(changelog, "\n", 2)
	if len(lines) == 0 {
		return changelog
	}

	// Check if first line is a version header (## v...)
	firstLine := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(firstLine, "## ") {
		// No header found, prepend the correct one
		correctHeader := fmt.Sprintf("## %s (%s)\n\n", version, date)
		return correctHeader + changelog
	}

	// Replace the existing header with the correct one
	correctHeader := fmt.Sprintf("## %s (%s)", version, date)
	if len(lines) == 2 {
		return correctHeader + "\n" + lines[1]
	}
	return correctHeader + "\n"
}

// extractJSONFromOutput extracts a JSON object from LLM output that may contain
// console styling, markdown code blocks, or other surrounding content.
// Handles nested code blocks within the JSON content.
func extractJSONFromOutput(output string) string {
	output = strings.TrimSpace(output)

	// Try to find JSON within ```json ... ``` code blocks first
	// The JSON content may contain nested ``` blocks (e.g., in changelog markdown),
	// so we need to find the closing ``` that's at the start of a line after the JSON object closes.
	if idx := strings.Index(output, "```json"); idx != -1 {
		start := idx + len("```json")
		rest := output[start:]

		// Look for the closing ``` by finding where the JSON object ends (})
		// and then finding the next ``` after that
		jsonStart := strings.Index(rest, "{")
		if jsonStart != -1 {
			// Find the end of the JSON object by tracking brace depth
			depth := 0
			inString := false
			escape := false
			jsonEnd := -1

			for i := jsonStart; i < len(rest); i++ {
				c := rest[i]
				if escape {
					escape = false
					continue
				}
				if c == '\\' && inString {
					escape = true
					continue
				}
				if c == '"' {
					inString = !inString
					continue
				}
				if inString {
					continue
				}
				if c == '{' {
					depth++
				} else if c == '}' {
					depth--
					if depth == 0 {
						jsonEnd = i + 1
						break
					}
				}
			}

			if jsonEnd != -1 {
				return strings.TrimSpace(rest[jsonStart:jsonEnd])
			}
		}
	}

	// Try to find JSON within ``` ... ``` code blocks (without language specifier)
	if idx := strings.Index(output, "```\n{"); idx != -1 {
		start := idx + len("```\n")
		rest := output[start:]

		// Same logic: track brace depth to find the JSON object end
		depth := 0
		inString := false
		escape := false
		jsonEnd := -1

		for i := 0; i < len(rest); i++ {
			c := rest[i]
			if escape {
				escape = false
				continue
			}
			if c == '\\' && inString {
				escape = true
				continue
			}
			if c == '"' {
				inString = !inString
				continue
			}
			if inString {
				continue
			}
			if c == '{' {
				depth++
			} else if c == '}' {
				depth--
				if depth == 0 {
					jsonEnd = i + 1
					break
				}
			}
		}

		if jsonEnd != -1 {
			return strings.TrimSpace(rest[:jsonEnd])
		}
	}

	// Try to find raw JSON object by looking for { and tracking brace depth
	startIdx := strings.Index(output, "{")
	if startIdx == -1 {
		return output // No JSON object found, return as-is
	}

	// Track brace depth to find the matching closing }
	depth := 0
	inString := false
	escape := false

	for i := startIdx; i < len(output); i++ {
		c := output[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inString {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return strings.TrimSpace(output[startIdx : i+1])
			}
		}
	}

	return output // No matching } found
}