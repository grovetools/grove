package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// promote flag vars (scoped to `grove release promote`).
var (
	promoteRepo   string
	promoteRun    string
	promoteDryRun bool
	promoteForce  bool
)

const (
	// proposedConfigName is the config file inside a staged proposal bundle.
	proposedConfigName = "proposed.docgen.config.yml"
	// notebookConfigName is the live notebook docgen config the bundle promotes to.
	notebookConfigName = "docgen.config.yml"
)

// promoteSection is the minimal shape we read out of a proposal bundle's
// proposed.docgen.config.yml — just what promote needs to validate the outline
// and reconcile prompts. Defined locally so grove does not import docgen's
// config package (mirrors release_tui_docs.go's docgenSectionConfig).
type promoteSection struct {
	Name   string `yaml:"name"`
	Title  string `yaml:"title"`
	Type   string `yaml:"type"`
	Output string `yaml:"output"`
	Prompt string `yaml:"prompt"`
	Binary string `yaml:"binary"`
	// Command is NOT a docgen field — proposals have been observed emitting
	// 'command:' where docgen requires 'binary:'; parsed only to give a
	// targeted validation hint.
	Command string `yaml:"command"`
}

// promoteConfig is the minimal top-level shape parsed from the bundle config.
type promoteConfig struct {
	Sections []promoteSection `yaml:"sections"`
}

// promoteBundle is a validated proposal bundle ready to apply: the parsed
// sections plus the raw config bytes (kept verbatim so promote writes the exact
// bytes docgen proposed, not a re-serialized subset).
type promoteBundle struct {
	Dir         string
	ConfigBytes []byte
	Sections    []promoteSection
}

// promoteCopy is one prompt file to copy from the bundle into the notebook.
type promoteCopy struct{ Src, Dst string }

// promoteActions is the fully-computed plan for applying a bundle: which config
// bytes land where, which prompt files copy in, and which stale notebook prompts
// get pruned. Computed by planPromoteApply WITHOUT touching the filesystem, so a
// --dry-run can print exactly what an apply would do and unit tests can assert
// the plan before any writes.
type promoteActions struct {
	DocgenDir     string
	ConfigTarget  string
	ConfigBytes   []byte
	PromptsDir    string
	PromptWrites  []promoteCopy
	PromptDeletes []string
}

// newReleasePromoteCmd creates the 'grove release promote' subcommand: apply a
// staged proposal bundle (from 'grove release propose') to a repo's notebook
// docgen dir, validating the bundle up front so a broken outline is rejected
// BEFORE it reaches the notebook. Promote is the first-class counterpart to
// hand-copying proposal files, and is reversible via git.
func newReleasePromoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "promote",
		Short: "Apply a staged docs proposal bundle to the notebook (validated up front; reversible via git)",
		Long: `Apply a staged proposal bundle (from 'grove release propose') to a repo's
notebook docgen directory. Promote replaces the ad-hoc hand-copy of proposal
files with a validated, reversible step:

  1. Resolve the bundle: <staging>/<repo>/proposal/latest by default, or a
     specific run with --run <leaf> (a runs/<leaf> dir name).
  2. Validate the bundle BEFORE touching the notebook and hard-fail listing
     every problem: the config must parse, have at least one section, every
     section must set 'output:', and every 'type: prose' section must set
     'prompt:' pointing at a file present in the bundle's prompts/ dir.
  3. Require the notebook docgen dir to be git-clean (so promote is fully
     reversible with 'git checkout'); use --force to skip this gate.
  4. Write the bundle config to docgen.config.yml, copy every bundle prompt in,
     and prune notebook prompts no longer referenced by the new config.

After promoting, review with 'git -C <docgenDir> diff', then run
'grove release gen --repo <repo> --model <same model>' within the cache TTL to
ride the prefix the proposal warmed.

Examples:
  grove release promote --repo notify
  grove release promote --repo notify --run 20260710-135333
  grove release promote --repo notify --dry-run   # print what would change, write nothing
  grove release promote --repo notify --force      # promote over a dirty notebook docgen dir`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReleasePromote(context.Background())
		},
	}

	cmd.Flags().StringVar(&promoteRepo, "repo", "", "Repository whose proposal bundle to promote (required)")
	cmd.Flags().StringVar(&promoteRun, "run", "", "Promote a specific proposal run (a runs/<leaf> dir name); defaults to the 'latest' run")
	cmd.Flags().BoolVar(&promoteDryRun, "dry-run", false, "Validate and print exactly what would change without writing to the notebook")
	cmd.Flags().BoolVar(&promoteForce, "force", false, "Skip the notebook git-clean safety gate (promote over uncommitted docgen changes)")

	_ = cmd.MarkFlagRequired("repo")

	return cmd
}

