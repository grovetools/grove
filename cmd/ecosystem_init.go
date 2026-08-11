package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/tui/theme"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	ecosystemInitGo     bool
	ecosystemInitFormat string
)

func newEcosystemInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [name]",
		Short: "Create a new Grove ecosystem",
		Long: `Create a new Grove ecosystem (monorepo).

By default, creates a minimal ecosystem with grove.toml and README.
Use --format yaml to scaffold grove.yml instead; both dialects are read
everywhere, and an existing ecosystem keeps whichever it already has.
Use --go to add Go workspace support (go.work, Makefile).

Examples:
  # Create minimal ecosystem in current directory
  grove ecosystem init

  # Create ecosystem with a name
  grove ecosystem init my-ecosystem

  # Scaffold a YAML manifest instead of TOML
  grove ecosystem init my-ecosystem --format yaml

  # Create Go-based ecosystem
  grove ecosystem init --go
  grove ecosystem init my-ecosystem --go`,
		Args: cobra.MaximumNArgs(1),
		RunE: runEcosystemInit,
	}

	cmd.Flags().BoolVar(&ecosystemInitGo, "go", false, "Add Go workspace support (go.work, Makefile)")
	cmd.Flags().StringVar(&ecosystemInitFormat, "format", "toml", "Manifest format for the new ecosystem: toml or yaml")

	return cmd
}

// ecosystemManifestScaffold returns the basename and body of the manifest a
// fresh ecosystem gets. TOML is the default dialect — it is what the config
// TUI, the setup wizard's default, and every ecosystem grove itself ships now
// write — with `--format yaml` kept as the escape hatch for people whose
// tooling still expects grove.yml.
func ecosystemManifestScaffold(format, name string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "toml":
		return "grove.toml", fmt.Sprintf(`name = "%s"
workspaces = ["*"]
`, name), nil
	case "yaml", "yml":
		return "grove.yml", fmt.Sprintf(`name: %s
workspaces:
  - "*"
`, name), nil
	default:
		return "", "", fmt.Errorf("unknown manifest format %q: use \"toml\" or \"yaml\"", format)
	}
}

func runEcosystemInit(cmd *cobra.Command, args []string) error {
	// Determine target directory
	var targetDir string
	var ecosystemName string

	if len(args) > 0 {
		ecosystemName = args[0]
		targetDir = args[0]
	} else {
		targetDir = "."
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		ecosystemName = filepath.Base(cwd)
	}

	// Resolve the manifest before anything is written, so a bad --format
	// leaves no half-created directory behind.
	manifestName, manifestContent, err := ecosystemManifestScaffold(ecosystemInitFormat, ecosystemName)
	if err != nil {
		return err
	}

	// A directory that already carries either manifest dialect is already an
	// ecosystem; re-scaffolding it would clobber a hand-authored file.
	if existing := config.FindEcosystemManifest(targetDir); existing != "" {
		return fmt.Errorf("%s already exists in %s", filepath.Base(existing), targetDir)
	}

	if len(args) > 0 {
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
	}

	fmt.Printf("Creating Grove ecosystem '%s'...\n", ecosystemName)

	manifestPath := filepath.Join(targetDir, manifestName)
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0o600); err != nil {
		return fmt.Errorf("failed to create %s: %w", manifestName, err)
	}
	fmt.Printf("  %s\n", manifestName)

	// Create README.md
	readmeContent := fmt.Sprintf("# %s\n\nA Grove ecosystem.\n", ecosystemName)
	if err := os.WriteFile(filepath.Join(targetDir, "README.md"), []byte(readmeContent), 0o600); err != nil {
		return fmt.Errorf("failed to create README.md: %w", err)
	}
	fmt.Println("  README.md")

	// Create .gitignore
	gitignoreContent := `# Binaries
bin/
*.exe

# OS files
.DS_Store
`
	if err := os.WriteFile(filepath.Join(targetDir, ".gitignore"), []byte(gitignoreContent), 0o600); err != nil {
		return fmt.Errorf("failed to create .gitignore: %w", err)
	}
	fmt.Println("  .gitignore")

	// Add Go support if requested
	if ecosystemInitGo {
		// Create go.work
		goWorkContent := `go 1.24.4

use (
)
`
		if err := os.WriteFile(filepath.Join(targetDir, "go.work"), []byte(goWorkContent), 0o600); err != nil {
			return fmt.Errorf("failed to create go.work: %w", err)
		}
		fmt.Println("  go.work")

		// Create Makefile
		makefileContent := `# Grove ecosystem Makefile

.PHONY: build test clean

build:
	@grove build

test:
	@grove build && go test ./...

clean:
	@rm -rf bin/
`
		if err := os.WriteFile(filepath.Join(targetDir, "Makefile"), []byte(makefileContent), 0o600); err != nil {
			return fmt.Errorf("failed to create Makefile: %w", err)
		}
		fmt.Println("  Makefile")
	}

	// Mint the ecosystem identity card before git init, so the card is part of
	// the initial commit and travels with every clone from here on. Layout and
	// remotes are derived from whatever git state already exists: a directory
	// that was already a repo keeps its remotes, and a directory about to be
	// `git init`ed simply has none yet.
	if _, err := config.WriteEcosystemCard(manifestPath, deriveEcosystemCard(targetDir, nil)); err != nil {
		return fmt.Errorf("failed to write the ecosystem card: %w", err)
	}
	card, err := config.LoadEcosystemCard(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read back the ecosystem card: %w", err)
	}
	fmt.Printf("  ecosystem identity card (id %s)\n", card.ID)

	// Initialize git if not already a git repo
	gitDir := filepath.Join(targetDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		gitInit := exec.Command("git", "init")
		gitInit.Dir = targetDir
		if err := gitInit.Run(); err != nil {
			return fmt.Errorf("failed to initialize git: %w", err)
		}

		// Add and commit
		gitAdd := exec.Command("git", "add", ".")
		gitAdd.Dir = targetDir
		_ = gitAdd.Run()

		gitCommit := exec.Command("git", "commit", "-m", "feat: initialize Grove ecosystem")
		gitCommit.Dir = targetDir
		_ = gitCommit.Run()
	}

	fmt.Printf("\n%s Ecosystem created!\n", theme.IconSuccess)

	// Notify daemon to re-scan workspaces
	client := daemon.New()
	if client.IsRunning() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = client.Refresh(ctx)
		cancel()
	}
	client.Close()

	// Check discoverability and prompt to add to groves if needed
	if err := checkAndPromptDiscoverability(targetDir); err != nil {
		// Don't fail the command if discoverability check fails, just warn
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	if len(args) > 0 {
		fmt.Printf("\ncd %s\n", ecosystemName)
	}

	return nil
}

