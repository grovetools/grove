package notescope

import (
	"fmt"
	"strings"

	"github.com/grovetools/core/pkg/syncproto"
)

// The row models both P3 TUI pages render.
//
// They are here, beside the scan they read and the vocabulary `grove sync join`
// prints, so the two renderings of one comparison cannot drift into disagreeing
// about what a row MEANS. A row states a direction and, at most, which key
// would act on it; pressing that key is the only thing that acts. Nothing in
// this file writes, fetches, or decides on the operator's behalf.

// ---- Notes page ---------------------------------------------------------------

// NotesRowKind distinguishes the two grains the Notes page renders.
type NotesRowKind string

const (
	NotesRowNotebook  NotesRowKind = "notebook"
	NotesRowNotespace NotesRowKind = "notespace"
)

// NotesRow is one rendered line-group on the Notes page: a notebook, or one
// notespace contained by an expanded notebook.
type NotesRow struct {
	Kind NotesRowKind
	// Key identifies the row for expansion state and cursor restoration.
	Key string
	// Notebook is the recorded notebook name, on both kinds of row.
	Notebook string
	// Declared / Root / Exists describe the notebook row's recorded root.
	Declared string
	Root     string
	Exists   bool
	// Sync is the DERIVED sync column — SyncLabelShared or SyncLabelLocal. It
	// is set on notebook rows only: the notebook is the sync knob, so a
	// per-notespace column would be a per-notespace toggle wearing a label.
	Sync string
	// Retention carries D9's recorded-unshared statement when there is one.
	Retention string
	// Expandable / Expanded drive the notebook row's disclosure glyph.
	Expandable bool
	Expanded   bool
	// Notespaces is the contained count, on notebook rows.
	Notespaces int

	// Dir / ID / Subject / NotespaceRoot describe a notespace row.
	Dir           string
	ID            string
	Subject       string
	NotespaceRoot string
}

// Movable reports whether `m` can act on this row. Moving is per notespace —
// it is how a notespace changes notebooks, and therefore how it changes sync
// scope — and an unminted notespace has no id to preserve across the move.
func (r NotesRow) Movable() bool {
	return r.Kind == NotesRowNotespace && r.ID != ""
}

// BuildNotesRows flattens the scan into rows, expanding the notebooks named in
// expanded. Order follows the scan, which is already deterministic.
func BuildNotesRows(scanned []Notebook, expanded map[string]bool) []NotesRow {
	rows := make([]NotesRow, 0, len(scanned))
	for _, nb := range scanned {
		open := expanded[nb.Name]
		rows = append(rows, NotesRow{
			Kind:       NotesRowNotebook,
			Key:        nb.Name,
			Notebook:   nb.Name,
			Declared:   nb.Declared,
			Root:       nb.Root,
			Exists:     nb.Exists,
			Sync:       nb.SyncLabel(),
			Retention:  nb.RetentionNote(),
			Expandable: len(nb.Notespaces) > 0,
			Expanded:   open && len(nb.Notespaces) > 0,
			Notespaces: len(nb.Notespaces),
		})
		if !open {
			continue
		}
		for _, ns := range nb.Notespaces {
			rows = append(rows, NotesRow{
				Kind:          NotesRowNotespace,
				Key:           nb.Name + "/" + ns.Dir,
				Notebook:      nb.Name,
				Sync:          "",
				Dir:           ns.Dir,
				ID:            ns.ID(),
				Subject:       ns.Subject(),
				NotespaceRoot: ns.Root,
			})
		}
	}
	return rows
}

// MoveDestinations lists the recorded notebooks a notespace currently in
// `from` could move into: recorded, rooted at a directory that exists, and not
// the one it is already in. A move never creates a notebook root, so a
// notebook whose root is missing is not offered.
func MoveDestinations(scanned []Notebook, from string) []string {
	out := make([]string, 0, len(scanned))
	for _, nb := range scanned {
		if nb.Name == from || !nb.Exists {
			continue
		}
		out = append(out, nb.Name)
	}
	return out
}

// ---- Join delta page ----------------------------------------------------------

// JoinRowKind distinguishes the grains and the trailing advisories of the join
// delta page.
type JoinRowKind string

const (
	JoinRowNotebook   JoinRowKind = "notebook"
	JoinRowNotespace  JoinRowKind = "notespace"
	JoinRowUnparented JoinRowKind = "unparented"
	JoinRowNotice     JoinRowKind = "notice"
)

// Join delta actions. These are the keys the page binds, carried on the row so
// the renderer and the key handler cannot disagree about which rows act.
const (
	JoinActionShare = "s"
	JoinActionPull  = "p"
)

