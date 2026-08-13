//go:build !windows

package cmd

import "syscall"

// execTool REPLACES this process with the tool. A full-screen TUI wants the
// terminal to itself: with syscall.Exec there is no grove left in the middle to
// forward signals, own the process group, or confuse the child's job control —
// the shell's foreground process simply becomes treemux.
//
// It only returns on failure; on success control never comes back.
func execTool(path string, argv []string, env []string) error {
	return syscall.Exec(path, argv, env)
}
