package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/config"
)

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
	// Get detailed commit log
	logCmd := exec.Command("git", "log", commitRange, "--pretty=fuller")
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

	// Combine context
	context := fmt.Sprintf("GIT LOG:\n%s\n\nGIT DIFF STAT:\n%s", string(logOutput), string(diffOutput))

	// 3. Generate changelog with LLM.
	result, err := generateChangelogWithLLM(context, newVersion, repoPath)
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

// FlowConfig defines the structure for the 'flow' section in grove.yml.
type FlowConfig struct {
	OneshotModel string `yaml:"oneshot_model"`
}

// generateChangelogWithLLM constructs a prompt and calls gemapi to generate the changelog.
// It now returns the new LLMChangelogResult struct.
func generateChangelogWithLLM(context, newVersion, repoPath string) (*LLMChangelogResult, error) {
	// Determine the model to use
	model := "gemini-1.5-flash-latest" // Default model

	// Try to load model from grove.yml configuration
	coreCfg, err := config.LoadFrom(repoPath)
	if err == nil {
		var flowCfg FlowConfig
		if err := coreCfg.UnmarshalExtension("flow", &flowCfg); err == nil && flowCfg.OneshotModel != "" {
			model = flowCfg.OneshotModel
			fmt.Printf("Using model from grove.yml: %s\n", model)
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
3. Categorize changes into sections (e.g., ### Features, ### Bug Fixes, ### 💥 BREAKING CHANGES). Omit empty sections.
4. At the very end, include a "### File Changes" section with the provided git diff stat in a code block.
5. Do not include any preamble or explanation.

**Context from Git:**
---
%s
---

Generate the JSON object now:`, newVersion, currentDate, context)

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

	// Construct and execute the gemapi command
	args := []string{
		"request",
		"--model", model,
		"--file", tmpFile.Name(),
		"--yes",
	}
	
	fmt.Printf("Calling gemapi with model %s for version suggestion...\n", model)
	gemapiCmd := exec.Command("gemapi", args...)
	gemapiCmd.Stderr = os.Stderr // Pipe stderr for progress visibility

	output, err := gemapiCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to execute 'gemapi request': %w", err)
	}
	
	// Clean the output to ensure it's valid JSON
	// LLMs sometimes wrap JSON in ```json ... ``` blocks
	jsonString := strings.TrimSpace(string(output))
	if strings.HasPrefix(jsonString, "```json") {
		jsonString = strings.TrimPrefix(jsonString, "```json")
		jsonString = strings.TrimSuffix(jsonString, "```")
		jsonString = strings.TrimSpace(jsonString)
	}

	// Unmarshal the JSON response
	var result LLMChangelogResult
	if err := json.Unmarshal([]byte(jsonString), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal LLM response into JSON: %w\nResponse:\n%s", err, string(output))
	}

	// Ensure the changelog ends with a newline
	if result.Changelog != "" && !strings.HasSuffix(result.Changelog, "\n") {
		result.Changelog = result.Changelog + "\n"
	}

	return &result, nil
}