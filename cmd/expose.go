package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/pkg/paths"
	"github.com/spf13/cobra"

	"github.com/grovetools/grove/pkg/sdk"
)

// exposeDirEnv overrides the directory `grove expose` links into. The default
// is ~/.local/bin LITERALLY — that is the user's own global namespace, not a
// grove-owned dir, so it deliberately does not follow GROVE_HOME/XDG. The env
// var exists so tests (and unusual layouts) can point it somewhere disposable.
const exposeDirEnv = "GROVE_EXPOSE_DIR"

// exposeLedgerFile records name → tool for every exposure grove created. The
// symlinks themselves all point at the grove binary (argv[0] dispatch), so the
// LINK NAME is normally the whole mapping — `cx` dispatches to cx. The ledger
// is what makes `--as` work: nothing about a link named `gnb` says "nb".
//
// It is a hint, never a source of truth: a missing or corrupt ledger costs a
// custom-named exposure its dispatch (it simply behaves as `grove`), and
// `grove hide` still works, because that reads the link, not the ledger.
const exposeLedgerFile = "exposures.json"

// exposeDir returns the directory exposures are linked into.
func exposeDir() (string, error) {
	if dir := os.Getenv(exposeDirEnv); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate your home directory: %w", err)
	}
	return filepath.Join(home, ".local", "bin"), nil
}

// exposeLedgerPath sits next to sdk's aliases.json in the grove state dir.
func exposeLedgerPath() string {
	return filepath.Join(paths.StateDir(), exposeLedgerFile)
}

type exposeLedger struct {
	Exposures map[string]string `json:"exposures"`
}

func loadExposures() map[string]string {
	ledger := exposeLedger{Exposures: map[string]string{}}
	data, err := os.ReadFile(exposeLedgerPath())
	if err != nil {
		return ledger.Exposures
	}
	if err := json.Unmarshal(data, &ledger); err != nil || ledger.Exposures == nil {
		return map[string]string{}
	}
	return ledger.Exposures
}

func saveExposures(exposures map[string]string) error {
	stateDir := paths.StateDir()
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}
	data, err := json.MarshalIndent(exposeLedger{Exposures: exposures}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(exposeLedgerPath(), data, 0o600)
}

// exposeLinkTarget is what an exposure symlink points at.
//
// paths.BinDir()/grove is preferred because the reconciler MAINTAINS that path:
// it repoints BinDir/<tool> at whichever version is active
// (pkg/reconciler/reconciler.go), so an exposure created today keeps working
// across every `grove update` and dev-link switch without being relinked. Only
// when that stable path does not exist yet (a grove running from a build tree,
// or before the first reconcile) do we fall back to this process's own binary,
// which pins the exposure to one build.
func exposeLinkTarget() (string, error) {
	if binDir := paths.BinDir(); binDir != "" {
		stable := filepath.Join(binDir, "grove")
		if _, err := os.Stat(stable); err == nil {
			return stable, nil
		}
	}
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot locate the grove binary: %w", err)
	}
	return self, nil
}

// globalGroveLink is the path of the ONE name the install puts in the user's
// global namespace: ~/.local/bin/grove. Everything else in the toolchain is
// reached as `grove <tool>`, or opted back in one at a time with `grove expose`.
func globalGroveLink() (string, error) {
	dir, err := exposeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "grove"), nil
}

// ensureGroveLink makes ~/.local/bin/grove resolve to the grove binary, and
// reports whether it had to change anything.
//
// It links at the same stable target exposures use (paths.BinDir()/grove, the
// path the reconciler maintains), so the hub link survives every update. It is
// refusal-first for the same reason `grove expose` is: ~/.local/bin is the
// USER's directory, so anything already sitting at the name that is not our
// link is reported and left alone, never replaced.
func ensureGroveLink() (linkPath string, changed bool, err error) {
	dir, err := exposeDir()
	if err != nil {
		return "", false, err
	}
	linkPath = filepath.Join(dir, "grove")

	target, err := exposeLinkTarget()
	if err != nil {
		return linkPath, false, err
	}
	// A grove that IS ~/.local/bin/grove needs no link to itself.
	if filepath.Clean(linkPath) == filepath.Clean(target) {
		return linkPath, false, nil
	}

	if _, statErr := os.Lstat(linkPath); statErr == nil {
		dest, isGrove := pointsAtGrove(linkPath)
		if !isGrove {
			what := "a file"
			if dest != "" {
				what = "a symlink to " + dest
			}
			return linkPath, false, fmt.Errorf("%s already exists (%s) and does not resolve to grove's managed binary — leave it, or repoint it at %s yourself", linkPath, what, target)
		}
		if resolvePath(dest) == resolvePath(target) {
			return linkPath, false, nil
		}
		if err := replaceSymlink(target, linkPath); err != nil {
			return linkPath, false, err
		}
		return linkPath, true, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return linkPath, false, fmt.Errorf("failed to create %s: %w", dir, err)
	}
	if err := replaceSymlink(target, linkPath); err != nil {
		return linkPath, false, err
	}
	return linkPath, true, nil
}

