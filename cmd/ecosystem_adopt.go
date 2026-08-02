package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/theme"
	"github.com/spf13/cobra"
)

var (
	ecosystemAdoptYes      bool
	ecosystemAdoptLayout   string
	ecosystemAdoptNotebook string
)

func newEcosystemAdoptCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "adopt [path]",
		Short: "Backfill an ecosystem identity card into an existing ecosystem",
		Long: `Backfill an ecosystem identity card into an existing ecosystem.

An ecosystem card is the [ecosystem] table in the ecosystem's own grove
manifest: a stable id, the layout a peer should clone it with, the git remotes
to clone from, and the notebooks bound to it. Because it lives in the repo, it
travels with every clone — a second machine can materialize this ecosystem
from the card alone.

Adopt is for ecosystems that predate the card. It detects the layout
(.gitmodules at the root means superrepo, otherwise flat), reads the remotes
from git, and asks you to confirm before writing.

The id is minted exactly once. Re-running adopt on an already-carded ecosystem
re-derives layout and remotes, and writes nothing at all when they are
unchanged.

Examples:
  # Adopt the ecosystem in the current directory
  grove ecosystem adopt

  # Adopt a specific ecosystem non-interactively
  grove ecosystem adopt ~/code/grovetools --yes

  # Bind a notebook while adopting
  grove ecosystem adopt --notebook work`,
		Args: cobra.MaximumNArgs(1),
		RunE: runEcosystemAdopt,
	}

	cmd.Flags().BoolVarP(&ecosystemAdoptYes, "yes", "y", false, "Accept the detected card without prompting")
	cmd.Flags().StringVar(&ecosystemAdoptLayout, "layout", "", "Override the detected layout (superrepo or flat)")
	cmd.Flags().StringVar(&ecosystemAdoptNotebook, "notebook", "", "Bind this notebook as the ecosystem's default")

	return cmd
}

func runEcosystemAdopt(cmd *cobra.Command, args []string) error {
	target := "."
	if len(args) > 0 {
		target = args[0]
	}
	absPath, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	manifest := config.FindEcosystemManifest(absPath)
	if manifest == "" {
		return fmt.Errorf("no grove manifest in %s — this is not a Grove ecosystem yet\n\nTo create one, run: grove ecosystem init", absPath)
	}

	existing, err := config.LoadEcosystemCard(manifest)
	if err != nil {
		return err
	}

	card := deriveEcosystemCard(absPath, existing)
	if ecosystemAdoptLayout != "" {
		switch ecosystemAdoptLayout {
		case config.LayoutSuperrepo, config.LayoutFlat:
			card.Layout = ecosystemAdoptLayout
		default:
			return fmt.Errorf("--layout must be %q or %q, got %q", config.LayoutSuperrepo, config.LayoutFlat, ecosystemAdoptLayout)
		}
	}

	notebook := ecosystemAdoptNotebook
	if notebook == "" && card.DefaultNotebookName() == "" {
		// Only propose one when the card binds none: a card that already names
		// a default is a declaration, and re-deriving must not quietly move it.
		cfg, cfgErr := config.LoadDefault()
		if cfgErr == nil {
			notebook = proposeEcosystemNotebook(absPath, cfg)
		}
	}
	setEcosystemDefaultNotebook(&card, notebook)

	if existing != nil {
		fmt.Printf("Updating the ecosystem card in %s:\n", manifest)
	} else {
		fmt.Printf("Adopting %s as a Grove ecosystem — card to write into %s:\n", absPath, filepath.Base(manifest))
	}
	fmt.Print(renderEcosystemCardSummary(card))

	if !ecosystemAdoptYes {
		if !satelliteStdinIsTTY() {
			return fmt.Errorf("aborted: adopt needs confirmation and stdin is not a terminal — re-run with --yes to accept the card above")
		}
		if !confirmYesNo("\nWrite this card?") {
			fmt.Println("Aborted; nothing was written.")
			return nil
		}
	}

	changed, err := config.WriteEcosystemCard(manifest, card)
	if err != nil {
		return err
	}
	if !changed {
		fmt.Printf("\n%s Already adopted — %s is unchanged.\n", theme.IconSuccess, manifest)
		return nil
	}

	fmt.Printf("\n%s Wrote the ecosystem card to %s\n", theme.IconSuccess, manifest)
	fmt.Fprintf(os.Stdout, "  Commit it so the card travels with clones of this ecosystem.\n")
	return nil
}
