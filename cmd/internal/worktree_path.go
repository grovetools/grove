package internal

import (
	"fmt"
	"path/filepath"

	"github.com/grovetools/core/pkg/workspace"
	"github.com/spf13/cobra"
)

// newWorktreePathCmd is the plumbing verb behind `grove satellite worktree
// push/pull`: it answers "where does (or would) the worktree named --name of
// the repository/ecosystem rooted at --git-root live on THIS machine?".
//
// It exists because the XDG worktree layout embeds
// pathutil.WorktreeID(gitRoot) — a sanitized basename plus a hash of the
// symlink-/case-normalized absolute path — which only the machine that owns
// the filesystem can compute faithfully. A remote caller (the laptop-side
// satellite worktree verbs) invokes this on the target machine instead of
// duplicating the normalization + sanitizer logic in generated shell.
//
// Contract (machine-readable):
//   - stdout: exactly one absolute path, newline-terminated, nothing else;
//   - an EXISTING worktree resolves via workspace.FindWorktreePath (legacy
//     base first, then XDG), so callers converge on wherever the worktree
//     already lives;
//   - otherwise the NEW-worktree location under the XDG layout:
//     paths.WorktreesDir()/DirIdentifier(gitRoot)/<name>;
//   - errors exit nonzero with nothing on stdout.
func newWorktreePathCmd() *cobra.Command {
	var gitRoot, name string
	cmd := &cobra.Command{
		Use:          "worktree-path --git-root <abs-path> --name <worktree>",
		Short:        "Print the resolved worktree path for a git root + worktree name (plumbing)",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !filepath.IsAbs(gitRoot) {
				return fmt.Errorf("--git-root must be an absolute path, got %q", gitRoot)
			}
			if name == "" {
				return fmt.Errorf("--name must not be empty")
			}
			if p, ok := workspace.FindWorktreePath(gitRoot, name); ok {
				fmt.Fprintln(cmd.OutOrStdout(), p)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), workspace.ResolveNewWorktreePath(gitRoot, name, true))
			return nil
		},
	}
	cmd.Flags().StringVar(&gitRoot, "git-root", "", "Absolute path of the owning repository/ecosystem root (required)")
	cmd.Flags().StringVar(&name, "name", "", "Worktree name (required)")
	_ = cmd.MarkFlagRequired("git-root")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}
