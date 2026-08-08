package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	coreplugin "github.com/grovetools/core/pkg/plugin"
	"github.com/grovetools/grove/pkg/plugin"
)

// `grove plugin` is the distribution layer for treemux sidecar panels: the CLI
// that drives grove's existing clone, build and config seams in order, pins
// what it installed, and asks the one question that has to be asked before a
// stranger's process joins your TUI.
//
// The consent screen below is the product. Everything else is plumbing.
//
// Two imports, one subject: `coreplugin` is the read side — the lockfile, the
// manifest schema, what is installed and whether it is intact — which lives in
// core so treemux can read it without importing grove. `plugin` is the install
// pipeline that writes all of it, which only grove has.

func newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Install treemux panels from a git repository",
		Long: `Install, update and remove treemux sidecar panels.

A plugin is a program plus a manifest. It runs in its own pane, it can speak
the embed/v1 control plane (focus, workspace scope, theme, deep links, key
claims), and nothing recompiles to add one.

  grove plugin install github.com/user/grove-panel-foo@v1.2.0
  grove plugin list
  grove plugin outdated
  grove plugin set foo work_minutes=30
  grove plugin update foo
  grove plugin remove foo

Installs pin an exact commit. A ref is resolved once, at install time, and
recorded in ~/.config/grove/plugins/` + coreplugin.LockFileName + `; nothing floats,
and 'update' is the only thing that moves a pin.

Installing asks first. Before anything is built or written, grove shows the
build command, the command treemux will run at every start, the environment,
and the hotkeys the panel intends to claim — and records your approval against
that exact pin in the same trust store 'grove config trust' uses. Approving one
version is not approving the next one; 'update' shows a diff and asks again.

'outdated' asks the same question without answering it destructively: it reads
the remote and touches nothing. 'set' is how an installed panel's own settings
are changed — grove owns the file they live in, so grove makes the edit and
re-records the approval against it.`,
	}
	cmd.AddCommand(newPluginInstallCmd())
	cmd.AddCommand(newPluginListCmd())
	cmd.AddCommand(newPluginOutdatedCmd())
	cmd.AddCommand(newPluginSetCmd())
	cmd.AddCommand(newPluginUpdateCmd())
	cmd.AddCommand(newPluginRemoveCmd())
	return cmd
}

func newPluginInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install <source>[@ref]",
		Short: "Install a panel from a git repository",
		Long: `Clone a plugin repository at a pinned ref, build it, and declare its panel.

The source is a git repository containing a ` + coreplugin.ManifestFile + ` at its
root:

  grove plugin install github.com/user/grove-panel-foo@v1.2.0
  grove plugin install https://github.com/user/grove-panel-foo --ref main
  grove plugin install ~/src/my-panel@v0.1.0

The ref is resolved to an exact commit and recorded. Re-running install with
the same pin does nothing.

DEVELOPMENT INSTALLS

  grove plugin install --dev ~/Code/grove-plugins/grove-panel-foo

--dev installs a panel you are writing. It builds the directory IN PLACE
instead of cloning it, so the build sees whatever workspace you develop in —
a go.work, a replace, an unpublished sibling module — exactly as your own
` + "`go build`" + ` does. A normal install builds in a managed checkout where none
of that applies, which is why a panel depending on an unreleased module can
be built by hand but not installed.

Nothing is pinned. The approval covers a directory, not a commit, and
` + "`grove plugin update <name>`" + ` rebuilds whatever is in it at the time. Removing
a dev install never deletes your source.`,
		Args: cobra.ExactArgs(1),
		RunE: runPluginInstall,
	}
	cmd.Flags().String("ref", "", "Tag, branch or commit to install (overrides @ref in the source)")
	cmd.Flags().Bool("yes", false, "Approve the install without prompting")
	cmd.Flags().Bool("dev", false, "Build a local directory in place, unpinned, for panel development")
	cmd.Flags().Bool("force", false, "Reinstall even when the pinned commit is already installed")
	cmd.Flags().Bool("json", false, "Output as JSON")
	return cmd
}

func newPluginListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed panels and their pins",
		Args:  cobra.NoArgs,
		RunE:  runPluginList,
	}
	cmd.Flags().Bool("json", false, "Output as JSON")
	return cmd
}

func newPluginOutdatedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "outdated [name...]",
		Short: "Check whether a panel's pinned ref has moved",
		Long: `Ask each panel's remote whether the ref it is pinned to names a different
commit now.

Read-only, and read-only in the way that matters: it asks with ` + "`git ls-remote`" + `
and touches no checkout, no binary and no pin. Nothing is fetched, nothing is
built, and nothing moves — ` + "`grove plugin update <name>`" + ` is what moves a pin,
and it asks first. So this is safe to run while the panels are running.

  NAME  PINNED        LATEST        STATE
  hn    274ca8258f11  9f0c1a2b3d4e  outdated

  current      the ref still names the pinned commit
  outdated     the ref names something else now
  unreachable  the remote could not be asked — offline, private, renamed
  dev          a development install: built from a working tree, nothing pinned

A remote that cannot be reached is reported, not raised. One unreachable plugin
must not fail the check for the rest, so the exit status is zero unless the
command itself was used wrongly.`,
		RunE: runPluginOutdated,
	}
	cmd.Flags().Bool("json", false, "Output as JSON")
	return cmd
}

func newPluginSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <name> <key=value>...",
		Short: "Change an installed panel's settings",
		Long: `Change the settings of a panel installed by ` + "`grove plugin`" + `.

  grove plugin set breaktimer work_minutes=30
  grove plugin set hn feed.limit=50 feed.refresh=10m

Settings are the panel's own options — the [panel.settings] table its manifest
declares, handed to it over the control plane and re-delivered live when the
config reloads. Name them with the dotted paths the install screen showed.

Why a command rather than a line in your own config: [tui.plugins] merges one
ENTRY at a time, so a later layer setting one option replaces the whole panel
entry, ` + "`command`" + ` and all, instead of adding to it. The installed fragment is
the only place these can live, and it is a file grove owns — the values in it
are part of what your install approval is bound to. So grove makes the edit and
re-records the approval against what it wrote.

Values are read as the type the panel declared: 25 stays a number, true stays a
boolean, "2s" is checked as a duration. A name the panel does not declare is
refused unless --new says to add it anyway.

Nothing is prompted — these are your settings, in a layer you own — but the
change is printed as the same diff an update would show.`,
		Args: cobra.MinimumNArgs(2),
		RunE: runPluginSet,
	}
	cmd.Flags().Bool("new", false, "Add a setting the panel's manifest does not declare")
	cmd.Flags().Bool("json", false, "Output as JSON")
	return cmd
}

func newPluginUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update [name...]",
		Short: "Move a panel's pin to what its ref names now",
		Long: `Re-resolve an installed panel's ref and move the pin to it.

With no arguments, every installed panel is considered. A panel pinned to a tag
does not move until the tag does — updating is explicit, and it re-asks with a
diff of what changed since you approved the current version.`,
		RunE: runPluginUpdate,
	}
	cmd.Flags().String("ref", "", "Move the pin to this tag, branch or commit instead of the recorded ref")
	cmd.Flags().Bool("yes", false, "Approve the update without prompting")
	cmd.Flags().Bool("force", false, "Rebuild and reinstall even when nothing changed")
	cmd.Flags().Bool("json", false, "Output as JSON")
	return cmd
}

func newPluginRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"uninstall"},
		Short:   "Uninstall a panel",
		Long: `Remove a panel: its rail item, its binary, its checkout, its pin, and the
install approval recorded for it.`,
		Args: cobra.ExactArgs(1),
		RunE: runPluginRemove,
	}
	cmd.Flags().Bool("keep-source", false, "Leave the checkout on disk")
	cmd.Flags().Bool("json", false, "Output as JSON")
	return cmd
}