// groveReachable answers the only PATH question the hub model asks: can the
// user type `grove`? Either the name resolves on PATH, or the hub link exists
// and points at something runnable (a shell that has not been restarted yet
// still counts — the setup is correct).
func groveReachable() (string, bool) {
	if path, err := exec.LookPath("grove"); err == nil {
		return path, true
	}
	link, err := globalGroveLink()
	if err != nil {
		return "", false
	}
	// Stat follows the symlink: a link pointing at nothing is not reachable.
	if info, err := os.Stat(link); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
		return link, true
	}
	return "", false
}

// groveReachableHint is the remedy every "grove is not on your PATH" message
// prints, so the installer, `grove install`, `grove bootstrap` and the doctor
// all name the same fix.
func groveReachableHint() []string {
	dir := filepath.Join("~", ".local", "bin")
	if d, err := exposeDir(); err == nil {
		dir = d
	}
	target := "the grove binary"
	if binDir := paths.BinDir(); binDir != "" {
		target = filepath.Join(binDir, "grove")
	}
	return []string{
		fmt.Sprintf("  ln -s %s %s", target, filepath.Join(dir, "grove")),
		fmt.Sprintf("  export PATH=\"%s:$PATH\"   # bash/zsh", dir),
		fmt.Sprintf("  fish_add_path %s           # fish", dir),
		"  (or re-run 'grove onboard', which does both)",
	}
}

// nameExposed reports whether name currently dispatches through grove from the
// expose dir — used to avoid telling users to expose something already exposed.
func nameExposed(name string) bool {
	dir, err := exposeDir()
	if err != nil {
		return false
	}
	_, ok := pointsAtGrove(filepath.Join(dir, name))
	return ok
}

// groveIdentities are the paths an exposure symlink is allowed to resolve to,
// fully resolved. `grove hide` removes a link only if it lands on one of them.
func groveIdentities() []string {
	var out []string
	add := func(path string) {
		if path == "" {
			return
		}
		out = append(out, resolvePath(path))
	}
	if binDir := paths.BinDir(); binDir != "" {
		add(filepath.Join(binDir, "grove"))
	}
	if self, err := os.Executable(); err == nil {
		add(self)
	}
	return out
}

// resolvePath resolves symlinks where it can and falls back to a cleaned
// absolute path when it cannot (a broken link still has a comparable target).
func resolvePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

// linkDestination returns the (absolute, uncleaned) target of a symlink.
func linkDestination(linkPath string) (string, bool) {
	info, err := os.Lstat(linkPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	dest, err := os.Readlink(linkPath)
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(filepath.Dir(linkPath), dest)
	}
	return dest, true
}

// pointsAtGrove reports whether linkPath is a symlink resolving to the grove
// binary, and returns the raw destination for error messages.
func pointsAtGrove(linkPath string) (dest string, ok bool) {
	dest, isLink := linkDestination(linkPath)
	if !isLink {
		return "", false
	}
	resolved := resolvePath(dest)
	for _, identity := range groveIdentities() {
		if identity == resolved {
			return dest, true
		}
	}
	return dest, false
}

