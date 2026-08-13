package doctorchecks

// The repair half of D8 / W3.6: `grove doctor --fix --remint <root>`.
//
// The runtime rule is the daemon's: two physical roots carrying one notespace
// stamp id, first-seen keeps syncing, the later copy is parked with evidence
// naming both paths. Parking is a HOLD, not a fix — the machine stays in a
// state where one identity has two homes, and no amount of sweeping resolves
// it, because nothing but the operator can say which copy should keep the
// history.
//
// So the repair is designation-driven from end to end:
//
//	The operator names a ROOT, never an id. An id is exactly the thing that
//	is ambiguous here; a path is not.
//
//	Nothing is inferred about which copy "should" lose. A run that names a
//	root carrying an id no other root claims is refused, and so is a root
//	that is not a recorded notespace at all.
//
//	The re-mint is a stamp rewrite plus whatever local bindings FOLLOWED that
//	root, in one operator sitting, with both halves printed. The id survives
//	at the copy that was not designated, so a binding naming that id is still
//	true and is deliberately left alone — the evidence says which bindings
//	were inspected and why each was or was not rewritten.
//
// Nothing here talks to the server or the daemon. The daemon rebuilds its
// parking verdict from the stamps on disk every pass, so the park clears itself
// once the duplicate is gone; and the server's claim still belongs to the id,
// which still exists, at the keeper.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/notespace"
)

// notespaceRecord is one stamped notespace directory beneath a recorded
// notebook.
type notespaceRecord struct {
	Notebook     string
	NotebookRoot string
	Root         string
	Stamp        notespace.NotespaceStamp
}

// ScanRecordedNotespaces walks every notebook the machine records and returns
// its stamped notespaces. It reads recorded topology only — no discovery, no
// walking up from the cwd — so what it reports is what routing sees.
func ScanRecordedNotespaces(table coderoot.Table) ([]notespaceRecord, error) {
	var out []notespaceRecord
	for _, name := range table.SortedNotebookNames() {
		notebookRoot := table.NotebookRoot(name)
		dir := filepath.Join(notebookRoot, "notespaces")
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // a notebook with no notespaces/ is not an error here
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			root := filepath.Join(dir, entry.Name())
			stamp, err := notespace.LoadNotespace(root)
			if err != nil {
				return nil, err
			}
			if stamp == nil {
				continue
			}
			out = append(out, notespaceRecord{Notebook: name, NotebookRoot: notebookRoot, Root: root, Stamp: *stamp})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Root < out[j].Root })
	return out, nil
}

// RemintResult is what the designation flow did, for the caller's evidence and
// for tests that must assert the binding half as well as the stamp half.
type RemintResult struct {
	Root  string
	OldID string
	NewID string
	// Keepers are the other roots that still carry OldID — the copies the
	// operator did NOT designate.
	Keepers []string
	// Rewritten and Left name the machine.toml bindings that were changed and
	// the ones that were deliberately not, each with its reason.
	Rewritten []string
	Left      []string
}

