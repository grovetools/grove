package plugin

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/pkg/paths"

	"github.com/grovetools/grove/pkg/build"
)

// The "Build" and "Install" stages. Both reuse what grove already has:
// grove/pkg/build's job runner is the same code path `grove build` drives, and
// the versions/<pin>/bin + BinDir() symlink layout is grove/pkg/sdk's
// managed-binary convention, so an installed panel sits next to every other
// grove-managed binary instead of somewhere new.

// runBuild executes a plugin's build command in its checkout, streaming output
// to out. An empty command is a no-op by design: a plugin that ships an
// interpreted program (a shell panel, a Python panel) needs no toolchain, and
// making the build step optional is what keeps that true.
func runBuild(ctx context.Context, name, dir string, argv []string, out io.Writer) error {
	if len(argv) == 0 {
		return nil
	}
	// ExtraPathDirs puts grove's managed bin dir in front of PATH so a plugin
	// build can use tools grove installed, exactly like `grove build` does.
	opts := &build.RunOptions{}
	if binDir := paths.BinDir(); binDir != "" {
		opts.ExtraPathDirs = []string{binDir}
	}
	jobs := []build.BuildJob{{Name: name, Path: dir, Command: argv}}
	for ev := range build.RunWithEventsAndOptions(ctx, jobs, 1, false, opts) {
		switch ev.Type {
		case "output":
			fmt.Fprintf(out, "    %s\n", ev.OutputLine)
		case "finish":
			if ev.Result != nil && ev.Result.Err != nil {
				return fmt.Errorf("build failed: %w", ev.Result.Err)
			}
		}
	}
	return nil
}

// installBinary copies the built artifact into the per-commit version
// directory and points the grove bin dir entry at it. The copy is what makes
// the pin real: the checkout can be re-checked-out at another commit and the
// installed binary still is the one that was approved.
func installBinary(builtPath, versionBinPath, binPath string) error {
	info, err := os.Stat(builtPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("the build did not produce %s", builtPath)
		}
		return fmt.Errorf("stat %s: %w", builtPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("build.binary %s is a directory", builtPath)
	}

	if err := os.MkdirAll(filepath.Dir(versionBinPath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(versionBinPath), err)
	}
	data, err := os.ReadFile(builtPath) //nolint:gosec // G304: path is inside grove's managed checkout
	if err != nil {
		return fmt.Errorf("read %s: %w", builtPath, err)
	}
	// Remove first: the destination may be a running binary from a previous
	// install, and unlink-then-create is what lets a replacement land while
	// the old one is still executing.
	_ = os.Remove(versionBinPath)
	if err := os.WriteFile(versionBinPath, data, 0o755); err != nil { //nolint:gosec // G306: this is an executable
		return fmt.Errorf("write %s: %w", versionBinPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(binPath), err)
	}
	_ = os.Remove(binPath)
	if err := os.Symlink(versionBinPath, binPath); err != nil {
		return fmt.Errorf("link %s -> %s: %w", binPath, versionBinPath, err)
	}
	return nil
}

// claimBinPath refuses to install over a bin dir entry grove did not create
// for this plugin. Without it, a plugin whose binary is named "grove" or "nb"
// would replace a tool the user depends on, which is a far bigger event than
// installing a panel.
func claimBinPath(binPath string, pin *Pin) error {
	info, err := os.Lstat(binPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", binPath, err)
	}
	if pin != nil && pin.Binary == binPath {
		return nil // ours already: a reinstall or an update
	}
	kind := "a file"
	if info.Mode()&os.ModeSymlink != 0 {
		if target, err := os.Readlink(binPath); err == nil {
			kind = "a link to " + target
		} else {
			kind = "a link"
		}
	}
	return fmt.Errorf("%s already exists (%s) and was not installed by `grove plugin` — rename the plugin's binary or remove that entry first", binPath, kind)
}

// removeBinary undoes installBinary for one plugin, leaving anything it does
// not own alone.
func removeBinary(pin *Pin, versionsDir string) []string {
	var removed []string
	if pin.Binary != "" {
		if target, err := os.Readlink(pin.Binary); err == nil && strings.HasPrefix(target, versionsDir+string(filepath.Separator)) {
			if os.Remove(pin.Binary) == nil {
				removed = append(removed, pin.Binary)
			}
		}
	}
	if versionsDir != "" {
		if err := os.RemoveAll(versionsDir); err == nil {
			removed = append(removed, versionsDir)
		}
	}
	return removed
}
