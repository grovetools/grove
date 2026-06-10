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
	doctor.Register(&binDirOnPathCheck{})
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

// binDirOnPathCheck verifies the grove bin dir exists and is on PATH.
type binDirOnPathCheck struct{}

func (c *binDirOnPathCheck) ID() string   { return "grove_bin_on_path" }
func (c *binDirOnPathCheck) Name() string { return "grove bin dir present on PATH" }

func (c *binDirOnPathCheck) Run(ctx context.Context, opts doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}

	binDir := paths.BinDir()
	if binDir == "" {
		res.Status = doctor.StatusWarn
		res.Message = "could not resolve the grove bin dir (no home directory?)"
		return res
	}

	if info, err := os.Stat(binDir); err != nil || !info.IsDir() {
		res.Status = doctor.StatusFail
		res.Message = fmt.Sprintf("grove bin dir %s does not exist", binDir)
		res.Resolution = "re-run the grove installer (or 'grove install') to create and populate it"
		return res
	}

	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == "" {
			continue
		}
		if samePath(p, binDir) {
			res.Status = doctor.StatusOK
			res.Message = fmt.Sprintf("grove bin dir %s is on PATH", binDir)
			return res
		}
	}

	res.Status = doctor.StatusFail
	res.Message = fmt.Sprintf("grove bin dir %s is not on PATH", binDir)
	res.Resolution = fmt.Sprintf("add 'export PATH=\"%s:$PATH\"' to your shell profile", binDir)
	return res
}

func (c *binDirOnPathCheck) AutoFix(ctx context.Context) error {
	return fmt.Errorf("%w: PATH changes must be made in your shell profile", doctor.ErrNotFixable)
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
