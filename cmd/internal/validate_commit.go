package internal

import (
	"fmt"
	"os"

	"github.com/grovetools/core/conventional"
	"github.com/spf13/cobra"
)

func NewInternalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "internal",
		Short:  "Internal commands for Grove tooling",
		Hidden: true,
	}

	cmd.AddCommand(newValidateCommitMsgCmd())
	return cmd
}

func newValidateCommitMsgCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate-commit-msg <path-to-commit-msg-file>",
		Short: "Validate a commit message follows conventional commit format",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			commitMsgFile := args[0]
			msgBytes, err := os.ReadFile(commitMsgFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Could not read commit message file: %v\n", err)
				return err
			}

			_, err = conventional.Parse(string(msgBytes))
			if err != nil {
				fmt.Fprintln(os.Stderr, "--------------------------------------------------")
				fmt.Fprintln(os.Stderr, "INVALID COMMIT MESSAGE")
				fmt.Fprintln(os.Stderr, "--------------------------------------------------")
				fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
				fmt.Fprintln(os.Stderr, "Your commit message does not follow the Conventional Commits format.")
				fmt.Fprintln(os.Stderr, "Please use a format like: type(scope): subject")
				fmt.Fprintln(os.Stderr, "Example: feat(api): add new user endpoint")
				fmt.Fprintln(os.Stderr, "Allowed types: feat, fix, chore, docs, style, refactor, perf, test, build, ci")
				fmt.Fprintln(os.Stderr, "--------------------------------------------------")
				return err
			}

			return nil
		},
	}

	return cmd
}