func runPluginInstall(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	ref, _ := cmd.Flags().GetString("ref")
	yes, _ := cmd.Flags().GetBool("yes")
	force, _ := cmd.Flags().GetBool("force")
	dev, _ := cmd.Flags().GetBool("dev")

	in := newInstaller(yes, jsonOutput)
	res, err := in.Install(cmd.Context(), args[0], plugin.Options{Ref: ref, Force: force, Dev: dev})
	if err != nil {
		return pluginErr(err)
	}
	return reportPluginResult(res, jsonOutput)
}

func runPluginUpdate(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	ref, _ := cmd.Flags().GetString("ref")
	yes, _ := cmd.Flags().GetBool("yes")
	force, _ := cmd.Flags().GetBool("force")

	names := args
	if len(names) == 0 {
		statuses, err := coreplugin.List()
		if err != nil {
			return err
		}
		for _, st := range statuses {
			names = append(names, st.Name)
		}
		if len(names) == 0 {
			if jsonOutput {
				return printJSON([]any{})
			}
			fmt.Println("No plugins installed.")
			return nil
		}
	}
	if ref != "" && len(names) > 1 {
		return fmt.Errorf("--ref moves one plugin's pin — name the plugin it applies to")
	}

	in := newInstaller(yes, jsonOutput)
	results := make([]map[string]string, 0, len(names))
	for _, name := range names {
		res, err := in.Update(cmd.Context(), name, plugin.Options{Ref: ref, Force: force})
		if err != nil {
			if errors.Is(err, plugin.ErrDeclined) && len(names) > 1 {
				// One declined update must not abandon the rest.
				fmt.Fprintf(os.Stderr, "%s: not updated (declined)\n", name)
				continue
			}
			return pluginErr(err)
		}
		results = append(results, map[string]string{
			"name": res.Name, "action": res.Action,
			"commit": res.Pin.Commit, "source": res.Pin.Consent.Source,
		})
		if !jsonOutput && res.Action != "unchanged" {
			fmt.Printf("\n%s %s (%s)\n", res.Action, res.Name, res.Pin.Consent.Source)
		}
	}
	if jsonOutput {
		return printJSON(results)
	}
	return nil
}

