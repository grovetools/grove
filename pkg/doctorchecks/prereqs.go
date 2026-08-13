package doctorchecks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/core/pkg/doctor"
	"github.com/grovetools/core/pkg/paths"
)

func init() {
	doctor.Register(&tmuxCheck{})
	doctor.Register(&claudeCLICheck{})
	doctor.Register(&groveOnPathCheck{})
	doctor.Register(&groveGlobalSymlinkCheck{})
	doctor.Register(&grovedBinaryCheck{})
}

// tmuxCheck verifies tmux is on PATH and reports its version.
type tmuxCheck struct{}

func (c *tmuxCheck) ID() string   { return "tmux_installed" }
func (c *tmuxCheck) Name() string { return "tmux available on PATH" }

func (c *tmuxCheck) Run(ctx context.Context, opts doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}

	path, err := exec.LookPath("tmux")
	if err != nil {
		res.Status = doctor.StatusFail
		res.Message = "tmux not found on PATH; treemux sessions cannot start"
		res.Resolution = "install tmux: brew install tmux (or apt install tmux)"
		return res
	}

	version := runForFirstLine(ctx, path, "-V")
	if version == "" {
		version = "tmux (version unknown)"
	}
	res.Status = doctor.StatusOK
	res.Message = fmt.Sprintf("%s at %s", version, path)
	return res
}

func (c *tmuxCheck) AutoFix(ctx context.Context) error {
	return fmt.Errorf("%w: install tmux manually — brew install tmux", doctor.ErrNotFixable)
}

// claudeCLICheck verifies the claude CLI is on PATH.
type claudeCLICheck struct{}

func (c *claudeCLICheck) ID() string   { return "claude_cli" }
func (c *claudeCLICheck) Name() string { return "claude CLI available on PATH" }

func (c *claudeCLICheck) Run(ctx context.Context, opts doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}

	path, err := exec.LookPath("claude")
	if err != nil {
		res.Status = doctor.StatusWarn
		res.Message = "claude CLI not found on PATH; agent sessions will not work"
		res.Resolution = "install Claude Code: npm install -g @anthropic-ai/claude-code (or see https://claude.com/claude-code)"
		return res
	}

	res.Status = doctor.StatusOK
	res.Message = fmt.Sprintf("claude CLI at %s", path)
	return res
}

func (c *claudeCLICheck) AutoFix(ctx context.Context) error {
	return fmt.Errorf("%w: install the claude CLI manually", doctor.ErrNotFixable)
}

// exposeDirEnv mirrors cmd's override so tests (and unusual layouts) can point
// the global bin dir somewhere disposable. Kept in sync by name, not by import:
// cmd depends on this package, not the other way round.
const exposeDirEnv = "GROVE_EXPOSE_DIR"

// globalBinDir is the USER's global bin dir — ~/.local/bin literally. It is not
// a grove-owned directory, so it deliberately does not follow GROVE_HOME/XDG.
func globalBinDir() string {
	if dir := os.Getenv(exposeDirEnv); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "bin")
}

// groveOnPathCheck verifies the one name the install owns is reachable.
//
// grove's toolchain dir is deliberately NOT checked against PATH: tools run as
// `grove <tool>`, so that directory being absent from PATH is the expected
// state, not a problem. What matters is that `grove` itself resolves.
type groveOnPathCheck struct{}

func (c *groveOnPathCheck) ID() string   { return "grove_on_path" }
func (c *groveOnPathCheck) Name() string { return "grove reachable by name" }

func (c *groveOnPathCheck) Run(ctx context.Context, opts doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}

	if path, err := exec.LookPath("grove"); err == nil {
		res.Status = doctor.StatusOK
		res.Message = fmt.Sprintf("grove resolves to %s", path)
		return res
	}

	// Not on THIS process's PATH, but a valid hub link means the setup is
	// right and only this shell is stale (a fresh install, not yet sourced).
	dir := globalBinDir()
	if dir != "" {
		link := filepath.Join(dir, "grove")
		if info, err := os.Stat(link); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			res.Status = doctor.StatusOK
			res.Message = fmt.Sprintf("%s exists but %s is not on this shell's PATH", link, dir)
			res.Resolution = fmt.Sprintf("add 'export PATH=\"%s:$PATH\"' to your shell profile, then restart your shell", dir)
			return res
		}
	}

	if dir == "" {
		dir = filepath.Join("~", ".local", "bin")
	}
	managed := "the grove binary"
	if binDir := paths.BinDir(); binDir != "" {
		managed = filepath.Join(binDir, "grove")
	}
	res.Status = doctor.StatusFail
	res.Message = fmt.Sprintf("grove is not on PATH and %s is not a usable link", filepath.Join(dir, "grove"))
	res.Resolution = fmt.Sprintf("link it and put the directory on PATH: ln -s %s %s && export PATH=\"%s:$PATH\" (or re-run 'grove onboard')",
		managed, filepath.Join(dir, "grove"), dir)
	return res
}

func (c *groveOnPathCheck) AutoFix(ctx context.Context) error {
	return fmt.Errorf("%w: PATH changes must be made in your shell profile", doctor.ErrNotFixable)
}