// runReleasePromote wires the resolve → validate → gate → apply → report flow.
func runReleasePromote(ctx context.Context) error {
	_ = ctx
	displayPhase("Promoting Docs Proposal")

	repoPath, err := resolveProposeRepoPath(promoteRepo)
	if err != nil {
		return err
	}

	stagingDir, err := getStagingDirPath()
	if err != nil {
		return fmt.Errorf("failed to resolve staging dir: %w", err)
	}
	proposalDir := filepath.Join(stagingDir, promoteRepo, "proposal")

	bundleDir, runLeaf, err := resolvePromoteBundleDir(proposalDir, promoteRun)
	if err != nil {
		return err
	}
	displayInfo(fmt.Sprintf("Promoting proposal run %s (from %s)", runLeaf, bundleDir))

	// Validate BEFORE resolving/touching the notebook — a broken bundle stops
	// here, listing every problem, so nothing partial ever reaches the notebook.
	bundle, err := validatePromoteBundle(bundleDir)
	if err != nil {
		return err
	}
	displayInfo(fmt.Sprintf("Validated bundle: %d section(s), config OK", len(bundle.Sections)))

	// The notebook docgen dir resolves against the repo's PARENT dir as rootDir
	// (resolveNotebookDocgenDir joins rootDir/<repo>), matching how the release
	// TUI derives it from plan.RootDir.
	rootDir := filepath.Dir(repoPath)
	docgenDir, err := resolveNotebookDocgenDir(rootDir, promoteRepo)
	if err != nil {
		return err
	}

	// Safety gate: require a git-clean notebook docgen dir so promote is fully
	// reversible via git. --force skips it.
	if !promoteForce {
		if gerr := promoteRequireClean(docgenDir); gerr != nil {
			return gerr
		}
	}

	actions, err := planPromoteApply(bundle, docgenDir)
	if err != nil {
		return err
	}

	if promoteDryRun {
		displayInfo(fmt.Sprintf("Dry run — would apply to %s:", docgenDir))
		displayInfo(fmt.Sprintf("  write config  -> %s", actions.ConfigTarget))
		for _, w := range actions.PromptWrites {
			displayInfo(fmt.Sprintf("  write prompt  -> %s", w.Dst))
		}
		for _, d := range actions.PromptDeletes {
			displayInfo(fmt.Sprintf("  prune prompt  -> %s (not referenced by the new config)", d))
		}
		displaySuccess(fmt.Sprintf("Dry run: %d prompt(s) to write, %d stale prompt(s) to prune (nothing written)",
			len(actions.PromptWrites), len(actions.PromptDeletes)))
		return nil
	}

	written, deleted, err := executePromoteActions(actions)
	if err != nil {
		return err
	}

	displaySuccess(fmt.Sprintf("Promoted run %s to %s: wrote config + %d prompt(s), pruned %d stale prompt(s)",
		runLeaf, docgenDir, written, deleted))
	displayInfo(fmt.Sprintf("Review the change:  git -C %s diff", docgenDir))
	displayInfo(fmt.Sprintf("Then regenerate:    grove release gen --repo %s --model <same model as the proposal> (within the cache TTL to ride the warm prefix)", promoteRepo))
	return nil
}