// checkAndPromptDiscoverability checks if the ecosystem will be discoverable
// and prompts the user to add it to the global config if not.
func checkAndPromptDiscoverability(ecosystemPath string) error {
	// Get absolute path of the ecosystem
	absPath, err := filepath.Abs(ecosystemPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Load the current global config
	cfg, err := config.LoadDefault()
	if err != nil {
		// If config loading fails, we can't check discoverability
		// This is not necessarily an error - might be first-time setup
		cfg = &config.Config{
			Groves: make(map[string]config.GroveSourceConfig),
		}
	}
	// Check if the ecosystem is already discoverable
	if discoverable, groveName := isEcosystemDiscoverable(absPath, cfg); discoverable {
		fmt.Printf("\nThis ecosystem is discoverable via grove '%s'\n", groveName)
		return nil
	}

	// Check if we're in a TTY (interactive mode)
	if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		fmt.Printf("\n%s This ecosystem is not in a configured grove and won't be\n", theme.IconWarning)
		fmt.Printf("   discovered by grove tools.\n")
		fmt.Printf("   Run in an interactive terminal to add it, or declare it yourself in\n")
		fmt.Printf("   %s under [roots.<name>].\n", coderoot.RootsPath())
		return nil
	}

	// Derive grove name from the ecosystem path itself
	groveName, err := deriveGroveName(absPath, cfg.Groves)
	if err != nil {
		fmt.Printf("\n%s This ecosystem is not in a configured grove and won't be\n", theme.IconWarning)
		fmt.Printf("   discovered by grove tools.\n")
		fmt.Printf("   Error: %v\n", err)
		fmt.Printf("   Declare it yourself in %s under [roots.<name>].\n", coderoot.RootsPath())
		return nil
	}

	// Get available notebooks
	notebooks := getNotebookKeys(cfg)
	sort.Strings(notebooks)

	// Run the TUI prompt
	result, err := runInitPrompt(absPath, groveName, notebooks)
	if err != nil {
		return fmt.Errorf("failed to run prompt: %w", err)
	}

	if !result.Confirmed {
		fmt.Println("\nSkipped adding grove to config.")
		return nil
	}

	// Record the ecosystem as one of this machine's subscriptions.
	configPath, err := registerCodeRoot(groveName, absPath, result.SelectedNotebook)
	if err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	fmt.Printf("\n%s Subscribed to ecosystem '%s' (%s)\n", theme.IconSuccess, groveName, absPath)
	fmt.Printf("  Updated %s\n", configPath)

	return nil
}
