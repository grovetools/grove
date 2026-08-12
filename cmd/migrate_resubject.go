package cmd

// --resubject is the local half of the subject re-record transaction: it
// re-derives the subject of every stamped notespace currently carrying a
// machine-local (`local:`) identity from the tree's present reality — an
// ecosystem card or git remote that P2 missed or that appeared after stamping —
// and atomically moves the stamp plus the machine.toml [primaries]/[subjects]
// records. Notespace ids are never changed.
//
// It is safe ONLY while none of the affected subjects has been registered with
// a sync server: once a subject is on the wire, changing it needs the
// server-side rerecord/alias transaction, which this verb deliberately does
// not perform.

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/subject"
)

const resubjectManifestKind = "resubject"

type resubjectOptions struct {
	DryRun, Yes, Undo, JSON bool
	ManifestPath            string
}

type resubjectChange struct {
	Notebook   string `json:"notebook"`
	Name       string `json:"name"`
	Root       string `json:"root"`
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	CodeRoot   string `json:"code_root,omitempty"`
	OldSubject string `json:"old_subject"`
	NewSubject string `json:"new_subject"`
}

type resubjectManifest struct {
	Version   int               `json:"version"`
	Kind      string            `json:"kind"`
	CreatedAt time.Time         `json:"created_at"`
	State     string            `json:"state"`
	Journal   string            `json:"journal"`
	Changes   []resubjectChange `json:"changes"`
	Skipped   []string          `json:"skipped,omitempty"`
	Files     []p2FileBackup    `json:"files,omitempty"`
}

