package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/spf13/cobra"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/transition"
)

func init() { rootCmd.AddCommand(newMigrateCmd()) }

func newMigrateCmd() *cobra.Command {
	var dryRun, yes, stageSync, jsonOutput bool
	var evidenceDir string
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate frozen legacy configuration to recorded roots and notebooks",
		Long: `Perform migration step 1: convert legacy groves, search paths, machine
subscriptions, and notebook definitions into roots.toml and notebooks.toml.

The command parses the old formats with a private frozen parser, prints a
deterministic diff, and changes nothing until confirmed. Existing files are
backed up with a UTC timestamp. notebooks.toml is always written before
roots.toml so a notebook reference is never recorded before its definition.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLegacyMigrateWithOptions(cmd.OutOrStdout(), cmd.InOrStdin(), migrationOptions{DryRun: dryRun, Yes: yes, StageSync: stageSync, JSON: jsonOutput, EvidenceDir: evidenceDir}, time.Now().UTC())
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the migration diff without writing")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Apply after printing the diff without prompting")
	cmd.Flags().BoolVar(&stageSync, "stage-sync", false, "Park legacy sync intent in sync.toml.p2-staged for the later P2 migration")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Render final transition evidence as JSON")
	cmd.Flags().StringVar(&evidenceDir, "evidence-dir", "", "Write normalized before/after effective topology evidence")
	return cmd
}

type migrationOptions struct {
	DryRun, Yes, StageSync, JSON bool
	EvidenceDir                  string
}

// migrationFailureHook is a test-only seam invoked after every state write.
// Production leaves it nil. Returning an error exercises the rollback stack.
var migrationFailureHook func(string) error

// runMigrationFailureHook also exposes the same seam to the checked-in
// config-lab, which must exercise rollback through the real built binary. The
// two-variable opt-in keeps this inert in every normal invocation.
func runMigrationFailureHook(path string) error {
	if migrationFailureHook != nil {
		if err := migrationFailureHook(path); err != nil {
			return err
		}
	}
	if os.Getenv("GROVE_CONFIGLAB") == "1" {
		failAfter := os.Getenv("GROVE_CONFIGLAB_FAIL_AFTER")
		if failAfter != "" && (failAfter == path || failAfter == filepath.Base(path)) {
			return fmt.Errorf("config-lab injected failure after %s", path)
		}
	}
	return nil
}

func runLegacyMigrate(out io.Writer, in io.Reader, dryRun, yes bool, now time.Time) error {
	return runLegacyMigrateWithOptions(out, in, migrationOptions{DryRun: dryRun, Yes: yes}, now)
}

func runLegacyMigrateWithOptions(out io.Writer, in io.Reader, opts migrationOptions, now time.Time) error {
	// Freeze the old loader's effective result before collection or candidate
	// construction. This independent replay is the left-hand side of the proof.
	legacyEffective, err := loadFrozenLegacyEffectiveConfig()
	if err != nil {
		return fmt.Errorf("load frozen pre-migration effective topology: %w", err)
	}
	expectedMap, err := configAsEffectiveMap(legacyEffective)
	if err != nil {
		return err
	}
	expected, err := canonicalEffectiveTopology(expectedMap)
	if err != nil {
		return err
	}
	m, err := collectLegacyMigrationWithOptions(opts.StageSync)
	if err != nil {
		return err
	}
	current, err := loadModernCanonicalForMigration(filepath.Dir(m.RootsPath))
	if err != nil {
		return err
	}
	roots := make(map[string]coderoot.Root, len(m.Roots))
	for name, c := range m.Roots {
		roots[name] = c.Root
	}
	targets := migrationSourceTargets(m.Sources)
	changed := !reflect.DeepEqual(roots, current.Roots) || !reflect.DeepEqual(m.Notebooks, current.Notebooks) || m.Default != current.Default || len(targets) > 0 || len(m.Compatibility) > 0
	if !changed {
		evidence := transition.Evidence{Action: "grove migrate step 1", Reason: "legacy configuration is already migrated; effective configuration is unchanged"}
		return renderMigrationEvidence(out, evidence, opts.JSON)
	}

	nbBytes, rootsBytes, err := buildMigrationCandidates(m, roots)
	if err != nil {
		return err
	}
	updates := map[string][]byte{m.NotebooksPath: nbBytes, m.RootsPath: rootsBytes}
	for p, data := range m.Compatibility {
		updates[p] = data
	}
	for _, p := range targets {
		s, err := sourceForTarget(m.Sources, p)
		if err != nil {
			return err
		}
		if s.ReplaceCanonical {
			// buildMigrationCandidates already populated this canonical path;
			// replacing the collision is the migration, not a surgical strip.
			continue
		}
		updates[p], err = removeLegacyDeclarations(s)
		if err != nil {
			return err
		}
	}
	if m.SyncStagePath != "" {
		updates[m.SyncStagePath], err = os.ReadFile(m.SyncPath)
		if err != nil {
			return err
		}
	}

	ordered := migrationWriteOrder(m, targets)
	if !opts.JSON {
		fmt.Fprintln(out, "Migration step 1 plan:")
		for _, name := range sortedLegacyRootNames(m.Roots) {
			c := m.Roots[name]
			fmt.Fprintf(out, "  %-10s %-20s %s  [%s]\n", c.Kind, name, c.Root.Path, c.Source)
		}
		for _, p := range ordered {
			if err := printMigrationDiff(out, p, updates[p]); err != nil {
				return err
			}
		}
		if m.SyncStagePath != "" {
			fmt.Fprintf(out, "\n  sync staging: preserve byte-identical P2 input at %s\n", m.SyncStagePath)
		}
	}
	if opts.DryRun {
		evidence := transition.Evidence{Action: "grove migrate step 1 dry-run", Reason: "preview completed; no files were changed"}
		return renderMigrationEvidence(out, evidence, opts.JSON)
	}
	if opts.JSON && !opts.Yes {
		return fmt.Errorf("--json apply requires --yes (use --dry-run --json for a non-mutating preview)")
	}
	if !opts.Yes {
		fmt.Fprint(out, "\nApply this migration? [y/N] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			return fmt.Errorf("migration not confirmed; no files were changed (re-run with --yes after reviewing the diff)")
		}
	}

	backupPaths := append([]string{}, targets...)
	for _, p := range []string{m.NotebooksPath, m.RootsPath} {
		if _, err := os.Stat(p); err == nil {
			backupPaths = append(backupPaths, p)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	sort.Strings(backupPaths)
	backupPaths = uniqueStrings(backupPaths)
	stamp := now.UTC().Format("20060102T150405Z")
	allChanged := append([]string{}, ordered...)
	for _, p := range backupPaths {
		allChanged = append(allChanged, p+"."+stamp+".bak")
	}
	txn, err := newMigrationRollbackStack(uniqueSortedStrings(allChanged))
	if err != nil {
		return err
	}
	fail := func(step string, cause error) error {
		if rollbackErr := txn.Rollback(); rollbackErr != nil {
			return fmt.Errorf("migration failed after %s: %v; rollback also failed: %w", step, cause, rollbackErr)
		}
		config.ResetLoadCache()
		return fmt.Errorf("migration failed after %s; restored every changed file byte-for-byte: %w", step, cause)
	}
	for _, p := range backupPaths {
		if err := backupMigrationFile(p, stamp); err != nil {
			return fail("backup "+p, err)
		}
		backup := p + "." + stamp + ".bak"
		if err := runMigrationFailureHook(backup); err != nil {
			return fail("backup "+p, err)
		}
		if !opts.JSON {
			fmt.Fprintf(out, "backup: %s.%s.bak\n", p, stamp)
		}
	}
	for _, p := range ordered {
		if err := atomicMigrationWrite(p, updates[p]); err != nil {
			return fail("write "+p, err)
		}
		if err := runMigrationFailureHook(p); err != nil {
			return fail("write "+p, err)
		}
	}

	config.ResetLoadCache()
	if _, err := coderoot.Load(); err != nil {
		return fail("final recorded-pair reload", err)
	}
	// Migration equivalence is canonicalized at the global config directory,
	// not the caller's arbitrary project cwd. This is the same config-show
	// representation without an unrelated project overlay replacing topology.
	envelope, err := loadEffectiveConfig(filepath.Dir(m.RootsPath))
	if err != nil {
		return fail("final effective-config reload", err)
	}
	actual, err := canonicalEffectiveTopology(envelope.Effective)
	if err != nil {
		return fail("final effective-config normalization", err)
	}
	if !bytes.Equal(expected, actual) {
		return fail("effective-config equivalence check", fmt.Errorf("canonical pre/post mismatch:\n%s", effectiveDiff(expected, actual)))
	}
	if opts.EvidenceDir != "" {
		if err := writeMigrationEquivalenceEvidence(opts.EvidenceDir, expected, actual); err != nil {
			return fail("write equivalence evidence", err)
		}
	}
	txn.Commit()

	evidence := transition.Evidence{Action: "grove migrate step 1", Counts: []transition.Count{
		{Name: "files_changed", Value: int64(len(ordered))},
		{Name: "notebooks_recorded", Value: int64(len(m.Notebooks))},
		{Name: "roots_recorded", Value: int64(len(roots))},
	}}
	for _, name := range sortedLegacyRootNames(m.Roots) {
		declared := m.Roots[name].Root.Path
		resolved := expandPath(declared)
		if abs, err := filepath.Abs(resolved); err == nil {
			resolved = abs
		}
		if real, err := filepath.EvalSymlinks(resolved); err == nil {
			resolved = real
		}
		evidence.ResolvedRoots = append(evidence.ResolvedRoots, transition.ResolvedRoot{Name: name, Declared: declared, Resolved: resolved})
	}
	if !opts.JSON {
		fmt.Fprintln(out, "\nEffective configuration equivalence: verified (canonical diff empty).")
	}
	return renderMigrationEvidence(out, evidence, opts.JSON)
}

func renderMigrationEvidence(out io.Writer, evidence transition.Evidence, jsonOutput bool) error {
	if jsonOutput {
		return transition.RenderJSON(out, evidence)
	}
	return transition.RenderHuman(out, evidence)
}

func writeMigrationEquivalenceEvidence(dir string, before, after []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create evidence directory %s: %w", dir, err)
	}
	for name, raw := range map[string][]byte{
		"before-effective.json": before,
		"after-effective.json":  after,
	} {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err != nil {
			return fmt.Errorf("format %s: %w", name, err)
		}
		pretty.WriteByte('\n')
		if err := os.WriteFile(filepath.Join(dir, name), pretty.Bytes(), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return os.WriteFile(filepath.Join(dir, "effective-equivalence.diff"), nil, 0o600)
}

func migrationWriteOrder(m *legacyMigration, targets []string) []string {
	// Compatibility declarations displaced by a canonical-name collision land
	// before that file is rewritten. The recorded pair remains ordered
	// notebooks then roots. A sync staging copy is durable before the live sync
	// file is stripped. All remaining source/card edits are deterministic.
	compat := make([]string, 0, len(m.Compatibility))
	for p := range m.Compatibility {
		compat = append(compat, p)
	}
	sort.Strings(compat)
	out := append(compat, m.NotebooksPath, m.RootsPath)
	if m.SyncStagePath != "" {
		out = append(out, m.SyncStagePath)
	}
	out = append(out, targets...)
	return uniqueStringsInOrder(out)
}

func uniqueStringsInOrder(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, value := range in {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func uniqueSortedStrings(in []string) []string {
	sort.Strings(in)
	return uniqueStrings(in)
}

// canonicalEffectiveTopology projects the complete author-visible topology
// emitted by config-show. Source attribution and runtime-only metadata are not
// in that representation. Every path/root_dir is expanded, made absolute and
// cleaned; existing symlinks are resolved, while missing paths retain the
// deterministic cleaned absolute spelling.
func canonicalEffectiveTopology(effective map[string]interface{}) ([]byte, error) {
	out := map[string]interface{}{
		"groves":    map[string]interface{}{},
		"notebooks": map[string]interface{}{},
	}
	for _, key := range []string{"groves", "notebooks"} {
		if value, exists := effective[key]; exists && value != nil {
			out[key] = normalizeEffectiveTopologyValue(value, "")
		}
	}
	return json.Marshal(out)
}

func normalizeEffectiveTopologyValue(value interface{}, key string) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for childKey, child := range typed {
			out[childKey] = normalizeEffectiveTopologyValue(child, childKey)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, child := range typed {
			out[i] = normalizeEffectiveTopologyValue(child, key)
		}
		return out
	case string:
		if key == "path" || key == "root_dir" || key == "notebook_root" {
			path := expandPath(typed)
			if abs, err := filepath.Abs(path); err == nil {
				path = abs
			}
			path = filepath.Clean(path)
			if real, err := filepath.EvalSymlinks(path); err == nil {
				path = real
			}
			return path
		}
	}
	return value
}

func effectiveDiff(before, after []byte) string {
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A: difflib.SplitLines(string(before)), B: difflib.SplitLines(string(after)),
		FromFile: "legacy-effective.json", ToFile: "recorded-effective.json", Context: 3,
	})
	if err != nil {
		return fmt.Sprintf("before=%s after=%s", before, after)
	}
	return diff
}

type migrationSnapshot struct {
	path   string
	data   []byte
	mode   os.FileMode
	exists bool
}

type migrationRollbackStack struct {
	snapshots []migrationSnapshot
	committed bool
}

func newMigrationRollbackStack(paths []string) (*migrationRollbackStack, error) {
	stack := &migrationRollbackStack{}
	for _, path := range paths {
		snapshot := migrationSnapshot{path: path, mode: 0o600}
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			stack.snapshots = append(stack.snapshots, snapshot)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("snapshot %s: target is not a regular file", path)
		}
		snapshot.data, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", path, err)
		}
		snapshot.exists, snapshot.mode = true, info.Mode().Perm()
		stack.snapshots = append(stack.snapshots, snapshot)
	}
	return stack, nil
}

func (s *migrationRollbackStack) Commit() { s.committed = true }

func (s *migrationRollbackStack) Rollback() error {
	if s.committed {
		return nil
	}
	var failures []string
	for i := len(s.snapshots) - 1; i >= 0; i-- {
		snapshot := s.snapshots[i]
		if !snapshot.exists {
			if err := os.Remove(snapshot.path); err != nil && !os.IsNotExist(err) {
				failures = append(failures, fmt.Sprintf("remove %s: %v", snapshot.path, err))
			}
			continue
		}
		if err := atomicMigrationWriteMode(snapshot.path, snapshot.data, snapshot.mode); err != nil {
			failures = append(failures, fmt.Sprintf("restore %s: %v", snapshot.path, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func buildMigrationCandidates(m *legacyMigration, roots map[string]coderoot.Root) ([]byte, []byte, error) {
	dir, err := os.MkdirTemp("", "grove-migrate-preview-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	nbPath, rootsPath := filepath.Join(dir, coderoot.NotebooksFileName), filepath.Join(dir, coderoot.RootsFileName)
	for from, to := range map[string]string{m.NotebooksPath: nbPath, m.RootsPath: rootsPath} {
		kind := filepath.Base(from)
		shape, _, classifyErr := classifyCanonicalFile(from, kind)
		if classifyErr != nil {
			return nil, nil, classifyErr
		}
		if shape != canonicalModern {
			continue
		}
		data, readErr := os.ReadFile(from)
		if readErr != nil {
			return nil, nil, readErr
		}
		if writeErr := os.WriteFile(to, data, 0o600); writeErr != nil {
			return nil, nil, writeErr
		}
	}
	def := m.Default
	if _, err := config.WriteNotebooks(nbPath, config.NotebookEdits{Default: &def, Upserts: m.Notebooks, Header: []string{"# Recorded notebook roots. Written by `grove migrate`.", ""}}); err != nil {
		return nil, nil, err
	}
	if _, err := config.WriteCodeRoots(rootsPath, config.CodeRootEdits{Upserts: roots, Header: []string{"# Recorded code roots. Written by `grove migrate`.", ""}}); err != nil {
		return nil, nil, err
	}
	nb, err := os.ReadFile(nbPath)
	if err != nil {
		return nil, nil, err
	}
	rf, err := os.ReadFile(rootsPath)
	if err != nil {
		return nil, nil, err
	}
	return nb, rf, nil
}

func sortedLegacyRootNames(m map[string]legacyRootCandidate) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sourceForTarget(sources []legacySource, target string) (legacySource, error) {
	out := legacySource{Path: target, Resolved: target}
	found := false
	for _, s := range sources {
		p := s.Resolved
		if p == "" {
			p = s.Path
		}
		if p != target || (!s.RemoveGlobal && !s.RemoveMachine && !s.RemoveSync && !s.AnnotateCard && !s.ReplaceCanonical) {
			continue
		}
		if found && out.TOML != s.TOML {
			return legacySource{}, fmt.Errorf("legacy aliases for %s disagree on TOML/YAML dialect; remove the conflicting alias before migrating", target)
		}
		if !found {
			out.TOML = s.TOML
			found = true
		}
		out.RemoveGlobal = out.RemoveGlobal || s.RemoveGlobal
		out.RemoveMachine = out.RemoveMachine || s.RemoveMachine
		out.RemoveSync = out.RemoveSync || s.RemoveSync
		out.AnnotateCard = out.AnnotateCard || s.AnnotateCard
		out.ReplaceCanonical = out.ReplaceCanonical || s.ReplaceCanonical
	}
	if !found {
		return legacySource{}, fmt.Errorf("internal migration error: no source for %s", target)
	}
	return out, nil
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := []string{in[0]}
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

func printMigrationDiff(out io.Writer, path string, after []byte) error {
	before, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		before = nil
	} else if err != nil {
		return err
	}
	if bytes.Equal(before, after) {
		return nil
	}
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{A: difflib.SplitLines(string(before)), B: difflib.SplitLines(string(after)), FromFile: path, ToFile: path + " (migrated)", Context: 3})
	if err != nil {
		return err
	}
	fmt.Fprintln(out)
	fmt.Fprint(out, diff)
	return nil
}

func backupMigrationFile(path, stamp string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("backup %s: %w", path, err)
	}
	backup := path + "." + stamp + ".bak"
	info, err := os.Stat(path)
	mode := os.FileMode(0o600)
	if err == nil {
		mode = info.Mode().Perm()
	}
	f, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("backup %s (refusing to overwrite an existing backup): %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("backup %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("backup %s: %w", path, err)
	}
	return nil
}

func atomicMigrationReplace(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return atomicMigrationWriteMode(path, data, info.Mode().Perm())
}

func atomicMigrationWrite(path string, data []byte) error {
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	return atomicMigrationWriteMode(path, data, mode)
}

func atomicMigrationWriteMode(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".migrate-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return nil
}
