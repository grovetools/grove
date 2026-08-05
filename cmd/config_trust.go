package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/exectrust"
	"github.com/grovetools/core/pkg/worktreeregistry"
	"github.com/spf13/cobra"
)

// newConfigTrustCmd builds the `grove config trust` subcommand: the review-
// and-allow UX for the exec-provenance gate in core/config. The gate strips
// shell commands that reach the merged config from a cloned repository's
// grove.toml; this command shows exactly what is being withheld and lets the
// user allow it after reading it.
func newConfigTrustCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trust",
		Short: "Review and trust a workspace's exec-bearing config",
		Long: `Show every shell command a workspace's config layers would run — and every
setting that would hand an agent the capability to run one — and decide whether
to allow them.

Grove's config cascade merges grove.toml files that come out of cloned
repositories. Keys like [[hooks.on_stop]], [tui.plugins], [tui.panels.bindings],
[commands] and [[daemon.hooks.on_skill_sync]] carry commands grove executes on
its own — when a session stops, when the TUI boots, on a keypress, when a verb
fans out across every workspace — so grove withholds them until you have read
them.

[claude] is withheld the same way. It is not a command, but grove propagates it
into the worktree's .claude/settings.local.json — permission rules, the sandbox
boundary, auto-approval, folder trust — so a repo that sets it chooses what the
next agent session in that worktree is allowed to do.

Bare 'grove config trust' is report-only. --yes records the decision, keyed to
a digest of exactly the commands shown: if the repo later edits or adds one,
the gate closes again and you are asked about the new content.

Trust is keyed to a config file's PATH, so a worktree checkout of a repo you
already reviewed starts out untrusted again. --inherit relocates that existing
decision onto every registered worktree whose copy of a repo's grove.toml
carries byte-identical exec values. A worktree whose branch CHANGED them keeps
the gate shut and is listed as differing, so nothing runs unreviewed.

  grove config trust            review what would be enabled
  grove config trust --yes      allow the config files in scope
  grove config trust --revoke   withdraw trust for the files in scope
  grove config trust --list     list every config file you have trusted
  grove config trust --inherit  preview worktree trust inherited from owners
  grove config trust --inherit --yes   record it`,
		RunE: runConfigTrust,
	}
	cmd.Flags().Bool("yes", false, "Trust the config files in scope")
	cmd.Flags().Bool("revoke", false, "Withdraw trust for the config files in scope")
	cmd.Flags().Bool("list", false, "List every trusted config file")
	cmd.Flags().Bool("inherit", false, "Relocate owner-checkout trust onto registered worktrees with identical exec config")
	cmd.Flags().Bool("json", false, "Output as JSON")
	return cmd
}

func runConfigTrust(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	listOnly, _ := cmd.Flags().GetBool("list")
	doTrust, _ := cmd.Flags().GetBool("yes")
	doRevoke, _ := cmd.Flags().GetBool("revoke")
	doInherit, _ := cmd.Flags().GetBool("inherit")

	if doTrust && doRevoke {
		return fmt.Errorf("--yes and --revoke are mutually exclusive")
	}
	if doInherit && doRevoke {
		return fmt.Errorf("--inherit and --revoke are mutually exclusive")
	}
	if listOnly {
		return runConfigTrustList(jsonOutput)
	}
	if doInherit {
		return runConfigTrustInherit(doTrust, jsonOutput)
	}

	cwd, _ := os.Getwd()
	layered, err := config.LoadLayered(cwd)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	var report *config.ExecGateReport
	if layered.Final != nil {
		report = layered.Final.ExecGate
	}
	if report == nil || len(report.Files) == 0 {
		if jsonOutput {
			return printJSON(map[string]interface{}{"files": []config.ExecGateFile{}, "findings": []config.ExecFinding{}})
		}
		fmt.Println("No exec-bearing config from repo-controlled layers here — nothing to trust.")
		return nil
	}

	switch {
	case doTrust:
		return applyConfigTrust(report, jsonOutput)
	case doRevoke:
		return revokeConfigTrust(report, jsonOutput)
	case jsonOutput:
		return printJSON(report)
	default:
		printExecGateReport(report, true)
		return nil
	}
}

// runConfigTrustList prints the whole trust store, which is machine-wide
// rather than scoped to the current directory.
func runConfigTrustList(jsonOutput bool) error {
	entries := exectrust.Load().Entries()
	if jsonOutput {
		out := make([]map[string]string, 0, len(entries))
		for _, e := range entries {
			out = append(out, map[string]string{"path": e.Path, "digest": e.Digest, "trusted_at": e.TrustedAt})
		}
		return printJSON(out)
	}
	if len(entries) == 0 {
		fmt.Println("No config files trusted yet.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TRUSTED AT\tFILE")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\n", e.TrustedAt, e.Path)
	}
	return w.Flush()
}