// resolvePromoteBundleDir locates the proposal bundle to promote: <proposalDir>/
// runs/<run> when --run is given, else whatever <proposalDir>/latest points at.
// It returns the bundle dir and its run-leaf name, with a clear error when the
// requested run (or any prior run) does not exist.
func resolvePromoteBundleDir(proposalDir, run string) (bundleDir, leaf string, err error) {
	if strings.TrimSpace(run) != "" {
		bundleDir = filepath.Join(proposalDir, "runs", run)
		info, serr := os.Stat(bundleDir)
		if serr != nil || !info.IsDir() {
			return "", "", fmt.Errorf("proposal run %q not found under %s (expected %s); list runs under %s",
				run, proposalDir, bundleDir, filepath.Join(proposalDir, "runs"))
		}
		return bundleDir, run, nil
	}
	latest, lerr := resolveProposeLatestDir(proposalDir)
	if lerr != nil {
		return "", "", lerr
	}
	return latest, filepath.Base(latest), nil
}

// validatePromoteBundle reads and validates a proposal bundle's config, hard-
// failing with EVERY problem collected (not just the first) so a reviewer fixes
// the whole bundle in one pass. On success it returns the parsed sections plus
// the raw config bytes to copy verbatim.
func validatePromoteBundle(bundleDir string) (*promoteBundle, error) {
	cfgPath := filepath.Join(bundleDir, proposedConfigName)
	data, err := os.ReadFile(cfgPath) //nolint:gosec // staging path derived from resolved repo/staging dirs
	if err != nil {
		return nil, fmt.Errorf("proposal bundle is missing %s: %w", proposedConfigName, err)
	}

	var cfg promoteConfig
	if uerr := yaml.Unmarshal(data, &cfg); uerr != nil {
		return nil, fmt.Errorf("proposal bundle %s does not parse as YAML: %w", cfgPath, uerr)
	}

	var problems []string
	if len(cfg.Sections) == 0 {
		problems = append(problems, "config has no sections (expected at least one)")
	}
	promptsDir := filepath.Join(bundleDir, "prompts")
	for i, s := range cfg.Sections {
		label := s.Name
		if label == "" {
			label = fmt.Sprintf("index %d", i)
		}
		if strings.TrimSpace(s.Output) == "" {
			problems = append(problems, fmt.Sprintf("section %q has no output: filename", label))
		}
		if s.Type == "prose" {
			if strings.TrimSpace(s.Prompt) == "" {
				problems = append(problems, fmt.Sprintf("prose section %q has no prompt: filename", label))
				continue
			}
			promptFile := filepath.Join(promptsDir, s.Prompt)
			if _, serr := os.Stat(promptFile); serr != nil {
				problems = append(problems, fmt.Sprintf("prose section %q references prompt %q, missing from the bundle prompts/ dir", label, s.Prompt))
			}
		}
		if s.Type == "capture" && strings.TrimSpace(s.Binary) == "" {
			// Live-observed proposal defect: 'command:' where docgen requires
			// 'binary:' — gen fails deterministically on it.
			msg := fmt.Sprintf("capture section %q has no binary: field", label)
			if strings.TrimSpace(s.Command) != "" {
				msg += fmt.Sprintf(" (it sets command: %q — docgen expects binary:)", s.Command)
			}
			problems = append(problems, msg)
		}
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("proposal bundle %s is invalid — refusing to promote:\n  - %s",
			bundleDir, strings.Join(problems, "\n  - "))
	}

	return &promoteBundle{Dir: bundleDir, ConfigBytes: data, Sections: cfg.Sections}, nil
}

