// Package notescope holds the notebook-scope model the P3 verbs and the config
// TUI both read.
//
// It was extracted from `grove/cmd`'s notebook scope verbs rather than written
// beside them, because the TUI has to answer the same three questions the verbs
// answer — what notebooks.toml records, what the stamps on disk say, and what
// the server holds — and a second implementation of "what is recorded here"
// would be a second thing to keep true. The rule the verbs run on travels with
// the code: these facts are READ, never guessed. A notebook whose recorded root
// does not exist is reported as missing rather than created; a notespace
// directory with no stamp is reported as unminted rather than dropped.
//
// Everything in this file is pure with respect to the notes plane: it reads
// config and stamps and writes nothing. The acts — share, pull, move — are the
// verbs', reached through Service.
package notescope

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/syncproto"
)

// ContainerDir is the notebook-relative directory notespaces live in. It
// mirrors core/pkg/workspace.NotespaceDirectory, quoted here rather than
// imported so this package depends only on the identity and routing packages.
const ContainerDir = "notespaces"

// Notespace is one notespace directory beneath a recorded notebook. Stamp is
// nil when the directory carries no .notespace.toml — a real state (created
// before P2, or by hand) that share mints and every other verb names.
type Notespace struct {
	Dir   string
	Root  string
	Stamp *notespace.NotespaceStamp
}

// ID is the notespace's immutable id, or "" when it is unminted.
func (n Notespace) ID() string {
	if n.Stamp == nil {
		return ""
	}
	return n.Stamp.ID
}

// Subject is the notespace's recorded subject, or "" when it is unminted.
func (n Notespace) Subject() string {
	if n.Stamp == nil {
		return ""
	}
	return n.Stamp.Subject
}

// Notebook is one [notebooks.<name>] table as this machine holds it: the
// recorded declaration, the directory it resolves to, whether that directory
// exists, the recorded share state, and what is inside it.
type Notebook struct {
	Name string
	// Declared is the notebooks.toml spelling; Root is the expanded path. Both
	// are kept because the transition printer reports both.
	Declared string
	Root     string
	Exists   bool
	// Shared is `share = true`; SyncRecorded distinguishes an explicit
	// `share = false` (D9's recorded-as-unshared) from "never recorded".
	Shared       bool
	SyncRecorded bool
	Stamp        *notespace.NotebookStamp
	Notespaces   []Notespace
}

// ID is the notebook's immutable id, or "" when the root carries no stamp.
func (n Notebook) ID() string {
	if n.Stamp == nil {
		return ""
	}
	return n.Stamp.ID
}

// Sync column labels. The notebook is the only sync knob, so this is DERIVED
// from containment and never toggled per row: a notespace is shared because
// the notebook containing it is shared.
const (
	SyncLabelShared = "● shared"
	SyncLabelLocal  = "local"
)

// SyncLabel is the derived sync column for this notebook.
func (n Notebook) SyncLabel() string {
	if n.Shared {
		return SyncLabelShared
	}
	return SyncLabelLocal
}

// RetentionNote states the third state of the recorded tri-state, which the
// two-valued sync column cannot carry.
//
// `share = false` is not the same fact as "never recorded": it is D9's
// recorded decision to stop, and the server keeps the history behind it. Both
// render as `local` in the column — the column answers "is this notebook in
// sync scope right now" — so the distinction is stated here instead of being
// silently folded away.
func (n Notebook) RetentionNote() string {
	if n.Shared || !n.SyncRecorded {
		return ""
	}
	return "recorded unshared (D9): " + syncproto.UnshareRetentionStatement
}

// Load reads the recorded routing pair and scans every notebook it names. A
// machine with no notebooks.toml is refused by name: the notebook scope model
// has nothing to talk about before the config collapse has landed here.
//
// This is the verbs' entry point. A reader that must tolerate a machine with
// nothing recorded — the config TUI's empty state — calls coderoot.Load and
// Scan directly instead.
func Load() (coderoot.Table, []Notebook, error) {
	table, err := coderoot.Load()
	if err != nil {
		return coderoot.Table{}, nil, err
	}
	if table.NotebooksFilePath == "" {
		return coderoot.Table{}, nil, fmt.Errorf("this machine records no %s; the notebook scope model reads recorded notebooks only — run `grove migrate` first", coderoot.NotebooksFileName)
	}
	scanned, err := Scan(table)
	if err != nil {
		return coderoot.Table{}, nil, err
	}
	return table, scanned, nil
}

