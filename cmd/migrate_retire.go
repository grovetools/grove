package cmd

// --retire removes explicitly named notespaces together with their machine
// identity records: the [primaries] entry for the stamped subject and every
// [subjects] mint recorded for the notespace path. The directory is only half
// the record — deleting it alone would strand primaries and mints pointing at
// nothing — so both sides move in one guarded transaction, exactly as
// --resubject moves them.
//
// The verb is built for sweeping shadow skeletons and test residue, so it
// refuses by default to retire a notespace holding content beyond the plan
// scaffold (plans/*/rules/** and the stamp itself); --allow-content overrides
// per run. Every retired byte is inlined into the manifest, and undo is
// hash-guarded: it refuses when any affected path changed after the apply.

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/paths"
)

const (
	retireManifestKind = "retire"
	// retireMaxBytes bounds what one notespace may inline into the undo
	// manifest. Anything larger is not residue and deserves a hand archive.
	retireMaxBytes = 4 << 20
)

type retireOptions struct {
	DryRun, Yes, Undo, JSON, AllowContent bool
	ManifestPath                          string
	Targets                               []string
}

type retireChange struct {
	Notebook string `json:"notebook"`
	Name     string `json:"name"`
	Root     string `json:"root"`
	// ID/Subject/Kind are empty when the directory carries no stamp at all —
	// residue minted by a write path that bypassed materialization.
	ID      string `json:"id,omitempty"`
	Subject string `json:"subject,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Files   int    `json:"files"`
	Bytes   int64  `json:"bytes"`
	// Dirs preserves empty directories that file restoration alone would lose.
	Dirs               []string `json:"dirs,omitempty"`
	RemovedPrimary     bool     `json:"removed_primary,omitempty"`
	RemovedSubjectKeys []string `json:"removed_subject_keys,omitempty"`
	filePaths          []string
}

type retireManifest struct {
	Version   int            `json:"version"`
	Kind      string         `json:"kind"`
	CreatedAt time.Time      `json:"created_at"`
	State     string         `json:"state"`
	Journal   string         `json:"journal"`
	Changes   []retireChange `json:"changes"`
	Skipped   []string       `json:"skipped,omitempty"`
	Warnings  []string       `json:"warnings,omitempty"`
	Files     []p2FileBackup `json:"files,omitempty"`
}

func runRetireMigration(out io.Writer, in io.Reader, opts retireOptions, now time.Time) error {
	if opts.Undo {
		return undoRetire(out, opts)
	}
	if len(opts.Targets) == 0 {
		return fmt.Errorf("--retire requires at least one --notespace notebook/name target")
	}
	if opts.ManifestPath == "" {
		opts.ManifestPath = filepath.Join(paths.StateDir(), "migrations", "retire-"+now.UTC().Format("20060102T150405Z")+".json")
	}
	if !filepath.IsAbs(opts.ManifestPath) {
		return fmt.Errorf("--manifest must be absolute")
	}
	if err := ensureDaemonStopped(); err != nil {
		return err
	}
	manifest, err := planRetire(opts, now)
	if err != nil {
		return err
	}
	if !opts.JSON {
		fmt.Fprintln(out, "Retire plan (notespace directory plus machine identity records, together):")
		for _, change := range manifest.Changes {
			identity := "unstamped"
			if change.ID != "" {
				identity = fmt.Sprintf("id=%s subject=%s", change.ID, change.Subject)
			}
			fmt.Fprintf(out, "  retire  %s/%s %s files=%d bytes=%d primaries=%t subjects=%d\n", change.Notebook, change.Name, identity, change.Files, change.Bytes, change.RemovedPrimary, len(change.RemovedSubjectKeys))
		}
		for _, skip := range manifest.Skipped {
			fmt.Fprintf(out, "  keep    %s\n", skip)
		}
		for _, warning := range manifest.Warnings {
			fmt.Fprintf(out, "  WARNING: %s\n", warning)
		}
		fmt.Fprintf(out, "  changes=%d kept=%d\n", len(manifest.Changes), len(manifest.Skipped))
	}
	if len(manifest.Changes) == 0 {
		return renderRetireResult(out, opts.JSON, "grove migrate retire", "no named target is eligible; nothing to retire", manifest)
	}
	if opts.DryRun {
		return renderRetireResult(out, opts.JSON, "grove migrate retire dry-run", "plan produced explicit evidence; no files were changed", manifest)
	}
	if !opts.Yes {
		if opts.JSON {
			return fmt.Errorf("--json apply requires --yes")
		}
		fmt.Fprint(out, "Apply retire? [y/N] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
			return fmt.Errorf("retire not confirmed; no files were changed")
		}
	}

	backupPaths := []string{config.MachineConfigPath()}
	for _, change := range manifest.Changes {
		backupPaths = append(backupPaths, change.filePaths...)
	}
	for _, path := range uniqueSortedStrings(backupPaths) {
		backup, err := backupP2File(path)
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, backup)
	}
	manifest.State = "prepared"
	if err := writeRetireManifest(opts.ManifestPath, manifest); err != nil {
		return err
	}
	fail := func(step string, cause error) error {
		rollbackErr := rollbackRetire(manifest)
		manifest.State = "rolled-back"
		_ = writeRetireManifest(opts.ManifestPath, manifest)
		if rollbackErr != nil {
			return fmt.Errorf("retire failed after %s: %v; rollback also failed: %w", step, cause, rollbackErr)
		}
		return fmt.Errorf("retire failed after %s; every changed file was restored: %w", step, cause)
	}

	for _, change := range manifest.Changes {
		change := change
		if change.RemovedPrimary || len(change.RemovedSubjectKeys) > 0 {
			if _, _, err := config.EditMachineConfig(config.MachineConfigPath(), config.MachineEditOptions{}, func(cfg *config.MachineConfig) error {
				if change.RemovedPrimary {
					if got := cfg.Primaries[change.Subject]; got != change.ID {
						return fmt.Errorf("cannot retire: [primaries] %q is %q, want %q", change.Subject, got, change.ID)
					}
					delete(cfg.Primaries, change.Subject)
				}
				for _, key := range change.RemovedSubjectKeys {
					delete(cfg.Subjects, key)
				}
				return nil
			}); err != nil {
				return fail("machine identity "+change.Notebook+"/"+change.Name, err)
			}
		}
		if err := os.RemoveAll(change.Root); err != nil {
			return fail("remove "+change.Notebook+"/"+change.Name, err)
		}
	}
	for i := range manifest.Files {
		h, err := hashFileOrMissing(manifest.Files[i].Path)
		if err != nil {
			return fail("hash final file", err)
		}
		manifest.Files[i].AfterHash = h
	}
	manifest.State = "applied"
	if err := writeRetireManifest(opts.ManifestPath, manifest); err != nil {
		return err
	}
	return renderRetireResult(out, opts.JSON, "grove migrate retire", "notespace directories and machine identity records were removed together", manifest)
}

func planRetire(opts retireOptions, now time.Time) (*retireManifest, error) {
	table, err := coderoot.Load()
	if err != nil {
		return nil, fmt.Errorf("load recorded roots/notebooks: %w", err)
	}
	machine, err := config.LoadMachineConfig()
	if err != nil {
		return nil, fmt.Errorf("load machine identity: %w", err)
	}
	if machine == nil {
		machine = &config.MachineConfig{}
	}
	manifest := &retireManifest{Version: p2ManifestVersion, Kind: retireManifestKind, CreatedAt: now.UTC(), State: "planned", Journal: opts.ManifestPath}
	seen := map[string]bool{}
	for _, target := range opts.Targets {
		nbName, nsName, ok := strings.Cut(target, "/")
		if !ok || nbName == "" || nsName == "" || strings.Contains(nsName, "/") {
			return nil, fmt.Errorf("--notespace %q: want notebook/name", target)
		}
		if seen[target] {
			continue
		}
		seen[target] = true
		nbRoot := table.NotebookRoot(nbName)
		if nbRoot == "" {
			return nil, fmt.Errorf("--notespace %q: notebook %q is not recorded", target, nbName)
		}
		nsRoot := filepath.Join(nbRoot, "notespaces", nsName)
		info, err := os.Lstat(nsRoot)
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("--notespace %q: %s does not exist", target, nsRoot)
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("--notespace %q: %s is not a directory", target, nsRoot)
		}
		change := retireChange{Notebook: nbName, Name: nsName, Root: nsRoot}
		content, irregular, err := collectRetireTree(nsRoot, &change)
		if err != nil {
			return nil, err
		}
		if len(irregular) > 0 {
			manifest.Skipped = append(manifest.Skipped, fmt.Sprintf("%s: cannot snapshot non-regular entries (%s); archive by hand", target, strings.Join(irregular, ", ")))
			continue
		}
		if change.Bytes > retireMaxBytes {
			manifest.Skipped = append(manifest.Skipped, fmt.Sprintf("%s: %d bytes exceed the %d-byte undo-manifest bound; archive by hand", target, change.Bytes, int64(retireMaxBytes)))
			continue
		}
		if len(content) > 0 && !opts.AllowContent {
			preview := content
			if len(preview) > 3 {
				preview = preview[:3]
			}
			manifest.Skipped = append(manifest.Skipped, fmt.Sprintf("%s: holds content beyond the plan scaffold (%s); re-run with --allow-content after review", target, strings.Join(preview, ", ")))
			continue
		}
		stamp, err := notespace.LoadNotespace(nsRoot)
		if err != nil {
			return nil, err
		}
		if stamp != nil {
			if recorded, ok := machine.Primaries[stamp.Subject]; ok && recorded != stamp.ID {
				manifest.Skipped = append(manifest.Skipped, fmt.Sprintf("%s: [primaries] records %s -> %s, stamp id is %s; machine.toml and stamp disagree, resolve first", target, stamp.Subject, recorded, stamp.ID))
				continue
			} else if ok {
				change.RemovedPrimary = true
			}
			change.ID, change.Subject, change.Kind = stamp.ID, stamp.Subject, stamp.Kind
		}
		nsCanonical := canonicalPath(nsRoot)
		for recordedPath, value := range machine.Subjects {
			if (stamp != nil && value == stamp.Subject) || canonicalPath(recordedPath) == nsCanonical {
				change.RemovedSubjectKeys = append(change.RemovedSubjectKeys, recordedPath)
			}
		}
		sort.Strings(change.RemovedSubjectKeys)
		manifest.Warnings = append(manifest.Warnings, retireRegistryReferences(nbRoot, nbName, nsName)...)
		manifest.Changes = append(manifest.Changes, change)
	}
	return manifest, nil
}

// collectRetireTree walks one notespace, filling the change's file/dir
// inventory and returning the paths that make it more than a skeleton: every
// regular file other than the stamp and plan scaffold rules. Non-regular
// entries (symlinks, sockets) are returned separately — they cannot be
// inlined into an undo manifest, so their presence blocks the retire.
func collectRetireTree(nsRoot string, change *retireChange) (content, irregular []string, err error) {
	err = filepath.WalkDir(nsRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(nsRoot, path)
		if relErr != nil {
			return relErr
		}
		if entry.IsDir() {
			if rel != "." {
				change.Dirs = append(change.Dirs, path)
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			irregular = append(irregular, rel)
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		change.Files++
		change.Bytes += info.Size()
		change.filePaths = append(change.filePaths, path)
		parts := strings.Split(rel, string(filepath.Separator))
		scaffold := rel == notespace.NotespaceStampName || (len(parts) >= 4 && parts[0] == "plans" && parts[2] == "rules")
		if !scaffold {
			content = append(content, rel)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(change.Dirs)
	sort.Strings(change.filePaths)
	sort.Strings(content)
	sort.Strings(irregular)
	return content, irregular, nil
}

// retireRegistryReferences reports files in the sibling registry notespace
// that mention the retire target by name. The registry holds live fleet and
// ecosystem-card state that can reference other notespaces (repo-a/repo-b are
// the known instance), so a hit is surfaced for the operator rather than
// silently swept past.
func retireRegistryReferences(nbRoot, nbName, nsName string) []string {
	registryRoot := filepath.Join(nbRoot, "notespaces", "registry")
	if nsName == "registry" {
		return []string{fmt.Sprintf("%s/registry looks like notes but holds live fleet state; retire it only with independent evidence it is disused", nbName)}
	}
	var warnings []string
	_ = filepath.WalkDir(registryRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		if info, infoErr := entry.Info(); infoErr != nil || info.Size() > 2<<20 {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr == nil && strings.Contains(string(data), nsName) {
			warnings = append(warnings, fmt.Sprintf("%s/registry references %q in %s", nbName, nsName, path))
		}
		return nil
	})
	return warnings
}

func rollbackRetire(manifest *retireManifest) error {
	var errs []string
	for _, change := range manifest.Changes {
		for _, dir := range change.Dirs {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}
	for _, file := range manifest.Files {
		if !file.Existed {
			if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err.Error())
			}
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(file.BeforeB64)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if err := restoreP2File(file, decoded); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("retire rollback errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

func undoRetire(out io.Writer, opts retireOptions) error {
	if opts.ManifestPath == "" {
		return fmt.Errorf("--undo requires --manifest")
	}
	data, err := os.ReadFile(opts.ManifestPath)
	if err != nil {
		return err
	}
	var manifest retireManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if manifest.Kind != retireManifestKind || manifest.Version != p2ManifestVersion {
		return fmt.Errorf("manifest %s is not an undoable retire manifest (kind=%q version=%d)", opts.ManifestPath, manifest.Kind, manifest.Version)
	}
	if manifest.State == "undone" {
		return renderRetireResult(out, opts.JSON, "grove migrate retire undo", "manifest already undone; no local mutation needed", &manifest)
	}
	if manifest.State != "applied" {
		return fmt.Errorf("manifest state %q is not undoable", manifest.State)
	}
	for _, file := range manifest.Files {
		got, err := hashFileOrMissing(file.Path)
		if err != nil || got != file.AfterHash {
			return fmt.Errorf("refusing undo: %s changed after retire (want %s, got %s)", file.Path, file.AfterHash, got)
		}
	}
	if err := rollbackRetire(&manifest); err != nil {
		return err
	}
	manifest.State = "undone"
	if err := writeRetireManifest(opts.ManifestPath, &manifest); err != nil {
		return err
	}
	return renderRetireResult(out, opts.JSON, "grove migrate retire undo", "hash guards passed and directory/machine identity state was restored", &manifest)
}

func writeRetireManifest(path string, manifest *retireManifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return atomicMigrationWrite(path, append(data, '\n'))
}

func renderRetireResult(out io.Writer, asJSON bool, action, reason string, manifest *retireManifest) error {
	result := struct {
		Action, Reason, Manifest string
		Changes, Kept            int
		Detail                   *retireManifest `json:"detail"`
	}{action, reason, manifest.Journal, len(manifest.Changes), len(manifest.Skipped), manifest}
	if asJSON {
		return json.NewEncoder(out).Encode(result)
	}
	fmt.Fprintf(out, "\n✓ %s\n  reason: %s\n  evidence: changes=%d kept=%d\n  manifest: %s\n", action, reason, result.Changes, result.Kept, result.Manifest)
	return nil
}