// runConfigTrustInherit relocates owner-checkout trust decisions onto every
// registered worktree. Unlike the cwd-scoped paths above this sweeps the whole
// worktree registry, because the per-worktree approval tax is exactly what it
// exists to remove — visiting each worktree to run it there would be the
// problem, not the fix.
func runConfigTrustInherit(apply, jsonOutput bool) error {
	entries, err := worktreeregistry.ListAll()
	if err != nil {
		return fmt.Errorf("failed to read the worktree registry: %w", err)
	}

	var candidates []config.InheritCandidate
	// owner tracks which worktree each candidate came from, for grouping.
	owningWorktree := map[string]string{}
	for _, e := range entries {
		if e == nil || e.IsArchived() || e.Owner == "" || len(e.Repos) == 0 {
			continue
		}
		if _, statErr := os.Stat(e.AbsPath); statErr != nil {
			continue // zombie entry; the worktree is gone from disk
		}
		for _, c := range config.WorktreeInheritCandidates(e.Owner, e.AbsPath, e.Repos) {
			owningWorktree[c.Dest] = e.AbsPath
			candidates = append(candidates, c)
		}
	}

	if len(candidates) == 0 {
		if jsonOutput {
			return printJSON(map[string]interface{}{"outcomes": []config.InheritOutcome{}, "granted": 0})
		}
		fmt.Println("No live registered worktrees with member repos — nothing to inherit.")
		return nil
	}

	outcomes, err := config.InheritExecTrust(candidates, apply)
	if err != nil {
		return err
	}
	if jsonOutput {
		return printJSON(map[string]interface{}{
			"outcomes": outcomes,
			"granted":  config.InheritGrantedCount(outcomes),
			"applied":  apply,
		})
	}
	printInheritReport(outcomes, owningWorktree, apply)
	return nil
}

// printInheritReport summarizes the sweep. The grant list is collapsed to a
// per-worktree count — hundreds of individual paths are noise — but the two
// outcomes that need a human decision (a branch that changed its exec config,
// an owner that was never trusted) are named explicitly.
func printInheritReport(outcomes []config.InheritOutcome, owningWorktree map[string]string, apply bool) {
	byReason := map[config.InheritSkipReason]int{}
	grantsPerWorktree := map[string]int{}
	var differing, untrustedSources []config.InheritOutcome

	for _, o := range outcomes {
		if o.Granted {
			grantsPerWorktree[owningWorktree[o.Dest]]++
			continue
		}
		byReason[o.Reason]++
		switch o.Reason {
		case config.InheritDigestMismatch:
			differing = append(differing, o)
		case config.InheritSourceUntrusted:
			untrustedSources = append(untrustedSources, o)
		}
	}

	granted := config.InheritGrantedCount(outcomes)
	verb := "would inherit"
	if apply {
		verb = "inherited"
	}
	fmt.Printf("%d of %d worktree config file(s) %s trust from their owner checkout, across %d worktree(s).\n",
		granted, len(outcomes), verb, len(grantsPerWorktree))

	if n := byReason[config.InheritAlreadyTrusted]; n > 0 {
		fmt.Printf("  %d already trusted.\n", n)
	}
	if n := byReason[config.InheritNoExecConfig]; n > 0 {
		fmt.Printf("  %d carry no exec-bearing config (nothing to trust).\n", n)
	}
	if n := byReason[config.InheritDestUnreadable] + byReason[config.InheritSourceUnreadable]; n > 0 {
		fmt.Printf("  %d skipped — config file missing or unparseable.\n", n)
	}

	if len(untrustedSources) > 0 {
		owners := map[string]struct{}{}
		for _, o := range untrustedSources {
			owners[o.Source] = struct{}{}
		}
		fmt.Println()
		fmt.Printf("%d file(s) could not inherit because their OWNER checkout is not trusted yet\n", len(untrustedSources))
		fmt.Printf("(%d distinct owner config file(s)). Inheritance relocates an existing decision;\n", len(owners))
		fmt.Println("it cannot invent one. Review and trust the owners first, then re-run:")
		fmt.Println()
		fmt.Println("    grove run -- grove config trust        # review each owner repo")
		fmt.Println("    grove run -- grove config trust --yes  # then allow them")
		fmt.Println("    grove config trust --inherit --yes     # then fan out to worktrees")
	}

	if len(differing) > 0 {
		fmt.Println()
		fmt.Printf("%d file(s) carry DIFFERENT exec config than their owner — the branch changed a\n", len(differing))
		fmt.Println("command, so the gate stays shut. Review these where they live:")
		fmt.Println()
		shown := differing
		const maxShown = 20
		if len(shown) > maxShown {
			shown = shown[:maxShown]
		}
		for _, o := range shown {
			fmt.Printf("    %s\n", filepath.Dir(o.Dest))
		}
		if len(differing) > len(shown) {
			fmt.Printf("    ... and %d more (use --json for the full list)\n", len(differing)-len(shown))
		}
	}

	if granted > 0 && !apply {
		fmt.Println()
		fmt.Println("This is a preview; nothing has been recorded. To apply:")
		fmt.Println()
		fmt.Println("    grove config trust --inherit --yes")
	}
}