// Scan walks the recorded notebooks in deterministic order. A missing root is
// recorded as Exists=false rather than an error: only the verbs that need the
// directory decide that its absence is fatal, and the join delta deliberately
// still reports it.
func Scan(table coderoot.Table) ([]Notebook, error) {
	out := make([]Notebook, 0, len(table.Notebooks))
	for _, name := range table.SortedNotebookNames() {
		definition := table.Notebooks[name]
		root := table.NotebookRoot(name)
		entry := Notebook{
			Name:         name,
			Declared:     definition.Root,
			Root:         root,
			Shared:       definition.Shared(),
			SyncRecorded: definition.SyncRecorded(),
		}
		info, err := os.Stat(root)
		entry.Exists = err == nil && info.IsDir()
		if !entry.Exists {
			out = append(out, entry)
			continue
		}
		stamp, err := notespace.LoadNotebook(root)
		if err != nil {
			return nil, err
		}
		entry.Stamp = stamp
		contained, err := ScanContained(root)
		if err != nil {
			return nil, err
		}
		entry.Notespaces = contained
		out = append(out, entry)
	}
	return out, nil
}

// ScanContained lists the notespaces of one notebook root. The notespaces/
// directory being absent is not an error — an empty notebook is a notebook.
func ScanContained(notebookRoot string) ([]Notespace, error) {
	dir := filepath.Join(notebookRoot, ContainerDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read notespaces in %s: %w", notebookRoot, err)
	}
	out := make([]Notespace, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		root := filepath.Join(dir, entry.Name())
		stamp, err := notespace.LoadNotespace(root)
		if err != nil {
			return nil, err
		}
		out = append(out, Notespace{Dir: entry.Name(), Root: root, Stamp: stamp})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out, nil
}

// Find resolves a notebook NAME against the recorded table. An unknown name is
// a hard error naming the file and every name it does record — the alternative
// is a verb that invents a notebook the operator never wrote down.
func Find(table coderoot.Table, scanned []Notebook, name string) (Notebook, error) {
	for _, entry := range scanned {
		if entry.Name == name {
			return entry, nil
		}
	}
	return Notebook{}, fmt.Errorf("%s records no notebook %q (it records: %s)",
		DisplayRecordedPath(table.NotebooksFilePath, coderoot.NotebooksFileName), name,
		strings.Join(table.SortedNotebookNames(), ", "))
}

// DisplayRecordedPath names a config file, falling back to its bare name when
// nothing recorded a path.
func DisplayRecordedPath(path, fallback string) string {
	if strings.TrimSpace(path) == "" {
		return fallback
	}
	return path
}

// LocalNotebooks projects the scan into the pure delta function's input. Only
// stamped notebooks carry an id, and only an id compares against a server —
// unstamped notebooks are reported separately by the caller rather than folded
// in as "absent from the server", which they are not.
func LocalNotebooks(scanned []Notebook) []syncproto.LocalNotebook {
	out := make([]syncproto.LocalNotebook, 0, len(scanned))
	for _, entry := range scanned {
		if entry.ID() == "" {
			continue
		}
		local := syncproto.LocalNotebook{
			ID:   syncproto.NotebookID(entry.ID()),
			Name: entry.Name,
			// Both halves of the recorded tri-state travel: `share = false` is
			// D9's recorded-as-unshared and must not read as "never considered".
			Shared:   entry.Shared,
			Recorded: entry.SyncRecorded,
		}
		for _, ns := range entry.Notespaces {
			if ns.ID() != "" {
				local.Notespaces = append(local.Notespaces, syncproto.NotespaceID(ns.ID()))
			}
		}
		out = append(out, local)
	}
	return out
}

// Delta compares this machine's scan against a server inventory.
func Delta(scanned []Notebook, inventory syncproto.InventoryResponse) syncproto.InventoryDelta {
	return syncproto.BuildInventoryDelta(LocalNotebooks(scanned), inventory)
}

// RefuseDuplicateNotebookIDs stops a verb that would ACT on a machine
// recording one notebook id twice — the copied-stamp state of D8.
//
// The check needs no server: duplication is entirely a local fact, and
// BuildInventoryDelta reports it against an empty inventory just as well as
// against a real one. The join delta deliberately does not call this — the
// delta is where an operator meets the duplicate first, and refusing to render
// it would hide the thing that needs fixing.
func RefuseDuplicateNotebookIDs(scanned []Notebook) error {
	return Delta(scanned, syncproto.InventoryResponse{}).Conflicts()
}

// StampedElsewhere reports another recorded notebook already carrying this
// notebook id.
//
// It exists for the one verb that can CREATE a duplicate rather than merely
// meet one: `notebook pull` installs a server-supplied id into a root that
// carried none, and doing that while a second recorded root already holds it
// would mint the exact D8 state every other verb here refuses to act on.
func StampedElsewhere(scanned []Notebook, exclude string, id syncproto.NotebookID) (Notebook, bool) {
	for _, entry := range scanned {
		if entry.Name == exclude {
			continue
		}
		if entry.ID() != "" && syncproto.NotebookID(entry.ID()) == id {
			return entry, true
		}
	}
	return Notebook{}, false
}