// RemintDesignatedDuplicate re-mints the notespace at root and repairs the
// local bindings that followed it.
//
// designated is the operator's choice of LOSER: the copy that gives up the
// duplicated id. Everything about the run is checked against recorded state
// before a byte is written, and the whole thing is refused rather than
// half-applied when any precondition fails.
func RemintDesignatedDuplicate(designated string, out io.Writer) (*RemintResult, error) {
	if strings.TrimSpace(designated) == "" {
		return nil, fmt.Errorf("--remint names the notespace ROOT to re-mint; an id is exactly the thing that is ambiguous here")
	}
	root := inspectPath(designated).Canonical
	if !inspectPath(designated).Exists {
		return nil, fmt.Errorf("%s is not a directory on this machine", designated)
	}

	table, err := coderoot.Load()
	if err != nil {
		return nil, fmt.Errorf("recorded notebook topology is invalid; fix it before re-minting: %w", err)
	}
	records, err := ScanRecordedNotespaces(table)
	if err != nil {
		return nil, err
	}

	var target *notespaceRecord
	byID := map[string][]string{}
	for i := range records {
		canonical := inspectPath(records[i].Root).Canonical
		byID[records[i].Stamp.ID] = append(byID[records[i].Stamp.ID], canonical)
		if canonical == root {
			target = &records[i]
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%s is not a stamped notespace beneath a recorded notebook; re-mint repairs recorded topology, and a root nothing records is not part of it", designated)
	}

	claimants := byID[target.Stamp.ID]
	if len(claimants) < 2 {
		return nil, fmt.Errorf("%s carries notespace id %s, which no other recorded root claims; there is no duplicate to repair (re-minting a unique id would only discard its history)",
			target.Root, target.Stamp.ID)
	}
	keepers := make([]string, 0, len(claimants)-1)
	for _, claimant := range claimants {
		if claimant != root {
			keepers = append(keepers, claimant)
		}
	}
	sort.Strings(keepers)

	fmt.Fprintf(out, "  duplicate    %s claimed by %d roots\n", target.Stamp.ID, len(claimants))
	for _, keeper := range keepers {
		fmt.Fprintf(out, "  keeps id     %s\n", keeper)
	}
	fmt.Fprintf(out, "  re-minting   %s\n", target.Root)

	minted, err := notespace.RemintNotespace(target.Root, target.Stamp.ID)
	if err != nil {
		return nil, err
	}
	result := &RemintResult{Root: target.Root, OldID: minted.OldID, NewID: minted.NewID, Keepers: keepers}
	fmt.Fprintf(out, "  new id       %s  (was %s, name %q, subject %s)\n", minted.NewID, minted.OldID, minted.Stamp.Name, minted.Stamp.Subject)

	if err := repairBindings(target, result); err != nil {
		// The stamp is already rewritten; say so plainly rather than implying
		// the machine is untouched.
		return result, fmt.Errorf("%s now carries %s, but its machine bindings could not be repaired: %w", target.Root, minted.NewID, err)
	}
	for _, line := range result.Rewritten {
		fmt.Fprintf(out, "  rewrote      %s\n", line)
	}
	for _, line := range result.Left {
		fmt.Fprintf(out, "  left         %s\n", line)
	}
	fmt.Fprintf(out, "\n  %s is a distinct notespace again. The daemon rebuilds its parking verdict\n", target.Root)
	fmt.Fprintf(out, "  from the stamps on disk each pass, so the park clears on its own — no restart.\n\n")
	return result, nil
}

// repairBindings rewrites the machine.toml records that FOLLOWED the re-minted
// root, and records why each one it left alone was left alone.
//
// The governing fact: the old id still exists, at the copy that was not
// designated. A binding that names it is therefore still true unless the
// binding also names a LOCATION that resolves to the re-minted root — which is
// what makes [sync.registry] different from [primaries]. The registry binding
// carries a notebook, so it can be checked against the root; a primary carries
// only subject → id, so nothing in it points at one copy rather than the other,
// and rewriting it would be a guess dressed as a repair.
func repairBindings(target *notespaceRecord, result *RemintResult) error {
	machinePath := config.MachineConfigPath()
	current, err := config.LoadMachineConfig()
	if err != nil {
		return err
	}
	if current == nil {
		result.Left = append(result.Left, "machine.toml records no bindings")
		return nil
	}

	registryFollows := false
	if current.Sync.Registry != nil && current.Sync.Registry.NotespaceID == result.OldID {
		table, loadErr := coderoot.Load()
		if loadErr != nil {
			return loadErr
		}
		recordedRoot := table.NotebookRoot(current.Sync.Registry.Notebook)
		registryFollows = recordedRoot != "" &&
			inspectPath(recordedRoot).Canonical == inspectPath(target.NotebookRoot).Canonical
		if !registryFollows {
			result.Left = append(result.Left, fmt.Sprintf("[sync.registry] notespace_id = %s (its notebook %q is not the one holding the re-minted root; the id still resolves, at %s)",
				result.OldID, current.Sync.Registry.Notebook, strings.Join(result.Keepers, ", ")))
		}
	}
	for subject, id := range current.Primaries {
		if id == result.OldID {
			result.Left = append(result.Left, fmt.Sprintf("[primaries] %q = %s (the id survives at %s, so the primary still names one root)",
				subject, result.OldID, strings.Join(result.Keepers, ", ")))
		}
	}

	if !registryFollows {
		return nil
	}

	// Validate against the stamp index the re-mint produced, so a rewrite that
	// would leave a binding pointing at an id no root carries is refused by the
	// writer rather than discovered by the next doctor run.
	known, err := knownNotespaceIDs()
	if err != nil {
		return err
	}
	_, changed, err := config.EditMachineConfig(machinePath, config.MachineEditOptions{KnownNotespaceIDs: known}, func(cfg *config.MachineConfig) error {
		if cfg.Sync.Registry == nil || cfg.Sync.Registry.NotespaceID != result.OldID {
			return fmt.Errorf("[sync.registry] changed underneath this repair; re-run `grove doctor`")
		}
		cfg.Sync.Registry.NotespaceID = result.NewID
		return nil
	})
	if err != nil {
		return err
	}
	if changed {
		result.Rewritten = append(result.Rewritten, fmt.Sprintf("[sync.registry] notespace_id %s → %s (it names notebook %q, which holds the re-minted root)",
			result.OldID, result.NewID, current.Sync.Registry.Notebook))
	}
	return nil
}

// knownNotespaceIDs is the stamp index EditMachineConfig validates bindings
// against: every id recorded topology can actually reach.
func knownNotespaceIDs() (map[string]struct{}, error) {
	table, err := coderoot.Load()
	if err != nil {
		return nil, err
	}
	records, err := ScanRecordedNotespaces(table)
	if err != nil {
		return nil, err
	}
	known := make(map[string]struct{}, len(records))
	for _, record := range records {
		known[record.Stamp.ID] = struct{}{}
	}
	return known, nil
}