// probePATHCollision reports the first executable named name on pathEnv,
// ignoring skipDirs. The skip list is what keeps the probe honest: the expose
// dir itself and grove's private toolchain dir hold names grove put there, and
// finding grove's own `cx` is not a collision with anything.
//
// This walks PATH by hand rather than calling exec.LookPath because LookPath
// reads the process environment and cannot be told to ignore directories;
// the executable test is the same one (a non-directory with an exec bit).
func probePATHCollision(name, pathEnv string, skipDirs []string) string {
	if name == "" {
		return ""
	}
	skip := make(map[string]bool, len(skipDirs))
	for _, dir := range skipDirs {
		if dir == "" {
			continue
		}
		skip[filepath.Clean(dir)] = true
		skip[resolvePath(dir)] = true
	}

	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		if skip[filepath.Clean(dir)] || skip[resolvePath(dir)] {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		return candidate
	}
	return ""
}

// validExposeName rejects anything that is not a bare command name — an
// exposure is a file created in the user's global bin dir, so a name carrying
// a path separator would write outside it.
func validExposeName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("exposure name cannot be empty")
	case name == "." || name == "..":
		return fmt.Errorf("invalid exposure name %q", name)
	case strings.ContainsRune(name, os.PathSeparator), strings.ContainsRune(name, '/'):
		return fmt.Errorf("exposure name %q must be a bare command name, not a path", name)
	}
	return nil
}

func newExposeCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("expose [tool]", "Put a tool's bare name in your global PATH")
	cmd.Long = `Expose a Grove tool under its own name in ~/.local/bin.

grove is the only name the install puts in your global namespace; the rest of the toolchain lives in grove's private bin dir and is reached as 'grove <tool>'.

'grove expose' opts one tool back into the global namespace by symlinking ~/.local/bin/<name> at the grove binary — grove notices it was invoked under a tool's name and delegates, so the exposure survives upgrades without relinking.

With no arguments it lists the exposures you already have.`
	cmd.Example = `  # Bare 'cx' anywhere
  grove expose cx

  # 'nb' is taken by another tool — expose grove's under a different name
  grove expose nb --as gnb

  # List current exposures
  grove expose

  # Undo
  grove hide cx`
	cmd.Args = cobra.MaximumNArgs(1)
	cmd.SilenceUsage = true

	var asName string
	var force bool
	cmd.Flags().StringVar(&asName, "as", "", "Expose under a different name")
	cmd.Flags().BoolVar(&force, "force", false, "Link even when the name already resolves elsewhere on PATH")

	cmd.RunE = func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			return runExposeList()
		}
		return runExpose(args[0], asName, force)
	}
	return cmd
}

func newHideCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("hide <name>", "Remove a tool's bare name from your global PATH")
	cmd.Long = `Remove an exposure created by 'grove expose'.

Only a symlink resolving to the grove binary is removed. Anything else in ~/.local/bin — a real binary, a link you made yourself — is left alone and reported, because grove does not own that directory.`
	cmd.Example = `  grove hide cx`
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		return runHide(args[0])
	}
	return cmd
}

func runExpose(tool, asName string, force bool) error {
	repoName, _, alias, found := sdk.FindTool(tool)
	if !found {
		return fmt.Errorf("unknown tool: %s. Run 'grove list' to see the tools grove can expose.", tool)
	}
	binName := alias
	if binName == "" {
		binName = repoName
	}

	linkName := asName
	if linkName == "" {
		linkName = binName
	}
	if err := validExposeName(linkName); err != nil {
		return err
	}

	dir, err := exposeDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}
	linkPath := filepath.Join(dir, linkName)

	target, err := exposeLinkTarget()
	if err != nil {
		return err
	}

	if _, err := os.Lstat(linkPath); err == nil {
		dest, isGrove := pointsAtGrove(linkPath)
		if !isGrove {
			what := "a file"
			if dest != "" {
				what = "a symlink to " + dest
			}
			return fmt.Errorf("%s already exists (%s) and grove did not create it — remove it yourself, or pick another name: grove expose %s --as <name>", linkPath, what, tool)
		}
		// Already ours. Repoint only if it is pinned somewhere less stable
		// than the target we would choose today.
		if resolvePath(dest) != resolvePath(target) {
			if err := replaceSymlink(target, linkPath); err != nil {
				return err
			}
			fmt.Printf("Repointed %s -> %s\n", linkPath, target)
		} else {
			fmt.Printf("%s is already exposed as %s\n", binName, linkName)
		}
		return recordExposure(linkName, binName)
	}

	if !force {
		if conflict := probePATHCollision(linkName, os.Getenv("PATH"), []string{dir, paths.BinDir()}); conflict != "" {
			return fmt.Errorf("%q already resolves to %s on your PATH — expose grove's under another name (grove expose %s --as g%s) or override with --force", linkName, conflict, tool, linkName)
		}
	}

	if err := replaceSymlink(target, linkPath); err != nil {
		return err
	}
	if err := recordExposure(linkName, binName); err != nil {
		return err
	}

	fmt.Printf("Exposed %s as %s\n", binName, linkPath)
	if binDir := paths.BinDir(); binDir != "" {
		if _, err := os.Stat(filepath.Join(binDir, binName)); err != nil {
			fmt.Printf("Note: %s is not installed yet — run 'grove install %s'\n", binName, tool)
		}
	}
	return nil
}

