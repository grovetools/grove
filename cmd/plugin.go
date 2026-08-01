package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/grovetools/grove/pkg/plugin"
)

// `grove plugin` is the distribution layer for treemux sidecar panels: the CLI
// that drives grove's existing clone, build and config seams in order, pins
// what it installed, and asks the one question that has to be asked before a
// stranger's process joins your TUI.
//
// The consent screen below is the product. Everything else is plumbing.

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
  grove plugin update foo
  grove plugin remove foo

Installs pin an exact commit. A ref is resolved once, at install time, and
recorded in ~/.config/grove/plugins/` + plugin.LockFileName + `; nothing floats,
and 'update' is the only thing that moves a pin.

Installing asks first. Before anything is built or written, grove shows the
build command, the command treemux will run at every start, the environment,
and the hotkeys the panel intends to claim — and records your approval against
that exact pin in the same trust store 'grove config trust' uses. Approving one
version is not approving the next one; 'update' shows a diff and asks again.`,
	}
	cmd.AddCommand(newPluginInstallCmd())
	cmd.AddCommand(newPluginListCmd())
	cmd.AddCommand(newPluginUpdateCmd())
	cmd.AddCommand(newPluginRemoveCmd())
	return cmd
}

func newPluginInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install <source>[@ref]",
		Short: "Install a panel from a git repository",
		Long: `Clone a plugin repository at a pinned ref, build it, and declare its panel.

The source is a git repository containing a ` + plugin.ManifestFile + ` at its
root:

  grove plugin install github.com/user/grove-panel-foo@v1.2.0
  grove plugin install https://github.com/user/grove-panel-foo --ref main
  grove plugin install ~/src/my-panel@v0.1.0

The ref is resolved to an exact commit and recorded. Re-running install with
the same pin does nothing.`,
		Args: cobra.ExactArgs(1),
		RunE: runPluginInstall,
	}
	cmd.Flags().String("ref", "", "Tag, branch or commit to install (overrides @ref in the source)")
	cmd.Flags().Bool("yes", false, "Approve the install without prompting")
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

	in := newInstaller(yes, jsonOutput)
	res, err := in.Install(cmd.Context(), args[0], plugin.Options{Ref: ref, Force: force})
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
		statuses, err := plugin.List()
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

func runPluginList(cmd *cobra.Command, _ []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	statuses, err := plugin.List()
	if err != nil {
		return err
	}
	if jsonOutput {
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
				"approved": st.Approved,
				"intact":   st.FragmentPresent && st.BinaryPresent,
			})
		}
		return printJSON(out)
	}

	if len(statuses) == 0 {
		fmt.Println("No plugins installed.")
		fmt.Println()
		fmt.Println("    grove plugin install github.com/user/grove-panel-foo@v1.2.0")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSOURCE\tPINNED\tPROTOCOL\tSTATE")
	var broken []plugin.Status
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
		}
		if state != "ok" {
			broken = append(broken, st)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", st.Name, st.Pin.Consent.Source, shortCommit(st.Pin.Commit), protocol, state)
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
	fmt.Fprintf(out, "  commit    %s\n", f.Commit)
	if f.Homepage != "" {
		fmt.Fprintf(out, "  home      %s\n", f.Homepage)
	}

	if req.IsUpdate() {
		fmt.Fprintln(out)
		if len(req.Changes) == 0 {
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
		fmt.Fprintln(out, "  grove will run this once, in the checkout, to build it:")
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

	if f.Protocol != "" || len(f.Keys) > 0 || len(f.Views) > 0 {
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

	if len(req.Unknown) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  This grove does not understand these manifest keys, and ignores them:\n      %s\n", strings.Join(req.Unknown, ", "))
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "  It will write:")
	fmt.Fprintf(out, "      %s\n", req.FragmentPath)
	fmt.Fprintf(out, "      %s\n", req.BinaryPath)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Approving covers this commit only. It is recorded in grove's trust store")
	fmt.Fprintln(out, "  (`grove config trust --list`); an update asks again with a diff.")
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

// shortCommit abbreviates a commit for the list table.
func shortCommit(c string) string {
	if len(c) >= 12 {
		return c[:12]
	}
	return c
}
