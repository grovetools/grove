package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/conventional"
	"github.com/spf13/cobra"
)

var (
	useLLM bool
)

func newChangelogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "changelog <repo-path>",
		Short:  "Generate a changelog for a repository",
		Hidden: true, // Internal command used by release workflow
		Long: `Generates a changelog entry for a repository and prepends it to CHANGELOG.md.

By default, it generates the changelog from conventional commits.
With the --llm flag, it uses an LLM to generate the changelog based on the git history since the last tag.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoPath := args[0]
			newVersion, _ := cmd.Flags().GetString("version")

			if useLLM {
				fmt.Println("Generating changelog with LLM...")
				return runLLMChangelog(repoPath, newVersion)
			}

			// 1. Get last tag
			tagCmd := exec.Command("git", "describe", "--tags", "--abbrev=0")
			tagCmd.Dir = repoPath
			lastTagBytes, err := tagCmd.Output()
			lastTag := "HEAD"
			if err == nil {
				lastTag = strings.TrimSpace(string(lastTagBytes))
			}

			// 2. Get commits since last tag
			logCmd := exec.Command("git", "log", fmt.Sprintf("%s..HEAD", lastTag), "--pretty=format:%B%x00")
			logCmd.Dir = repoPath
			logBytes, err := logCmd.Output()
			if err != nil {
				return fmt.Errorf("failed to get git log: %w", err)
			}

			// Split commits by null byte
			commitMessages := strings.Split(string(logBytes), "\x00")

			// 3. Parse commits
			var conventionalCommits []*conventional.Commit
			for _, msg := range commitMessages {
				msg = strings.TrimSpace(msg)
				if msg == "" {
					continue
				}
				if c, err := conventional.Parse(msg); err == nil {
					conventionalCommits = append(conventionalCommits, c)
				}
			}

			if len(conventionalCommits) == 0 {
				fmt.Println("No conventional commits found since last tag. No changelog generated.")
				return nil
			}

			// 4. Generate changelog content
			changelogContent := conventional.Generate(newVersion, conventionalCommits)

			// 5. Prepend to CHANGELOG.md
			changelogPath := filepath.Join(repoPath, "CHANGELOG.md")
			existingContent, _ := os.ReadFile(changelogPath)
			newContent := changelogContent + string(existingContent)

			return os.WriteFile(changelogPath, []byte(newContent), 0644)
		},
	}
	cmd.Flags().String("version", "v0.0.0", "The new version for the changelog header")
	cmd.Flags().BoolVar(&useLLM, "llm", false, "Generate changelog using an LLM")
	return cmd
}