// replaceSymlink creates linkPath -> target, atomically replacing whatever
// symlink is there. The temp-then-rename keeps a live exposure from having a
// window where the name resolves to nothing.
func replaceSymlink(target, linkPath string) error {
	tmp := linkPath + ".grove-expose.tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("failed to create symlink %s: %w", linkPath, err)
	}
	if err := os.Rename(tmp, linkPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to create symlink %s: %w", linkPath, err)
	}
	return nil
}

func recordExposure(linkName, binName string) error {
	exposures := loadExposures()
	if exposures[linkName] == binName {
		return nil
	}
	exposures[linkName] = binName
	if err := saveExposures(exposures); err != nil {
		// The ledger is a hint; a link that already exists still dispatches
		// under its own name. Warn rather than fail the exposure.
		fmt.Fprintf(os.Stderr, "Warning: could not record exposure: %v\n", err)
	}
	return nil
}

func runHide(name string) error {
	if err := validExposeName(name); err != nil {
		return err
	}
	dir, err := exposeDir()
	if err != nil {
		return err
	}
	linkPath := filepath.Join(dir, name)

	if _, err := os.Lstat(linkPath); err != nil {
		return fmt.Errorf("nothing named %s in %s — run 'grove expose' to list exposures", name, dir)
	}
	dest, isGrove := pointsAtGrove(linkPath)
	if !isGrove {
		if dest == "" {
			return fmt.Errorf("%s is not a symlink — grove did not create it and will not remove it", linkPath)
		}
		return fmt.Errorf("%s points at %s, not the grove binary — grove did not create it and will not remove it", linkPath, dest)
	}
	if err := os.Remove(linkPath); err != nil {
		return fmt.Errorf("failed to remove %s: %w", linkPath, err)
	}

	exposures := loadExposures()
	if _, ok := exposures[name]; ok {
		delete(exposures, name)
		if err := saveExposures(exposures); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not update the exposure ledger: %v\n", err)
		}
	}
	fmt.Printf("Removed %s\n", linkPath)
	return nil
}

func runExposeList() error {
	dir, err := exposeDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("No exposures (%s does not exist).\nRun 'grove expose <tool>' to put a tool's bare name on your PATH.\n", dir)
			return nil
		}
		return fmt.Errorf("failed to read %s: %w", dir, err)
	}

	exposures := loadExposures()
	type row struct{ name, tool string }
	var rows []row
	width := 0
	for _, entry := range entries {
		name := entry.Name()
		if _, ok := pointsAtGrove(filepath.Join(dir, name)); !ok {
			continue
		}
		rows = append(rows, row{name, exposedToolFor(name, exposures)})
		if len(name) > width {
			width = len(name)
		}
	}
	if len(rows) == 0 {
		fmt.Printf("No exposures in %s.\nRun 'grove expose <tool>' to put a tool's bare name on your PATH.\n", dir)
		return nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

	fmt.Printf("Exposures in %s:\n", dir)
	for _, r := range rows {
		fmt.Printf("  %-*s -> %s\n", width, r.name, r.tool)
	}
	return nil
}

// exposedToolFor answers what a link named name actually runs — the same
// question argv[0] dispatch asks, so the listing can never claim a dispatch
// that would not happen.
func exposedToolFor(name string, exposures map[string]string) string {
	if tool, ok := argv0Tool(name, exposures, registryBinary); ok {
		return tool
	}
	return "grove"
}
