package cmd

import (
	"bufio"
	"bytes"
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
)

func init() { rootCmd.AddCommand(newMigrateCmd()) }

func newMigrateCmd() *cobra.Command {
	var dryRun, yes bool
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
			return runLegacyMigrate(cmd.OutOrStdout(), cmd.InOrStdin(), dryRun, yes, time.Now().UTC())
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the migration diff without writing")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Apply after printing the diff without prompting")
	return cmd
}

func runLegacyMigrate(out io.Writer, in io.Reader, dryRun, yes bool, now time.Time) error {
	m, err := collectLegacyMigration()
	if err != nil {
		return err
	}
	current, err := coderoot.Load()
	if err != nil {
		return err
	}
	roots := make(map[string]coderoot.Root, len(m.Roots))
	for name, c := range m.Roots {
		roots[name] = c.Root
	}
	legacyTargets := migrationSourceTargets(m.Sources)
	changed := !reflect.DeepEqual(roots, current.Roots) || !reflect.DeepEqual(m.Notebooks, current.Notebooks) || m.Default != current.Default || len(legacyTargets) > 0
	if !changed {
		fmt.Fprintln(out, "Legacy configuration is already migrated; nothing to do.")
		return nil
	}

	nbBytes, rootsBytes, err := buildMigrationCandidates(m, roots)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Migration step 1 plan:")
	for _, name := range sortedLegacyRootNames(m.Roots) {
		c := m.Roots[name]
		fmt.Fprintf(out, "  %-10s %-20s %s  [%s]\n", c.Kind, name, c.Root.Path, c.Source)
	}
	if err := printMigrationDiff(out, m.NotebooksPath, nbBytes); err != nil {
		return err
	}
	if err := printMigrationDiff(out, m.RootsPath, rootsBytes); err != nil {
		return err
	}
	for _, p := range legacyTargets {
		s := sourceForTarget(m.Sources, p)
		updated, err := removeLegacyDeclarations(p, s.RemoveGlobal, s.RemoveMachine)
		if err != nil {
			return err
		}
		if err := printMigrationDiff(out, p, updated); err != nil {
			return err
		}
	}
	if dryRun {
		fmt.Fprintln(out, "\n--dry-run: nothing was written.")
		return nil
	}
	if !yes {
		fmt.Fprint(out, "\nApply this migration? [y/N] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			return fmt.Errorf("migration not confirmed; no files were changed (re-run with --yes after reviewing the diff)")
		}
	}

	backupPaths := append([]string{}, legacyTargets...)
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
	for _, p := range backupPaths {
		if err := backupMigrationFile(p, stamp); err != nil {
			return err
		}
		fmt.Fprintf(out, "backup: %s.%s.bak\n", p, stamp)
	}

	// Ordering is intentional and part of the migration contract.
	def := m.Default
	nbChanged, err := config.WriteNotebooks(m.NotebooksPath, config.NotebookEdits{Default: &def, Upserts: m.Notebooks, Header: []string{"# Recorded notebook roots. Written by `grove migrate`.", ""}})
	if err != nil {
		return err
	}
	rootChanged, err := config.WriteCodeRoots(m.RootsPath, config.CodeRootEdits{Upserts: roots, Header: []string{"# Recorded code roots. Written by `grove migrate`.", ""}})
	if err != nil {
		return err
	}

	for _, p := range legacyTargets {
		s := sourceForTarget(m.Sources, p)
		updated, err := removeLegacyDeclarations(p, s.RemoveGlobal, s.RemoveMachine)
		if err != nil {
			return err
		}
		if err := atomicMigrationReplace(p, updated); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "\nApplied migration: notebooks.toml=%t, roots.toml=%t; removed legacy declarations from %d file(s).\n", nbChanged, rootChanged, len(legacyTargets))
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
		data, readErr := os.ReadFile(from)
		if readErr == nil {
			if writeErr := os.WriteFile(to, data, 0o600); writeErr != nil {
				return nil, nil, writeErr
			}
		} else if !os.IsNotExist(readErr) {
			return nil, nil, readErr
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

func sourceForTarget(sources []legacySource, target string) legacySource {
	var out legacySource
	for _, s := range sources {
		p := s.Resolved
		if p == "" {
			p = s.Path
		}
		if p == target {
			out.Path = target
			out.RemoveGlobal = out.RemoveGlobal || s.RemoveGlobal
			out.RemoveMachine = out.RemoveMachine || s.RemoveMachine
		}
	}
	return out
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
	if err := os.WriteFile(backup, data, mode); err != nil {
		return fmt.Errorf("backup %s: %w", path, err)
	}
	return nil
}

func atomicMigrationReplace(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
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
	if err := os.Chmod(name, info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return nil
}
