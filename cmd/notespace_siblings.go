package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/subject"
	"github.com/grovetools/core/pkg/transition"
	"github.com/grovetools/core/tui/theme"
)

// `grove notespace new|primary|list` — primary and siblings (P4 W4.1/W4.4).
//
// A subject may hold several notespaces on this machine. Which one this machine
// writes into by default is the PRIMARY, and the sentence that definition needs
// ships on every surface below:
//
//	the primary is this machine's default target notespace for a subject — a
//	machine-local routing pointer recorded in config, never a synced property
//	of the notespace.
//
// That is why the three verbs divide the way they do. `new` materializes a
// sibling and never touches [primaries]: creating a second notespace is not a
// statement about routing, and a verb that quietly re-pointed the machine while
// making one would make every later unqualified write land somewhere the
// operator never chose. `primary` does nothing BUT re-point, so the flip is one
// visible act with its own evidence. `list` shows the arrangement both of them
// operate on.
//
// Siblings in the SAME notebook are legal — the label disambiguates
// (`core (primary)` / `core · personal`), the directory name is uniquified —
// because the notebook is the sync boundary, not the identity boundary.
//
// Everything here is fail-closed on the two states that make an answer
// untrustworthy: a duplicated stamp id (D8) and a malformed stamp. Both are
// refused by the shared stamp index at load, before any verb decides anything.