// applyConfigTrust records the current digest for every file in scope that is
// not already trusted at it.
func applyConfigTrust(report *config.ExecGateReport, jsonOutput bool) error {
	// Print what is being trusted before recording it. `grove config trust`
	// and `grove config trust --yes` are two separate loads: without this,
	// anything that edited the config between the review and the approval
	// (a `git pull`, a running agent, a sync) would get trusted unseen.
	if !jsonOutput {
		printExecGateReport(report, false)
		fmt.Println()
	}

	store := exectrust.Load()
	now := time.Now()
	var changed []string
	for _, f := range report.Files {
		if f.Trusted {
			continue
		}
		store.Trust(f.Path, f.Digest, now)
		changed = append(changed, f.Path)
	}
	if len(changed) == 0 {
		if jsonOutput {
			return printJSON(map[string]interface{}{"trusted": []string{}, "already_trusted": true})
		}
		fmt.Println("Every config file in scope is already trusted at its current contents.")
		return nil
	}
	if err := store.Save(); err != nil {
		return fmt.Errorf("failed to record trust: %w", err)
	}
	if jsonOutput {
		return printJSON(map[string]interface{}{"trusted": changed})
	}
	for _, p := range changed {
		fmt.Printf("Trusted %s\n", p)
	}
	fmt.Println()
	fmt.Println("Its exec-bearing config is now honored. If the file's commands change,")
	fmt.Println("grove will withhold them again until you review the new ones.")
	return nil
}

// revokeConfigTrust drops the records for every file in scope.
func revokeConfigTrust(report *config.ExecGateReport, jsonOutput bool) error {
	store := exectrust.Load()
	var changed []string
	for _, f := range report.Files {
		if store.Revoke(f.Path) {
			changed = append(changed, f.Path)
		}
	}
	if len(changed) == 0 {
		if jsonOutput {
			return printJSON(map[string]interface{}{"revoked": []string{}})
		}
		fmt.Println("No config file in scope was trusted — nothing to revoke.")
		return nil
	}
	if err := store.Save(); err != nil {
		return fmt.Errorf("failed to record revocation: %w", err)
	}
	if jsonOutput {
		return printJSON(map[string]interface{}{"revoked": changed})
	}
	for _, p := range changed {
		fmt.Printf("Revoked trust for %s\n", p)
	}
	return nil
}

// printExecGateReport renders the report diff-style: every command the config
// would run, grouped by the file that declares it, marked with whether it is
// currently withheld.
func printExecGateReport(report *config.ExecGateReport, showCallToAction bool) {
	byFile := map[string][]config.ExecFinding{}
	for _, f := range report.Findings {
		byFile[f.File] = append(byFile[f.File], f)
	}

	fmt.Printf("Exec-bearing config from repo-controlled layers (policy: %s)\n", report.Mode)

	var ignored, honored int
	for _, file := range report.Files {
		state := "UNTRUSTED"
		if file.Trusted {
			state = "trusted"
		}
		fmt.Println()
		fmt.Printf("  %s  (%s, %s)\n", file.Path, file.Layer, state)

		findings := byFile[file.Path]
		sort.SliceStable(findings, func(i, j int) bool { return findings[i].Key < findings[j].Key })
		for _, f := range findings {
			marker, status := "+", "honored"
			if f.Quarantined {
				marker, status = "-", "IGNORED"
				ignored++
			} else {
				honored++
			}
			fmt.Printf("    %s %s  [%s, %s]\n", marker, f.Key, f.Risk, status)
			fmt.Printf("        %s\n", f.Description)
			for _, line := range strings.Split(f.Value, "\n") {
				fmt.Printf("        %s\n", line)
			}
		}
	}

	fmt.Println()
	fmt.Printf("%d value(s) ignored, %d honored.\n", ignored, honored)
	if ignored > 0 && showCallToAction {
		fmt.Println()
		fmt.Println("Read the values above before allowing them — the commands run on your")
		fmt.Println("machine with your privileges, and the capability entries decide what an")
		fmt.Println("agent session in this workspace may do. To allow them:")
		fmt.Println()
		fmt.Println("    grove config trust --yes")
	}
}

// warnUntrustedExecConfig prints a one-line notice to stderr when the gate
// withheld something from the loaded config. Used by the commands that show
// the merged/audited config, where the withheld keys are invisible by
// construction and their absence would otherwise read as a bug.
func warnUntrustedExecConfig(layered *config.LayeredConfig) {
	if layered == nil || layered.Final == nil {
		return
	}
	report := layered.Final.ExecGate
	if !report.HasQuarantined() {
		return
	}
	fmt.Fprintf(os.Stderr,
		"⚠ %d exec-bearing or capability-granting config value(s) are being ignored because\n"+
			"  their workspace is not trusted. Review them with `grove config trust`.\n\n",
		len(report.Quarantined()))
}

// printJSON is the shared indented-JSON emitter for this command.
func printJSON(v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode output: %w", err)
	}
	fmt.Println(string(data))
	return nil
}
