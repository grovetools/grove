//go:build windows

package cmd

import (
	"os"
	"os/exec"
)

// execTool runs the tool as a child and exits with its status. Windows has no
// exec-replace, so this is the closest equivalent: grove stays alive as a thin
// parent but the caller still sees the child's exit code.
func execTool(path string, argv []string, env []string) error {
	cmd := exec.Command(path, argv[1:]...) //nolint:gosec // path is resolved by resolveTool, not user input
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	os.Exit(0)
	return nil
}