func newNotespaceNewCmd() *cobra.Command {
	var opts notespaceNewOptions
	cmd := &cobra.Command{
		Use:   "new <subject> --in <notebook>",
		Short: "Create an additional notespace for a subject the machine already routes",
		Long: `Create one more notespace for a subject that already has a primary.

The primary is this machine's default target notespace for a subject — a
machine-local routing pointer recorded in config, never a synced property of the
notespace. This verb does not touch it. It creates a directory and a stamp:
same subject, a NEW immutable id, and machine.toml is left byte-identical.

  · the subject must already have exactly one recorded, resolvable primary.
    The FIRST notespace for a subject is materialized with its [primaries]
    record by ` + "`grove notebook share`" + ` or ` + "`grove migrate`" + ` (D4); this verb
    makes a sibling of something, so it refuses when there is nothing to be a
    sibling of, and names the repair when the recorded primary is broken;
  · --in names the destination notebook, which must be recorded and must exist.
    A sibling in the SAME notebook as the primary is legal;
  · --name is optional. Without it the primary's name is reused, and a numeric
    suffix is added only if that directory name is already taken in the
    destination. An explicit --name that is taken is refused rather than
    silently renamed.

Nothing routes to the new notespace until you say so: unqualified writes keep
landing in the primary until ` + "`grove notespace primary`" + ` moves it.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.subject = strings.TrimSpace(args[0])
			return runNotespaceNew(cmd.OutOrStdout(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.in, "in", "", "Destination notebook name (recorded in notebooks.toml)")
	cmd.Flags().StringVar(&opts.name, "name", "", "Notespace name and directory (default: the primary's name, uniquified)")
	cmd.Flags().StringVar(&opts.kind, "kind", "", "Notespace kind (default: the primary's kind)")
	cmd.Flags().BoolVar(&opts.asJSON, "json", false, "Render the transition evidence as JSON")
	return cmd
}

type notespaceNewOptions struct {
	subject string
	in      string
	name    string
	kind    string
	asJSON  bool
}

func runNotespaceNew(out io.Writer, opts notespaceNewOptions) error {
	if strings.TrimSpace(opts.in) == "" {
		return fmt.Errorf("--in <notebook> is required; a new notespace is created in a notebook this machine records")
	}
	scope, err := loadNotespaceScope()
	if err != nil {
		return err
	}
	if err := subject.Validate(opts.subject); err != nil {
		return fmt.Errorf("%w\n  %s", err, scope.recordedSubjectsHint())
	}

	// The exactly-one-primary invariant is enforced HERE, at creation, not only
	// in the next doctor run (W4.4). A sibling minted against a subject whose
	// routing is already broken would make the repair harder and hide the
	// breakage behind a verb that appeared to succeed.
	primary, err := scope.index.PrimaryFor(opts.subject, scope.primaries)
	if err != nil {
		return fmt.Errorf("%w\n  a sibling is created next to a primary; `grove notespace list` shows what this machine records, and `grove doctor` names the repair", err)
	}

	destination, err := findRecordedNotebook(scope.table, scope.scanned, strings.TrimSpace(opts.in))
	if err != nil {
		return err
	}
	if !destination.Exists {
		return fmt.Errorf("notebook %q records root %s, which does not exist; `notespace new` never creates a notebook root — create it, or fix [notebooks.%s].root",
			destination.Name, destination.Root, destination.Name)
	}

	taken := map[string]bool{}
	for _, ns := range destination.Notespaces {
		taken[ns.Dir] = true
	}
	name, derived, err := siblingName(opts.name, primary, taken)
	if err != nil {
		return err
	}
	kind := strings.TrimSpace(opts.kind)
	if kind == "" {
		kind = primary.Stamp.Kind
	}

	root := filepath.Join(destination.Root, notespaceContainerDir, name)
	if _, statErr := os.Lstat(root); statErr == nil {
		return fmt.Errorf("%s already exists; a new notespace is a new directory, never an adoption of one that is already there", root)
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create the notespace root %s: %w", root, err)
	}
	stamp, err := notespace.MintNotespace(root, notespace.NotespaceMutable{Name: name, Subject: opts.subject, Kind: kind})
	if err != nil {
		// The directory is removed non-recursively: it was created empty a
		// moment ago, so a rmdir that fails because something is in it is a
		// surprise worth leaving alone rather than forcing.
		_ = os.Remove(root)
		return fmt.Errorf("mint the notespace stamp at %s: %w", root, err)
	}
	// Two roots claiming one id is the state every verb here refuses to act on
	// (D8), so the verb that MINTS one checks it did not just create it.
	if existing, dupErr := scope.index.ByID(stamp.ID); dupErr != nil || len(existing) > 0 {
		return fmt.Errorf("the freshly minted id %s is already stamped at %s; nothing was recorded — re-mint the duplicate with `grove doctor --fix --remint` before creating siblings",
			stamp.ID, existing[0].Root)
	}

	fmt.Fprintf(out, "  created      %s  %s\n", stamp.ID, root)
	fmt.Fprintf(out, "    subject    %s\n", stamp.Subject)
	fmt.Fprintf(out, "    notebook   %s  %s\n", destination.Name, destination.Root)
	if derived != "" {
		fmt.Fprintf(out, "    name       %s  (%s)\n", name, derived)
	}
	fmt.Fprintf(out, "  primary      %s  %s  — unchanged; machine.toml was not written\n", primary.Stamp.ID, primary.Root)
	fmt.Fprintf(out, "\n  Unqualified writes for this subject still land in the primary. `grove notespace primary %s` moves them here.\n\n", stamp.ID)

	evidence := transition.Evidence{
		Action: "notespace new",
		Counts: []transition.Count{
			{Name: "notespaces-created", Value: 1},
			{Name: "siblings-for-subject", Value: int64(len(scope.index.SiblingsFor(opts.subject, scope.primaries)) + 1)},
		},
		ResolvedRoots: []transition.ResolvedRoot{{Name: destination.Name, Declared: destination.Declared, Resolved: root}},
	}
	if opts.asJSON {
		return transition.RenderJSON(out, evidence)
	}
	return transition.RenderHuman(out, evidence)
}

// siblingName settles the new notespace's name and directory, returning the
// name and — when it was not the one asked for — how it was derived.
//
// An explicit name is never adjusted: an operator who names a notespace and
// gets a differently-named one has been given something they did not ask for.
// A DERIVED name is uniquified, because "same subject, same notebook" is a
// legal arrangement whose whole point is more than one directory.
func siblingName(explicit string, primary notespace.Record, taken map[string]bool) (string, string, error) {
	if name := strings.TrimSpace(explicit); name != "" {
		if err := validateNotespaceName(name); err != nil {
			return "", "", err
		}
		if taken[name] {
			return "", "", fmt.Errorf("this notebook already holds a notespace directory named %q; name the new one something else", name)
		}
		return name, "", nil
	}
	base := strings.TrimSpace(primary.Stamp.Name)
	if validateNotespaceName(base) != nil {
		base = filepath.Base(primary.Root)
	}
	if err := validateNotespaceName(base); err != nil {
		return "", "", fmt.Errorf("the primary's name cannot be reused (%w); pass --name", err)
	}
	if !taken[base] {
		return base, "", nil
	}
	for suffix := 2; suffix < 1000; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		if !taken[candidate] {
			return candidate, fmt.Sprintf("%q is taken in this notebook, so the name was uniquified", base), nil
		}
	}
	return "", "", fmt.Errorf("every uniquified name derived from %q is taken; pass --name", base)
}

// validateNotespaceName holds a name to what a notespace directory can be: one
// path component, no dot-prefix (the scanners skip those), and the stamp's own
// whitespace rules.
func validateNotespaceName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("notespace name is empty")
	case strings.TrimSpace(name) != name || strings.ContainsAny(name, "\r\n\t"):
		return fmt.Errorf("notespace name %q has surrounding or control whitespace", name)
	case name != filepath.Base(name) || strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/'):
		return fmt.Errorf("notespace name %q is not a single directory name", name)
	case name == "." || name == "..":
		return fmt.Errorf("notespace name %q is not a directory name", name)
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("notespace name %q starts with a dot; the notespace scanners would not see it", name)
	}
	return nil
}

// ---- primary ------------------------------------------------------------------

func newNotespacePrimaryCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "primary <notespace>",
		Short: "Record which notespace this machine writes into for a subject",
		Long: `Rewrite the recorded primary for one subject.

The primary is this machine's default target notespace for a subject — a
machine-local routing pointer recorded in config, never a synced property of the
notespace. Flipping it changes where unqualified writes land and nothing else:

  · no notes move, no directory is touched, and the old primary's content is
    left exactly as it is;
  · both notespaces keep their immutable ids, so history, cursors and the
    server's claims are unaffected;
  · exactly one [primaries] entry is rewritten — the subject stamped on the
    notespace you named, and no other.

<notespace> is matched against stamped ids first, then stamped display names,
then directory names, across every recorded notebook. An ambiguous match is
refused rather than resolved by sort order.

A subject whose recorded primary was deleted is repaired by this verb: naming a
surviving sibling records it, and doctor stops reporting the dangling entry.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNotespacePrimary(cmd.OutOrStdout(), args[0], asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Render the transition evidence as JSON")
	return cmd
}

func runNotespacePrimary(out io.Writer, want string, asJSON bool) error {
	scope, err := loadNotespaceScope()
	if err != nil {
		return err
	}
	target, owner, err := locateRecordedNotespace(scope.scanned, want)
	if err != nil {
		return err
	}
	if target.Stamp == nil {
		return fmt.Errorf("%s carries no %s; primariness is recorded against an immutable id and this notespace has none — `grove notebook share %s` mints one",
			target.Root, notespace.NotespaceStampName, owner.Name)
	}
	// The named notespace has to be reachable as itself before it can be
	// recorded as anything: a duplicated id is refused here rather than written
	// into [primaries], where it would make the routing pointer ambiguous.
	records, err := scope.index.ByID(target.Stamp.ID)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return fmt.Errorf("notespace %s is stamped at %s but absent from the stamp index; nothing was recorded", target.Stamp.ID, target.Root)
	}
	value := target.Stamp.Subject
	if err := subject.Validate(value); err != nil {
		return fmt.Errorf("%s is stamped with an invalid subject: %w", target.Root, err)
	}
	for recordedSubject, id := range scope.primaries {
		if id == target.Stamp.ID && recordedSubject != value {
			return fmt.Errorf("machine.toml already records notespace %s as the primary for subject %q, but it is stamped for %q; repair that entry first (`grove doctor`) — one notespace is the primary of at most one subject",
				id, recordedSubject, value)
		}
	}

	current := scope.primaries[value]
	if current == target.Stamp.ID {
		fmt.Fprintf(out, "  primary      %s\n", value)
		fmt.Fprintf(out, "    unchanged  %s  %s\n\n", target.Stamp.ID, target.Root)
		evidence := transition.Evidence{
			Action:        "notespace primary",
			Counts:        []transition.Count{{Name: "primaries-rewritten", Value: 0}},
			ResolvedRoots: []transition.ResolvedRoot{{Name: owner.Name, Declared: owner.Declared, Resolved: target.Root}},
			Reason:        transition.Reason("machine.toml already records this notespace as the primary for " + value),
		}
		if asJSON {
			return transition.RenderJSON(out, evidence)
		}
		return transition.RenderHuman(out, evidence)
	}

	// The write is a compare-and-swap on the whole file: the revision pins what
	// this decision was made against, and the callback re-checks the one entry
	// being replaced. A concurrent writer that changed either is a refusal, not
	// a last-writer-wins overwrite of somebody else's routing.
	path := config.MachineConfigPath()
	revision, err := config.MachineRevision(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	_, changed, err := config.EditMachineConfig(path, config.MachineEditOptions{
		ExpectedRevision:  revision,
		KnownNotespaceIDs: scope.knownIDs(),
	}, func(machine *config.MachineConfig) error {
		if got := machine.Primaries[value]; got != current {
			return fmt.Errorf("[primaries] %q was %q when this flip was decided and is %q now; nothing was rewritten", value, current, got)
		}
		if machine.Primaries == nil {
			machine.Primaries = map[string]string{}
		}
		machine.Primaries[value] = target.Stamp.ID
		return nil
	})
	if err != nil {
		return fmt.Errorf("record the primary for %q: %w", value, err)
	}
	if !changed {
		return fmt.Errorf("machine.toml was not rewritten and the primary for %q is still %q; nothing changed", value, current)
	}
	config.ResetLoadCache()

	fmt.Fprintf(out, "  primary      %s\n", value)
	if current == "" {
		fmt.Fprintf(out, "    was        (nothing recorded — this subject had no primary)\n")
	} else {
		fmt.Fprintf(out, "    was        %s  %s\n", current, describeRecordedNotespace(scope, current))
	}
	fmt.Fprintf(out, "    now        %s  %s  [notebook %s]\n", target.Stamp.ID, target.Root, owner.Name)
	fmt.Fprintf(out, "\n  Unqualified writes for this subject now land in %s.\n", target.Root)
	fmt.Fprintf(out, "  Nothing moved: no notes were copied and the previous primary's content is untouched.\n\n")

	evidence := transition.Evidence{
		Action:        "notespace primary",
		Counts:        []transition.Count{{Name: "primaries-rewritten", Value: 1}},
		ResolvedRoots: []transition.ResolvedRoot{{Name: owner.Name, Declared: owner.Declared, Resolved: target.Root}},
	}
	if asJSON {
		return transition.RenderJSON(out, evidence)
	}
	return transition.RenderHuman(out, evidence)
}

// describeRecordedNotespace names where an id lives, or says it is not here.
func describeRecordedNotespace(scope notespaceScope, id string) string {
	records, err := scope.index.ByID(id)
	if err != nil {
		return "(" + err.Error() + ")"
	}
	if len(records) == 0 {
		return "(no stamped root on this machine carries this id)"
	}
	return records[0].Root
}

// ---- list ---------------------------------------------------------------------

func newNotespaceListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List notespaces grouped by subject, primary first",
		Long: `Show every stamped notespace this machine records, grouped by subject.

The primary is this machine's default target notespace for a subject — a
machine-local routing pointer recorded in config, never a synced property of the
notespace. It is listed first in its group and marked; the parent notebook is
shown dimmed beside every sibling, because two notespaces for one subject are
told apart by where they live rather than by what they are called.

Nothing is inferred: a subject with no recorded primary, a recorded primary with
no stamp on this machine, and a directory with no stamp at all are each reported
as what they are, with the verb that repairs them.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNotespaceList(cmd.OutOrStdout(), asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Render the listing as JSON")
	return cmd
}

// notespaceListRow is one notespace in one subject group. There is deliberately
// no per-row primary flag: primariness is a relationship recorded per SUBJECT,
// so it is carried once, on the group, and the group's order reflects it.
type notespaceListRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Dir      string `json:"dir"`
	Notebook string `json:"notebook"`
	Root     string `json:"root"`
	Kind     string `json:"kind,omitempty"`
}

type notespaceListGroup struct {
	Subject string `json:"subject"`
	// PrimaryNotespaceID is the recorded routing pointer for this subject, or
	// "" when nothing is recorded. Rows are ordered primary first.
	PrimaryNotespaceID string             `json:"primary_notespace_id"`
	Notespaces         []notespaceListRow `json:"notespaces"`
}

type notespaceListing struct {
	Subjects []notespaceListGroup `json:"subjects"`
	Unminted []notespaceListRow   `json:"unminted,omitempty"`
	Problems []string             `json:"problems,omitempty"`
}

func runNotespaceList(out io.Writer, asJSON bool) error {
	scope, err := loadNotespaceScope()
	if err != nil {
		return err
	}
	listing := notespaceListing{}
	for _, value := range scope.index.Subjects() {
		group := notespaceListGroup{Subject: value, PrimaryNotespaceID: scope.primaries[value]}
		for _, record := range scope.index.SiblingsFor(value, scope.primaries) {
			group.Notespaces = append(group.Notespaces, scope.row(record))
		}
		listing.Subjects = append(listing.Subjects, group)
	}
	for _, entry := range scope.scanned {
		for _, ns := range entry.Notespaces {
			if ns.Stamp == nil {
				listing.Unminted = append(listing.Unminted, notespaceListRow{Dir: ns.Dir, Notebook: entry.Name, Root: ns.Root})
			}
		}
	}
	for _, problem := range scope.index.AuditPrimaries(scope.primaries) {
		listing.Problems = append(listing.Problems, problem.String())
	}
	for _, id := range scope.duplicateIDs() {
		listing.Problems = append(listing.Problems, "duplicate notespace id "+id+" (D8): repair with `grove doctor --fix --remint <notespace-root>`")
	}

	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(listing)
	}
	renderNotespaceListing(out, listing)
	return nil
}

func renderNotespaceListing(out io.Writer, listing notespaceListing) {
	muted := theme.DefaultTheme.Muted
	if len(listing.Subjects) == 0 && len(listing.Unminted) == 0 {
		fmt.Fprintln(out, "No stamped notespaces under any recorded notebook.")
		fmt.Fprintln(out, muted.Render("`grove notebook share <name>` mints ids for a notebook's notespaces; `grove migrate --step 2` does it for a migrated machine."))
		return
	}
	for _, group := range listing.Subjects {
		fmt.Fprintf(out, "%s\n", group.Subject)
		for _, row := range group.Notespaces {
			marker, label := " ", ""
			if row.ID == group.PrimaryNotespaceID {
				marker, label = "●", "primary"
			}
			fmt.Fprintf(out, "  %s %-24s %s  %-8s %s\n",
				marker, row.Name, row.ID, label, muted.Render(row.Notebook+"  "+row.Root))
		}
		if group.PrimaryNotespaceID == "" {
			fmt.Fprintf(out, "    %s\n", muted.Render("no recorded primary — `grove notespace primary <notespace>` records one"))
		}
		fmt.Fprintln(out)
	}
	if len(listing.Unminted) > 0 {
		fmt.Fprintln(out, "unminted directories (no .notespace.toml, so no identity and no routing)")
		for _, row := range listing.Unminted {
			fmt.Fprintf(out, "    %-24s %s\n", row.Dir, muted.Render(row.Notebook+"  "+row.Root))
		}
		fmt.Fprintln(out)
	}
	if len(listing.Problems) > 0 {
		fmt.Fprintln(out, "problems")
		for _, problem := range listing.Problems {
			fmt.Fprintf(out, "    %s\n", problem)
		}
		fmt.Fprintln(out)
	}
}

// ---- the shared read ------------------------------------------------------------

// notespaceScope is one fail-closed read of everything the sibling verbs reason
// over: the recorded notebook table, the scan beneath it, the stamp index over
// exactly those roots, and the recorded [primaries] table.
//
// It is one read on purpose. The verbs compare these facts against each other,
// so re-reading any of them mid-decision would let a verb act on two different
// versions of the machine.
type notespaceScope struct {
	table     coderoot.Table
	scanned   []recordedNotebook
	index     *notespace.Index
	primaries map[string]string
	// registryID is machine.toml's [sync.registry] binding, carried only so the
	// whole-file binding rule the config writer enforces can see it.
	registryID string
	// notebookOf maps a notespace root to the notebook recording it, so the
	// listing can show the parent without a second containment guess.
	notebookOf map[string]string
}

func loadNotespaceScope() (notespaceScope, error) {
	table, scanned, err := loadRecordedNotebooks()
	if err != nil {
		return notespaceScope{}, err
	}
	if err := refuseDuplicateNotebookIDs(scanned); err != nil {
		return notespaceScope{}, err
	}
	var roots []string
	notebookOf := map[string]string{}
	for _, entry := range scanned {
		for _, ns := range entry.Notespaces {
			roots = append(roots, ns.Root)
			notebookOf[ns.Root] = entry.Name
		}
	}
	// BuildIndex loads exactly these roots and refuses a malformed stamp; it
	// discovers nothing of its own.
	index, err := notespace.BuildIndex(roots)
	if err != nil {
		return notespaceScope{}, err
	}
	machineCfg, err := config.LoadMachineConfig()
	if err != nil {
		return notespaceScope{}, fmt.Errorf("read machine.toml: %w", err)
	}
	scope := notespaceScope{table: table, scanned: scanned, index: index, notebookOf: notebookOf}
	if machineCfg != nil {
		scope.primaries = machineCfg.Primaries
		scope.registryID = registryNotespaceID(machineCfg)
	}
	return scope, nil
}

func registryNotespaceID(machineCfg *config.MachineConfig) string {
	if machineCfg == nil || machineCfg.Sync.Registry == nil {
		return ""
	}
	return machineCfg.Sync.Registry.NotespaceID
}

func (s notespaceScope) row(record notespace.Record) notespaceListRow {
	return notespaceListRow{
		ID:       record.Stamp.ID,
		Name:     record.Stamp.Name,
		Dir:      filepath.Base(record.Root),
		Notebook: s.notebookOf[record.Root],
		Root:     record.Root,
		Kind:     record.Stamp.Kind,
	}
}

// knownIDs is the stamp index the machine-config writer validates the whole
// file against.
//
// It is the stamps on disk WIDENED by what machine.toml already references,
// which is the same choice `notebook share` makes and for the same reason: the
// rule is whole-file, so a single unrelated dangling entry elsewhere would
// otherwise block an edit that does not touch it. Doctor reports that drift;
// this verb refuses to be the place it surfaces.
func (s notespaceScope) knownIDs() map[string]struct{} {
	known := map[string]struct{}{}
	for _, record := range s.index.Records() {
		known[record.Stamp.ID] = struct{}{}
	}
	for _, id := range s.primaries {
		known[id] = struct{}{}
	}
	if s.registryID != "" {
		known[s.registryID] = struct{}{}
	}
	return known
}

// duplicateIDs lists ids carried by more than one physical root (D8).
func (s notespaceScope) duplicateIDs() []string {
	roots := map[string]int{}
	for _, record := range s.index.Records() {
		roots[record.Stamp.ID]++
	}
	var out []string
	for id, count := range roots {
		if count > 1 {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// recordedSubjectsHint lists the subjects this machine actually holds, so a
// mistyped subject is answered with the real ones rather than a grammar lesson.
func (s notespaceScope) recordedSubjectsHint() string {
	subjects := s.index.Subjects()
	if len(subjects) == 0 {
		return "this machine records no stamped notespaces yet; `grove notebook share <name>` mints them"
	}
	return "recorded subjects: " + strings.Join(subjects, ", ")
}