func runResubjectMigration(out io.Writer, in io.Reader, opts resubjectOptions, now time.Time) error {
	if opts.Undo {
		return undoResubject(out, opts)
	}
	if opts.ManifestPath == "" {
		opts.ManifestPath = filepath.Join(paths.StateDir(), "migrations", "resubject-"+now.UTC().Format("20060102T150405Z")+".json")
	}
	if !filepath.IsAbs(opts.ManifestPath) {
		return fmt.Errorf("--manifest must be absolute")
	}
	if err := ensureDaemonStopped(); err != nil {
		return err
	}
	manifest, err := planResubject(opts.ManifestPath, now)
	if err != nil {
		return err
	}
	if !opts.JSON {
		fmt.Fprintln(out, "Resubject plan (machine-local identity records only; no server transaction):")
		for _, change := range manifest.Changes {
			fmt.Fprintf(out, "  resubject  %s/%s id=%s %s -> %s (from %s)\n", change.Notebook, change.Name, change.ID, change.OldSubject, change.NewSubject, change.CodeRoot)
		}
		for _, skip := range manifest.Skipped {
			fmt.Fprintf(out, "  keep       %s\n", skip)
		}
		fmt.Fprintf(out, "  changes=%d kept=%d\n", len(manifest.Changes), len(manifest.Skipped))
		if len(manifest.Changes) > 0 {
			fmt.Fprintln(out, "  NOTE: only run this while none of the listed subjects has been registered with a sync server; ids are preserved, subjects move.")
		}
	}
	if len(manifest.Changes) == 0 {
		return renderResubjectResult(out, opts.JSON, "grove migrate resubject", "no local-subject notespace derives a real identity; nothing to change", manifest)
	}
	if opts.DryRun {
		return renderResubjectResult(out, opts.JSON, "grove migrate resubject dry-run", "plan produced explicit evidence; no files were changed", manifest)
	}
	if !opts.Yes {
		if opts.JSON {
			return fmt.Errorf("--json apply requires --yes")
		}
		fmt.Fprint(out, "Apply resubject? [y/N] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
			return fmt.Errorf("resubject not confirmed; no files were changed")
		}
	}

	backupPaths := []string{config.MachineConfigPath()}
	for _, change := range manifest.Changes {
		backupPaths = append(backupPaths, notespace.NotespaceStampPath(change.Root))
	}
	for _, path := range uniqueSortedStrings(backupPaths) {
		backup, err := backupP2File(path)
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, backup)
	}
	manifest.State = "prepared"
	if err := writeResubjectManifest(opts.ManifestPath, manifest); err != nil {
		return err
	}
	fail := func(step string, cause error) error {
		rollbackErr := rollbackResubject(manifest)
		manifest.State = "rolled-back"
		_ = writeResubjectManifest(opts.ManifestPath, manifest)
		if rollbackErr != nil {
			return fmt.Errorf("resubject failed after %s: %v; rollback also failed: %w", step, cause, rollbackErr)
		}
		return fmt.Errorf("resubject failed after %s; every changed file was restored: %w", step, cause)
	}

	for _, change := range manifest.Changes {
		if _, err := notespace.UpdateNotespace(change.Root, change.ID, notespace.NotespaceMutable{Name: change.Name, Subject: change.NewSubject, Kind: change.Kind}); err != nil {
			return fail("stamp "+change.Notebook+"/"+change.Name, err)
		}
		// This is config.RerecordSubject's shape, except that a mint retired in
		// favor of a DERIVED subject is deleted from [subjects] rather than
		// rewritten: that table records machine-local identities only, and a
		// card/remote-derived subject is re-derivable from the tree itself.
		change := change
		if _, _, err := config.EditMachineConfig(config.MachineConfigPath(), config.MachineEditOptions{}, func(cfg *config.MachineConfig) error {
			if got := cfg.Primaries[change.OldSubject]; got != change.ID {
				return fmt.Errorf("cannot resubject: [primaries] %q is %q, want %q", change.OldSubject, got, change.ID)
			}
			if got, exists := cfg.Primaries[change.NewSubject]; exists && got != change.ID {
				return fmt.Errorf("cannot resubject: [primaries] %q already points to %q", change.NewSubject, got)
			}
			delete(cfg.Primaries, change.OldSubject)
			cfg.Primaries[change.NewSubject] = change.ID
			for recordedPath, value := range cfg.Subjects {
				if value == change.OldSubject {
					delete(cfg.Subjects, recordedPath)
				}
			}
			return nil
		}); err != nil {
			return fail("machine identity "+change.Notebook+"/"+change.Name, err)
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
	if err := writeResubjectManifest(opts.ManifestPath, manifest); err != nil {
		return err
	}
	return renderResubjectResult(out, opts.JSON, "grove migrate resubject", "stamps and machine identity records were moved together; ids preserved", manifest)
}

func planResubject(manifestPath string, now time.Time) (*resubjectManifest, error) {
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
	manifest := &resubjectManifest{Version: p2ManifestVersion, Kind: resubjectManifestKind, CreatedAt: now.UTC(), State: "planned", Journal: manifestPath}
	var candidates []resubjectChange
	for _, nbName := range table.SortedNotebookNames() {
		nbRoot := table.NotebookRoot(nbName)
		if nbRoot == "" || isLocalModeNotebook(nbRoot) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(nbRoot, "notespaces"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			nsRoot := filepath.Join(nbRoot, "notespaces", entry.Name())
			stamp, err := notespace.LoadNotespace(nsRoot)
			if err != nil {
				return nil, err
			}
			if stamp == nil || !strings.HasPrefix(stamp.Subject, subject.LocalPrefix) {
				continue
			}
			// Recorded [subjects] mints are deliberately not consulted: the point
			// is to answer from the tree's current card/remote reality, not from
			// the mint being reconsidered.
			codeRoot, derived, err := deriveNotespaceSubject(table, entry.Name(), nbName, nil)
			if err != nil {
				return nil, fmt.Errorf("subject for %s/%s: %w", nbName, entry.Name(), err)
			}
			if derived == "" {
				continue // still honestly local
			}
			if recorded, ok := machine.Primaries[stamp.Subject]; ok && recorded != stamp.ID {
				manifest.Skipped = append(manifest.Skipped, fmt.Sprintf("%s/%s: [primaries] records %s -> %s, stamp id is %s; machine.toml and stamp disagree, resolve first", nbName, entry.Name(), stamp.Subject, recorded, stamp.ID))
				continue
			}
			if prior, ok := machine.Primaries[derived]; ok && prior != stamp.ID {
				manifest.Skipped = append(manifest.Skipped, fmt.Sprintf("%s/%s keeps %s: derived %s already has primary %s", nbName, entry.Name(), stamp.Subject, derived, prior))
				continue
			}
			candidates = append(candidates, resubjectChange{Notebook: nbName, Name: stamp.Name, Root: nsRoot, ID: stamp.ID, Kind: stamp.Kind, CodeRoot: codeRoot, OldSubject: stamp.Subject, NewSubject: derived})
		}
	}
	// One primary per subject: when several notespaces derive the same identity
	// none of them may take it silently — the operator names the answer.
	claims := map[string][]resubjectChange{}
	for _, candidate := range candidates {
		claims[candidate.NewSubject] = append(claims[candidate.NewSubject], candidate)
	}
	for _, candidate := range candidates {
		if len(claims[candidate.NewSubject]) > 1 {
			var names []string
			for _, claimant := range claims[candidate.NewSubject] {
				names = append(names, claimant.Notebook+"/"+claimant.Name)
			}
			manifest.Skipped = append(manifest.Skipped, fmt.Sprintf("%s/%s keeps %s: %s all derive %s; exactly one notespace may answer for it", candidate.Notebook, candidate.Name, candidate.OldSubject, strings.Join(names, ", "), candidate.NewSubject))
			continue
		}
		manifest.Changes = append(manifest.Changes, candidate)
	}
	return manifest, nil
}

func rollbackResubject(manifest *resubjectManifest) error {
	var errs []string
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
		return fmt.Errorf("resubject rollback errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

func undoResubject(out io.Writer, opts resubjectOptions) error {
	if opts.ManifestPath == "" {
		return fmt.Errorf("--undo requires --manifest")
	}
	data, err := os.ReadFile(opts.ManifestPath)
	if err != nil {
		return err
	}
	var manifest resubjectManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if manifest.Kind != resubjectManifestKind || manifest.Version != p2ManifestVersion {
		return fmt.Errorf("manifest %s is not an undoable resubject manifest (kind=%q version=%d)", opts.ManifestPath, manifest.Kind, manifest.Version)
	}
	if manifest.State == "undone" {
		return renderResubjectResult(out, opts.JSON, "grove migrate resubject undo", "manifest already undone; no local mutation needed", &manifest)
	}
	if manifest.State != "applied" {
		return fmt.Errorf("manifest state %q is not undoable", manifest.State)
	}
	for _, file := range manifest.Files {
		got, err := hashFileOrMissing(file.Path)
		if err != nil || got != file.AfterHash {
			return fmt.Errorf("refusing undo: %s changed after resubject (want %s, got %s)", file.Path, file.AfterHash, got)
		}
	}
	if err := rollbackResubject(&manifest); err != nil {
		return err
	}
	manifest.State = "undone"
	if err := writeResubjectManifest(opts.ManifestPath, &manifest); err != nil {
		return err
	}
	return renderResubjectResult(out, opts.JSON, "grove migrate resubject undo", "hash guards passed and stamp/machine identity state was restored", &manifest)
}

func writeResubjectManifest(path string, manifest *resubjectManifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return atomicMigrationWrite(path, append(data, '\n'))
}

func renderResubjectResult(out io.Writer, asJSON bool, action, reason string, manifest *resubjectManifest) error {
	result := struct {
		Action, Reason, Manifest string
		Changes, Kept            int
		Detail                   *resubjectManifest `json:"detail"`
	}{action, reason, manifest.Journal, len(manifest.Changes), len(manifest.Skipped), manifest}
	if asJSON {
		return json.NewEncoder(out).Encode(result)
	}
	fmt.Fprintf(out, "\n✓ %s\n  reason: %s\n  evidence: changes=%d kept=%d\n  manifest: %s\n", action, reason, result.Changes, result.Kept, result.Manifest)
	return nil
}