// JoinRow is one line of the join delta.
type JoinRow struct {
	Kind JoinRowKind
	Key  string
	// Name is the notebook's display name — the recorded local name when this
	// machine records one, otherwise the server's.
	Name string
	ID   string
	// Here / Server / Summary are the CLI's three delta columns, verbatim.
	Here    string
	Server  string
	Summary string
	// Action is JoinActionShare, JoinActionPull, or "" for a row no key acts
	// on. Reason says why, whenever there is something to say — including for
	// actionable rows whose local half looks unready, so the operator reads it
	// before pressing rather than after.
	Action string
	Reason string
	// Text carries the whole line for notespace, unparented and notice rows.
	Text string

	Expandable bool
	Expanded   bool
	Depth      int
}

// BuildJoinRows renders a delta as rows grouped by notebook, expanding the
// notebook ids named in expanded to notespace grain.
//
// scanned is the local scan the delta was built from: it supplies the recorded
// name for an id (names do not route, but they are what the operator typed)
// and the local readiness facts that belong in a Reason.
func BuildJoinRows(delta syncproto.InventoryDelta, scanned []Notebook, expanded map[string]bool) []JoinRow {
	names := map[syncproto.NotebookID]string{}
	recorded := map[string]Notebook{}
	for _, entry := range scanned {
		if entry.ID() != "" {
			names[syncproto.NotebookID(entry.ID())] = entry.Name
		}
		recorded[entry.Name] = entry
	}
	// One id recorded twice has no single local name, and picking one by map
	// iteration would name a different copy on different runs. The row shows
	// every name that claims the id, which is the evidence the operator needs
	// to decide which copy to re-mint; no verb acts on it either way.
	for _, dup := range delta.DuplicateLocalNotebooks {
		names[dup.ID] = strings.Join(dup.Names, ", ")
	}

	rows := make([]JoinRow, 0, len(delta.Notebooks))
	for _, nb := range delta.Notebooks {
		name := nb.Name
		if local, ok := names[nb.ID]; ok {
			name = local
		}
		if strings.TrimSpace(name) == "" {
			name = "(unnamed)"
		}
		key := nb.ID.String()
		open := expanded[key]
		children := len(nb.LocalOnlyNotespaces) + len(nb.ServerOnlyNotespaces)
		action, reason := joinAction(nb, name, recorded)
		rows = append(rows, JoinRow{
			Kind:       JoinRowNotebook,
			Key:        key,
			Name:       name,
			ID:         nb.ID.String(),
			Here:       LocalStateColumn(nb),
			Server:     ServerStateColumn(nb),
			Summary:    DeltaSummary(nb),
			Action:     action,
			Reason:     reason,
			Expandable: children > 0,
			Expanded:   open && children > 0,
		})
		if !open {
			continue
		}
		for _, id := range nb.LocalOnlyNotespaces {
			rows = append(rows, JoinRow{
				Kind: JoinRowNotespace, Key: key + "/" + id.String(), Name: name, ID: id.String(),
				Text: "here only    " + id.String(), Depth: 1,
			})
		}
		for _, id := range nb.ServerOnlyNotespaces {
			rows = append(rows, JoinRow{
				Kind: JoinRowNotespace, Key: key + "/" + id.String(), Name: name, ID: id.String(),
				Text: "server only  " + id.String(), Depth: 1,
			})
		}
	}

	// A notespace the server holds in no notebook is surfaced, not hidden: it
	// is a real state, and a machine that drops it from the comparison is a
	// machine that cannot later explain where a notespace went.
	if n := len(delta.UnparentedServerNotespaces); n > 0 {
		key := "\x00unparented"
		open := expanded[key]
		rows = append(rows, JoinRow{
			Kind: JoinRowUnparented, Key: key,
			Text:       fmt.Sprintf("unparented on the server: %d notespace%s", n, plural(n)),
			Expandable: true, Expanded: open,
		})
		if open {
			for _, id := range delta.UnparentedServerNotespaces {
				rows = append(rows, JoinRow{Kind: JoinRowNotespace, Key: key + "/" + id.String(), ID: id.String(), Text: id.String(), Depth: 1})
			}
		}
	}

	rows = append(rows, joinNotices(delta, scanned)...)
	return rows
}