// groveGlobalSymlinkCheck verifies the hub link ~/.local/bin/grove points at
// the CURRENT toolchain. A link pinned to an old build (or to some other grove
// entirely) keeps working while quietly running the wrong binary, which is
// exactly the failure a version-mismatch hunt never suspects.
type groveGlobalSymlinkCheck struct{}

func (c *groveGlobalSymlinkCheck) ID() string   { return "grove_global_symlink" }
func (c *groveGlobalSymlinkCheck) Name() string { return "~/.local/bin/grove links the managed binary" }

func (c *groveGlobalSymlinkCheck) Run(ctx context.Context, opts doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}

	dir := globalBinDir()
	if dir == "" {
		res.Status = doctor.StatusWarn
		res.Message = "could not resolve ~/.local/bin (no home directory?)"
		return res
	}
	link := filepath.Join(dir, "grove")
	managed := ""
	if binDir := paths.BinDir(); binDir != "" {
		managed = filepath.Join(binDir, "grove")
	}

	info, err := os.Lstat(link)
	if err != nil {
		res.Status = doctor.StatusWarn
		res.Message = fmt.Sprintf("no %s — grove is reachable only however you installed it", link)
		if managed != "" {
			res.Resolution = fmt.Sprintf("ln -s %s %s (or re-run 'grove onboard')", managed, link)
		}
		return res
	}
	if info.Mode()&os.ModeSymlink == 0 {
		res.Status = doctor.StatusWarn
		res.Message = fmt.Sprintf("%s is a regular file, not a link to the managed binary — updates will not reach it", link)
		if managed != "" {
			res.Resolution = fmt.Sprintf("replace it yourself: ln -sf %s %s", managed, link)
		}
		return res
	}

	dest, err := os.Readlink(link)
	if err == nil && !filepath.IsAbs(dest) {
		dest = filepath.Join(dir, dest)
	}
	if _, statErr := os.Stat(link); statErr != nil {
		res.Status = doctor.StatusFail
		res.Message = fmt.Sprintf("%s is a broken symlink (points at %s)", link, dest)
		if managed != "" {
			res.Resolution = fmt.Sprintf("repoint it: ln -sf %s %s", managed, link)
		}
		return res
	}
	if managed != "" && !samePath(dest, managed) {
		res.Status = doctor.StatusWarn
		res.Message = fmt.Sprintf("%s points at %s, not the managed binary %s", link, dest, managed)
		res.Resolution = fmt.Sprintf("repoint it so updates take effect: ln -sf %s %s", managed, link)
		return res
	}

	res.Status = doctor.StatusOK
	res.Message = fmt.Sprintf("%s -> %s", link, dest)
	return res
}

func (c *groveGlobalSymlinkCheck) AutoFix(ctx context.Context) error {
	return fmt.Errorf("%w: ~/.local/bin is yours — link or repoint grove there yourself", doctor.ErrNotFixable)
}

// grovedBinaryCheck verifies the groved daemon binary is resolvable and
// queries its version. It never starts a daemon: `groved version` only prints
// build info and exits.
type grovedBinaryCheck struct{}

func (c *grovedBinaryCheck) ID() string   { return "groved_binary" }
func (c *grovedBinaryCheck) Name() string { return "groved binary resolvable" }

func (c *grovedBinaryCheck) Run(ctx context.Context, opts doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}

	var path string
	if binDir := paths.BinDir(); binDir != "" {
		candidate := filepath.Join(binDir, "groved")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			path = candidate
		}
	}
	if path == "" {
		if p, err := exec.LookPath("groved"); err == nil {
			path = p
		}
	}
	if path == "" {
		res.Status = doctor.StatusFail
		res.Message = fmt.Sprintf("groved binary not found in %s or on PATH", paths.BinDir())
		res.Resolution = "run 'grove install' (or the grove installer) to install groved"
		return res
	}

	// Query the version without starting a daemon: `groved version` is a
	// plain print-and-exit subcommand; fall back to `groved --version`.
	version := runForFirstLine(ctx, path, "version")
	if version == "" {
		version = runForFirstLine(ctx, path, "--version")
	}
	if version == "" {
		res.Status = doctor.StatusWarn
		res.Message = fmt.Sprintf("groved found at %s but the version query failed", path)
		res.Resolution = "the binary may be corrupt or stale; reinstall with 'grove install'"
		return res
	}

	version = strings.TrimSpace(strings.TrimPrefix(version, "Version:"))
	res.Status = doctor.StatusOK
	res.Message = fmt.Sprintf("groved %s at %s", version, path)
	return res
}

func (c *grovedBinaryCheck) AutoFix(ctx context.Context) error {
	return fmt.Errorf("%w: install groved via 'grove install'", doctor.ErrNotFixable)
}

// runForFirstLine runs a binary with a short timeout and returns the first
// line of its stdout, or "" on any error.
func runForFirstLine(ctx context.Context, path string, args ...string) string {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, path, args...).Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(line)
}

// samePath reports whether two paths refer to the same directory, tolerating
// trailing slashes and symlinks.
func samePath(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ca == cb {
		return true
	}
	ra, errA := filepath.EvalSymlinks(ca)
	rb, errB := filepath.EvalSymlinks(cb)
	if errA != nil || errB != nil {
		return false
	}
	return ra == rb
}