func runPluginOutdated(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	reports, err := plugin.Outdated(cmd.Context(), args)
	if err != nil {
		return err
	}

	if jsonOutput {
		out := make([]map[string]any, 0, len(reports))
		for _, r := range reports {
			row := map[string]any{
				"name":   r.Name,
				"state":  string(r.State),
				"url":    r.Pin.URL,
				"ref":    r.Pin.Ref,
				"dev":    r.Pin.Dev,
				"pinned": r.Pin.Commit,
				"latest": r.Latest,
			}
			if r.Reason != "" {
				row["reason"] = r.Reason
			}
			out = append(out, row)
		}
		return printJSON(out)
	}

	if len(reports) == 0 {
		fmt.Println("No plugins installed.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPINNED\tLATEST\tSTATE")
	for _, r := range reports {
		// A dev entry's recorded commit is the HEAD its working tree happened to
		// be on at install time. Printing it under PINNED would read as a pin,
		// and there is nothing here for a remote to be ahead of.
		pinned := shortCommit(r.Pin.Commit)
		if r.Pin.Dev {
			pinned = "—"
		}
		latest := shortCommit(r.Latest)
		if latest == "" {
			latest = "—"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Name, pinned, latest, r.State)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, r := range reports {
		switch r.State {
		case plugin.StateOutdated:
			fmt.Println()
			fmt.Printf("%s: %s names %s now. Review what changed and move the pin with\n    grove plugin update %s\n",
				r.Name, refLabel(r.Pin.Ref), shortCommit(r.Latest), r.Name)
		case plugin.StateUnreachable:
			fmt.Println()
			fmt.Printf("%s: %s\n    The pin is untouched; this check could not reach the remote to compare it.\n", r.Name, r.Reason)
		}
	}
	return nil
}

func runPluginSet(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	allowNew, _ := cmd.Flags().GetBool("new")

	in := &plugin.Installer{Out: os.Stdout}
	res, err := in.Set(args[0], args[1:], allowNew)
	if err != nil {
		return err
	}

	if jsonOutput {
		changes := make([]map[string]string, 0, len(res.Changes))
		for _, c := range res.Changes {
			changes = append(changes, map[string]string{"field": c.Field, "old": c.Old, "new": c.New})
		}
		return printJSON(map[string]any{
			"name":           res.Name,
			"fragment":       res.Pin.Fragment,
			"consent_digest": res.Pin.ConsentDigest,
			"settings":       res.Pin.Consent.Settings,
			"changes":        changes,
		})
	}

	if len(res.Changes) == 0 {
		fmt.Printf("%s already had those values; nothing changed.\n", res.Name)
		return nil
	}
	fmt.Println()
	for _, c := range res.Changes {
		fmt.Printf("  %-28s - %s\n", c.Field, valueOrNone(c.Old))
		fmt.Printf("  %-28s + %s\n", "", valueOrNone(c.New))
	}
	fmt.Println()
	fmt.Printf("Wrote %s and re-recorded the install approval against it.\n", res.Pin.Fragment)
	fmt.Println("treemux delivers the new settings on its next config reload.")
	return nil
}

func runPluginList(cmd *cobra.Command, _ []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	statuses, err := coreplugin.List()
	if err != nil {
		return err
	}
	if jsonOutput {
		return printJSON(pluginListRows(statuses))
	}

	if len(statuses) == 0 {
		fmt.Println("No plugins installed.")
		fmt.Println()
		fmt.Println("    grove plugin install github.com/user/grove-panel-foo@v1.2.0")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSOURCE\tPINNED\tPROTOCOL\tSTATE")
	var broken []coreplugin.Status
	for _, st := range statuses {
		protocol := st.Pin.Consent.Protocol
		if protocol == "" {
			protocol = "pty"
		}
		state := "ok"
		switch {
		case !st.FragmentPresent:
			state = "no rail item"
		case !st.BinaryPresent:
			state = "no binary"
		case !st.Approved:
			state = "edited"
		case st.Pin.Dev:
			// Reported last so a genuinely broken dev install still shows what
			// is broken. "dev" is not a fault, but it is never "ok" either:
			// the binary on disk came from a directory that has very likely
			// moved on since, and the PINNED column below is showing a commit
			// that nothing is actually pinned to.
			state = "dev"
		}
		// "dev" is a mode, not a fault, so it does not join the broken list —
		// there is no remedy to print for it.
		if state != "ok" && state != "dev" {
			broken = append(broken, st)
		}
		// The PINNED column would otherwise print the HEAD recorded at install
		// time, which reads as a pin and is not one.
		pinned := shortCommit(st.Pin.Commit)
		if st.Pin.Dev {
			pinned = "—"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", st.Name, st.Pin.Consent.Source, pinned, protocol, state)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, st := range broken {
		fmt.Println()
		switch {
		case !st.FragmentPresent:
			fmt.Printf("%s: %s is gone, so the rail item is not declared. Reinstall it with\n    grove plugin update %s --force\n", st.Name, st.Pin.Fragment, st.Name)
		case !st.BinaryPresent:
			fmt.Printf("%s: %s is gone, so the pane would fail to start. Rebuild it with\n    grove plugin update %s --force\n", st.Name, st.Pin.Binary, st.Name)
		case !st.Approved:
			fmt.Printf("%s: %s no longer matches the approval recorded for this pin —\n    it was edited outside `grove plugin`. Review it, then re-approve with\n    grove plugin update %s --force\n", st.Name, st.Pin.Fragment, st.Name)
		}
	}
	return nil
}

func runPluginRemove(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	keepSource, _ := cmd.Flags().GetBool("keep-source")

	in := &plugin.Installer{Out: os.Stdout}
	removed, err := in.Remove(args[0], keepSource)
	if err != nil {
		return err
	}
	if jsonOutput {
		return printJSON(map[string]any{"name": args[0], "removed": removed})
	}
	fmt.Printf("Removed %s\n", args[0])
	for _, p := range removed {
		fmt.Printf("  %s\n", p)
	}
	fmt.Println()
	fmt.Println("treemux drops the rail item on its next config reload.")
	return nil
}

// pluginListRows renders `list --json`.
//
// It carries everything the lockfile holds rather than a summary of it, because
// the reader is as often a program as a person: the Plugins panel answers "when
// was this installed, where is its source, what did it declare, is the approval
// still the one that covers it" from this document, and a panel that had to
// parse the lockfile itself to fill in the gaps would be reading grove's private
// state behind its back.
//
// The keys the first version shipped keep their names and their meanings.
// Everything below them is additive.
func pluginListRows(statuses []coreplugin.Status) []map[string]any {
	out := make([]map[string]any, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, map[string]any{
			"name":     st.Name,
			"source":   st.Pin.Consent.Source,
			"ref":      st.Pin.Ref,
			"commit":   st.Pin.Commit,
			"binary":   st.Pin.Binary,
			"fragment": st.Pin.Fragment,
			"protocol": st.Pin.Consent.Protocol,
			"dev":      st.Pin.Dev,
			"approved": st.Approved,
			"intact":   st.FragmentPresent && st.BinaryPresent,

			"installed_at":    st.Pin.InstalledAt,
			"source_dir":      st.Pin.SourceDir,
			"version_binary":  st.Pin.VersionBinary,
			"manifest_digest": st.Pin.ManifestDigest,
			"consent_digest":  st.Pin.ConsentDigest,
			// The whole approval snapshot: what the user was shown and what the
			// digest above is over. A reader comparing declared against observed
			// — the keys a panel claimed versus the keys it claims at runtime —
			// needs the declaration, and this is where it is recorded.
			"consent": st.Pin.Consent,
			// What the installed binary was actually BUILT from, which the pin
			// cannot say. Empty when the bin dir entry is missing or is not one
			// of grove's version links.
			"built_commit": st.Pin.BuiltCommit(),
		})
	}
	return out
}

// newInstaller wires the consent gate. yes approves without prompting, which
// is the only way an install can proceed without a terminal.
func newInstaller(yes, jsonOutput bool) *plugin.Installer {
	var out io.Writer = os.Stdout
	if jsonOutput {
		// Progress lines would corrupt the JSON document.
		out = os.Stderr
	}
	return &plugin.Installer{
		Out: out,
		Confirm: func(req *plugin.ConsentRequest) (bool, error) {
			printConsent(req, out)
			if yes {
				fmt.Fprintln(out, "Approved by --yes.")
				return true, nil
			}
			// satelliteStdinIsTTY is grove's existing "can a [y/N] prompt be
			// answered at all?" check; a prompt nobody can answer must name
			// the flag that skips it rather than silently failing.
			if !satelliteStdinIsTTY() {
				return false, fmt.Errorf("this install needs approval and stdin is not a terminal — re-run with --yes once you have read the above")
			}
			verb := "Install"
			if req.IsUpdate() {
				verb = "Update"
			}
			return confirmYesNo(fmt.Sprintf("%s %s?", verb, req.Facts.Name)), nil
		},
	}
}

// printConsent renders the install-time trust screen: everything the approval
// binds to, in the order that matters — what runs, what it may take over, and
// what lands on disk.
func printConsent(req *plugin.ConsentRequest, out io.Writer) {
	f := req.Facts
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  plugin    %s — %s\n", f.Name, f.Description)
	fmt.Fprintf(out, "  source    %s\n", f.Source)
	if f.Dev {
		// The commit line would read as a pin. What is actually being approved
		// here is a directory, and that difference is the single most important
		// thing on this screen — every other fact below is read from a manifest
		// that can be edited a second after this prompt is answered.
		if f.Commit != "" {
			fmt.Fprintf(out, "  head      %s (right now — not a pin)\n", f.Commit)
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  ⚠ DEVELOPMENT INSTALL — this approval covers a directory, not a commit.")
		fmt.Fprintln(out, "    The panel is built in place, so it picks up your workspace (go.work,")
		fmt.Fprintln(out, "    replaces, unpublished siblings) the way your own build does. Every")
		fmt.Fprintln(out, "    rebuild runs whatever that directory contains at the time, including")
		fmt.Fprintln(out, "    changes nobody has reviewed. Approve it for code you are writing.")
	} else {
		fmt.Fprintf(out, "  commit    %s\n", f.Commit)
	}
	if f.Homepage != "" {
		fmt.Fprintf(out, "  home      %s\n", f.Homepage)
	}

	if req.IsUpdate() {
		fmt.Fprintln(out)
		if len(req.Changes) == 0 && f.Dev {
			// There is no pin to move. What changed is the source, which this
			// screen cannot show and does not cover.
			fmt.Fprintln(out, "  Nothing in the manifest has changed. The source may have — a")
			fmt.Fprintln(out, "  development install rebuilds the directory either way.")
		} else if len(req.Changes) == 0 {
			fmt.Fprintln(out, "  Nothing you approved has changed; only the pin moves.")
		} else {
			fmt.Fprintf(out, "  Changed since you approved %s:\n", req.Previous.Source)
			for _, c := range req.Changes {
				fmt.Fprintf(out, "    %-9s - %s\n", c.Field, valueOrNone(c.Old))
				fmt.Fprintf(out, "    %-9s + %s\n", "", valueOrNone(c.New))
			}
		}
	}

	fmt.Fprintln(out)
	if len(f.Build) > 0 {
		if f.Dev {
			// Neither "once" nor "in the checkout" is true here, and both
			// matter: it runs in the user's own directory, every rebuild.
			fmt.Fprintf(out, "  grove will run this in %s to build it,\n", req.SourceDir)
			fmt.Fprintln(out, "  now and on every rebuild:")
		} else {
			fmt.Fprintln(out, "  grove will run this once, in the checkout, to build it:")
		}
		fmt.Fprintf(out, "      %s\n", strings.Join(f.Build, " "))
	} else {
		fmt.Fprintln(out, "  There is no build step: the repository ships the program itself.")
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "  treemux will run this every time it starts, as you:")
	fmt.Fprintf(out, "      %s\n", strings.Join(f.Run, " "))
	for _, e := range f.Env {
		fmt.Fprintf(out, "      with %s\n", e)
	}

	if f.Protocol != "" || len(f.Keys) > 0 || len(f.Views) > 0 || f.DigestDescription != "" || f.NotebookSubtree != "" {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  The panel declares:")
		if f.Protocol != "" {
			fmt.Fprintf(out, "      protocol  %s — it gets a control socket: focus, workspace scope,\n", f.Protocol)
			fmt.Fprintln(out, "                          theme, icon set, pane size, its settings, deep links")
			fmt.Fprintln(out, "                          into other panels, and key claims")
		}
		for _, k := range f.Keys {
			fmt.Fprintf(out, "      key       %s (while it holds focus, if the host allows it)\n", k)
		}
		// Views sit in this block rather than the settings block below because
		// they are a declaration and not a knob: the user does not edit this
		// list, they choose one entry from it when they mount the panel.
		for _, v := range f.Views {
			fmt.Fprintf(out, "      view      %s\n", v)
		}
		// Beside the views, because it is the third answer to "what does this
		// panel draw, and where" — and the only one whose answer is somewhere
		// the panel is not running. Worth its own sentence rather than a marker
		// on the view lines: a digest is drawn in slots the user never mounted
		// this panel in, which is the part they are actually approving.
		if f.DigestDescription != "" {
			fmt.Fprintf(out, "      digest    publishes a one-line summary other panes can show — %s\n", f.DigestDescription)
		}
		// The notebook line is a disclosure, not a mount: the host neither
		// creates nor guards the subtree, it only makes sure the user has read
		// the author's claim about their notebook before approving the process
		// that will act on it.
		if f.NotebookSubtree != "" {
			fmt.Fprintf(out, "      notebook  writes under %s/ in your notebook — %s\n", f.NotebookSubtree, f.NotebookDescription)
		}
	}

	// Settings get their own block rather than a line in the one above,
	// because they are the only thing on this screen the user is expected to
	// go on to EDIT. grove never runs them — the host forwards the table and
	// never interprets it — but they decide what the panel does on first run,
	// so approving without reading them is approving a behavior sight unseen.
	if len(f.Settings) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Its settings start out as (yours to change afterwards, in the file below):")
		for _, s := range f.Settings {
			fmt.Fprintf(out, "      %s\n", s)
		}
	}

	// In the settings block rather than the declarations block above, because
	// unlike the views these describe a knob the user is expected to turn: the
	// values a config UI will offer for the settings just listed. They are a
	// DECLARATION all the same — a value outside the list is still writable —
	// so what is being read here is what the panel says it understands.
	if len(f.SettingOptions) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Some of those take one of a declared set of values:")
		for _, s := range f.SettingOptions {
			fmt.Fprintf(out, "      %s\n", s)
		}
	}

	if len(req.Unknown) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  This grove does not understand these manifest keys, and ignores them:\n      %s\n", strings.Join(req.Unknown, ", "))
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "  It will write:")
	fmt.Fprintf(out, "      %s\n", req.FragmentPath)
	fmt.Fprintf(out, "      %s\n", req.BinaryPath)
	fmt.Fprintln(out)
	if f.Dev {
		fmt.Fprintln(out, "  Approving covers the DIRECTORY above, not a commit — that is what makes")
		fmt.Fprintln(out, "  this a development install. It is recorded in grove's trust store")
		fmt.Fprintln(out, "  (`grove config trust --list`); a rebuild does not ask again.")
	} else {
		fmt.Fprintln(out, "  Approving covers this commit only. It is recorded in grove's trust store")
		fmt.Fprintln(out, "  (`grove config trust --list`); an update asks again with a diff.")
	}
	fmt.Fprintln(out)
}

// reportPluginResult prints the outcome of an install.
func reportPluginResult(res *plugin.Result, jsonOutput bool) error {
	if jsonOutput {
		return printJSON(map[string]any{
			"name": res.Name, "action": res.Action,
			"source": res.Pin.Consent.Source, "commit": res.Pin.Commit,
			"binary": res.Pin.Binary, "fragment": res.Pin.Fragment,
		})
	}
	if res.Action == "unchanged" {
		return nil
	}
	fmt.Println()
	fmt.Printf("%s %s (%s)\n", res.Action, res.Name, res.Pin.Consent.Source)
	fmt.Println()
	fmt.Println("treemux picks it up on its next config reload — the rail item appears")
	fmt.Println("without a restart. If treemux is not running, it is there next time.")
	return nil
}

// pluginErr keeps a declined install from being reported as a failure of the
// machinery. Nothing went wrong; the user said no.
func pluginErr(err error) error {
	if errors.Is(err, plugin.ErrDeclined) {
		fmt.Println("Not installed. Nothing was built, written or trusted.")
		return nil
	}
	return err
}

func valueOrNone(v string) string {
	if v == "" {
		return "(none)"
	}
	return v
}

// refLabel names the ref a pin follows, so a message about a pin that follows
// the remote's default branch does not read as a message about an empty string.
func refLabel(ref string) string {
	if ref == "" {
		return "the default branch"
	}
	return ref
}

// shortCommit abbreviates a commit for the list table.
func shortCommit(c string) string {
	if len(c) >= 12 {
		return c[:12]
	}
	return c
}