// joinAction decides which key, if any, acts on a notebook row, and what the
// operator should know before pressing it.
//
// The TUI never decides that an act would fail — the verb does, out loud, and
// this page runs the same verb the CLI does. So a blocking local fact becomes
// a Reason on an otherwise actionable row rather than a silently removed key:
// the operator reads it here, and if they press anyway they get the verb's own
// full sentence instead of a TUI's paraphrase.
func joinAction(nb syncproto.NotebookDelta, name string, recorded map[string]Notebook) (string, string) {
	switch nb.Direction {
	case syncproto.DeltaDirectionShare:
		if nb.LocalDuplicate {
			return "", "this machine records this notebook id twice (D8); re-mint one copy before sharing"
		}
		entry, ok := recorded[name]
		switch {
		case !ok:
			return JoinActionShare, "share names a recorded notebook; this row's name is not one"
		case !entry.Exists:
			return JoinActionShare, "recorded root " + entry.Root + " does not exist; share never creates one"
		}
		return JoinActionShare, ""
	case syncproto.DeltaDirectionPull:
		if !nb.PullEligible {
			return "", "retained after an unshare; not on offer (D9)"
		}
		entry, ok := recorded[name]
		switch {
		case !ok:
			return JoinActionPull, "pull materializes into a RECORDED root; this machine records no notebook named " + name
		case !entry.Exists:
			return JoinActionPull, "recorded root " + entry.Root + " does not exist; pull refuses a missing root rather than creating one"
		}
		return JoinActionPull, ""
	}
	// Present on both sides. Membership may still differ, which the summary
	// says; reconciling it is `notespace move`, not a notebook-grained key.
	if nb.LocalDuplicate {
		return "", "this machine records this notebook id twice (D8); every acting verb refuses until it is resolved"
	}
	return "", ""
}

// joinNotices renders the local facts that sit outside the comparison but
// change what it means.
func joinNotices(delta syncproto.InventoryDelta, scanned []Notebook) []JoinRow {
	var out []JoinRow
	if conflict := delta.Conflicts(); conflict != nil {
		out = append(out, JoinRow{Kind: JoinRowNotice, Key: "\x00duplicates", Text: conflict.Error()})
	}
	var unstamped []string
	for _, entry := range scanned {
		if entry.Exists && entry.ID() == "" {
			unstamped = append(unstamped, entry.Name)
		}
	}
	if len(unstamped) > 0 {
		out = append(out, JoinRow{
			Kind: JoinRowNotice, Key: "\x00unstamped",
			Text: "recorded here but unstamped, so absent from this comparison: " + strings.Join(unstamped, ", ") +
				" — `grove notebook share <name>` mints a notebook id as part of sharing it",
		})
	}
	for _, entry := range scanned {
		if !entry.Exists {
			out = append(out, JoinRow{
				Kind: JoinRowNotice, Key: "\x00missing/" + entry.Name,
				Text: "notebook " + entry.Name + " records root " + entry.Root + ", which does not exist — pull refuses into a missing root",
			})
		}
	}
	return out
}

// ---- delta vocabulary ----------------------------------------------------------
//
// These three are the columns `grove sync join` prints. They live here so the
// CLI table and the TUI page say the same words about the same row.

// LocalStateColumn renders the recorded tri-state symmetrically with the
// server's. "unshared" is not the same as "—": the first is D9's recorded
// decision to stop, the second is a notebook nobody ever answered for.
func LocalStateColumn(nb syncproto.NotebookDelta) string {
	state := nb.LocalShareState
	if nb.Direction == syncproto.DeltaDirectionPull {
		return "—"
	}
	if state == "" {
		state = "recorded"
	}
	if nb.LocalDuplicate {
		return state + " (duplicate id)"
	}
	return state
}

// ServerStateColumn renders what the server says about this notebook.
func ServerStateColumn(nb syncproto.NotebookDelta) string {
	if nb.ServerShareState == "" {
		return "—"
	}
	return nb.ServerShareState
}

// DeltaSummary states the direction, never an action.
func DeltaSummary(nb syncproto.NotebookDelta) string {
	switch nb.Direction {
	case syncproto.DeltaDirectionShare:
		return "share: this server does not hold it"
	case syncproto.DeltaDirectionPull:
		if !nb.PullEligible {
			return "retained after an unshare; not on offer (D9)"
		}
		return "pull: this machine does not record it"
	}
	switch {
	case len(nb.LocalOnlyNotespaces) == 0 && len(nb.ServerOnlyNotespaces) == 0:
		return "same membership on both sides"
	default:
		return fmt.Sprintf("%d notespace%s here only, %d there only",
			len(nb.LocalOnlyNotespaces), plural(len(nb.LocalOnlyNotespaces)), len(nb.ServerOnlyNotespaces))
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