// planPromoteApply computes the apply plan without writing anything: the config
// target, the prompt files to copy from the bundle, and the notebook prompts to
// prune (any *.md not referenced by the new config's prose 'prompt:' fields —
// stale prompts otherwise pollute future propose suffixes). Reading only, so
// both the dry-run and the real apply share one plan.
func planPromoteApply(bundle *promoteBundle, docgenDir string) (*promoteActions, error) {
	notebookPrompts := filepath.Join(docgenDir, "prompts")

	// Files the new config references (bare filenames relative to prompts/).
	referenced := map[string]bool{}
	for _, s := range bundle.Sections {
		if p := strings.TrimSpace(s.Prompt); p != "" {
			referenced[p] = true
		}
	}

	// Every *.md in the bundle prompts/ dir is copied in verbatim.
	var writes []promoteCopy
	bundlePrompts := filepath.Join(bundle.Dir, "prompts")
	if entries, rerr := os.ReadDir(bundlePrompts); rerr == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			writes = append(writes, promoteCopy{
				Src: filepath.Join(bundlePrompts, e.Name()),
				Dst: filepath.Join(notebookPrompts, e.Name()),
			})
		}
	}
	sort.Slice(writes, func(i, j int) bool { return writes[i].Dst < writes[j].Dst })

	// Notebook prompts no longer referenced by the config are pruned.
	var deletes []string
	if entries, rerr := os.ReadDir(notebookPrompts); rerr == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			if !referenced[e.Name()] {
				deletes = append(deletes, filepath.Join(notebookPrompts, e.Name()))
			}
		}
	}
	sort.Strings(deletes)

	return &promoteActions{
		DocgenDir:     docgenDir,
		ConfigTarget:  filepath.Join(docgenDir, notebookConfigName),
		ConfigBytes:   bundle.ConfigBytes,
		PromptsDir:    notebookPrompts,
		PromptWrites:  writes,
		PromptDeletes: deletes,
	}, nil
}

// executePromoteActions applies a computed plan: write the config, copy prompts
// in, prune stale prompts. Returns counts of files written (config counted with
// the prompts) and deleted.
func executePromoteActions(a *promoteActions) (written, deleted int, err error) {
	if mkErr := os.MkdirAll(a.DocgenDir, 0o755); mkErr != nil { //nolint:gosec // notebook dir
		return 0, 0, fmt.Errorf("creating docgen dir %s: %w", a.DocgenDir, mkErr)
	}
	if werr := os.WriteFile(a.ConfigTarget, a.ConfigBytes, 0o644); werr != nil { //nolint:gosec // notebook config
		return 0, 0, fmt.Errorf("writing %s: %w", a.ConfigTarget, werr)
	}
	written = 1

	if len(a.PromptWrites) > 0 {
		if mkErr := os.MkdirAll(a.PromptsDir, 0o755); mkErr != nil { //nolint:gosec // notebook prompts dir
			return written, 0, fmt.Errorf("creating prompts dir %s: %w", a.PromptsDir, mkErr)
		}
	}
	for _, w := range a.PromptWrites {
		data, rerr := os.ReadFile(w.Src) //nolint:gosec // bundle prompt path
		if rerr != nil {
			return written, deleted, fmt.Errorf("reading bundle prompt %s: %w", w.Src, rerr)
		}
		if werr := os.WriteFile(w.Dst, data, 0o644); werr != nil { //nolint:gosec // notebook prompt
			return written, deleted, fmt.Errorf("writing prompt %s: %w", w.Dst, werr)
		}
		written++
	}

	for _, d := range a.PromptDeletes {
		if rerr := os.Remove(d); rerr != nil && !os.IsNotExist(rerr) {
			return written, deleted, fmt.Errorf("pruning stale prompt %s: %w", d, rerr)
		}
		deleted++
	}

	return written, deleted, nil
}

// promoteRequireClean refuses to promote unless the notebook docgen dir is a
// git-clean working tree, so promote is trivially reversible with git checkout.
// A dir that is not inside a git repo also fails (there'd be no undo).
func promoteRequireClean(docgenDir string) error {
	out, err := exec.Command("git", "-C", docgenDir, "status", "--porcelain", "--", ".").Output()
	if err != nil {
		return fmt.Errorf("notebook docgen dir %s is not inside a git repository (or git failed): %w — promote needs git to be reversible; re-run with --force to promote anyway", docgenDir, err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return fmt.Errorf("notebook docgen dir %s has uncommitted changes — commit or stash them so promote stays reversible, or re-run with --force:\n%s", docgenDir, strings.TrimRight(string(out), "\n"))
	}
	return nil
}
