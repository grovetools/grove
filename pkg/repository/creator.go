package repository

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/mattsolo1/grove-meta/pkg/gh"
	"github.com/mattsolo1/grove-meta/pkg/templates"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
)

type Creator struct {
	logger *logrus.Logger
	tmpl   *templates.Manager
	gh     *gh.Client
}

type CreateOptions struct {
	Name        string
	Alias       string
	Description string
	SkipGitHub  bool
	DryRun      bool
	StageChanges bool
	TemplatePath string
}

type creationState struct {
	localRepoCreated  bool
	githubRepoCreated bool
	tagCreated        bool
}

func NewCreator(logger *logrus.Logger) *Creator {
	return &Creator{
		logger: logger,
		tmpl:   templates.NewManager(),
		gh:     gh.NewClient(),
	}
}

func (c *Creator) Create(opts CreateOptions) error {
	// Phase 1: Validation
	if err := c.validate(opts); err != nil {
		return err
	}

	// Handle dry-run mode
	if opts.DryRun {
		return c.dryRun(opts)
	}

	// Track state for rollback
	state := &creationState{}

	// Phase 2: Generate local skeleton
	if err := c.generateSkeleton(opts); err != nil {
		return err
	}
	state.localRepoCreated = true

	// Phase 2.5: Detect project type early
	repoPath := filepath.Join(".", opts.Name)
	projectType := c.detectProjectType(repoPath)
	
	// Phase 2.6: Add to go.work temporarily for Go projects only
	if projectType == "go" {
		if err := c.updateGoWork(opts); err != nil {
			c.rollback(state, opts)
			return fmt.Errorf("failed to update go.work: %w", err)
		}
	}

	// Phase 2.7: Local verification
	c.logger.Info("Running local verification...")
	if err := c.verifyLocal(opts); err != nil {
		c.rollback(state, opts)
		return fmt.Errorf("local verification failed: %w", err)
	}

	// Phase 3: GitHub operations
	if !opts.SkipGitHub {
		// Create repo first
		if err := c.createGitHubRepo(opts); err != nil {
			c.rollback(state, opts)
			return err
		}
		state.githubRepoCreated = true

		// Set up secrets BEFORE pushing
		if err := c.setupSecrets(opts); err != nil {
			c.rollback(state, opts)
			return err
		}

		// Now push the code
		if err := c.pushToGitHub(opts); err != nil {
			c.rollback(state, opts)
			return err
		}
	}

	// Phase 4: Initial release
	if err := c.createInitialRelease(opts); err != nil {
		c.rollback(state, opts)
		return err
	}

	// Wait for CI to complete if GitHub repo was created
	if !opts.SkipGitHub {
		if err := c.waitForCI(opts); err != nil {
			c.logger.Warnf("Could not monitor CI build: %v", err)
			c.logger.Infof("Repository created successfully. Check CI status at: https://github.com/mattsolo1/%s/actions", opts.Name)
		}
	}

	// Phase 5: Add to ecosystem
	if err := c.addToEcosystem(opts); err != nil {
		c.rollback(state, opts)
		return fmt.Errorf("failed to add repository to ecosystem: %w\n\nNote: The grove-ecosystem may have been partially modified.\nYou can clean up with: git reset --hard", err)
	}

	// Phase 6: Stage changes (only if requested)
	if opts.StageChanges {
		if err := c.stageEcosystemChanges(opts); err != nil {
			return fmt.Errorf("failed to stage ecosystem changes: %w\n\nNote: The grove-ecosystem has been modified but changes were not staged.\nYou can:\n  - Stage manually: git add Makefile go.work .gitmodules %s\n  - Or reset: git reset --hard", err, opts.Name)
		}
	}

	// Phase 7: Final summary
	return c.showSummary(opts)
}

func (c *Creator) validate(opts CreateOptions) error {
	c.logger.Info("Validating repository configuration...")

	// Validate repository name
	if !isValidRepoName(opts.Name) {
		return fmt.Errorf("invalid repository name: must only contain lowercase letters, numbers, and hyphens")
	}

	// Validate alias
	if opts.Alias == "" {
		return fmt.Errorf("binary alias cannot be empty")
	}

	// Check for alias conflicts
	if err := checkBinaryAliasConflict(opts.Alias); err != nil {
		return err
	}

	// Check if directory already exists
	if _, err := os.Stat(opts.Name); err == nil {
		return fmt.Errorf("directory %s already exists", opts.Name)
	}

	// Check if we're in grove-ecosystem root
	if _, err := os.Stat("grove.yml"); err != nil {
		return fmt.Errorf("must be run from grove-ecosystem root directory")
	}

	// Check dependencies
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is not installed or not in PATH")
	}

	if !opts.SkipGitHub {
		if _, err := exec.LookPath("gh"); err != nil {
			return fmt.Errorf("GitHub CLI (gh) is not installed or not in PATH")
		}

		// Check GitHub authentication
		cmd := exec.Command("gh", "auth", "status")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("not authenticated with GitHub. Run 'gh auth login' first")
		}

		// Check if repo already exists on GitHub
		checkCmd := exec.Command("gh", "repo", "view", fmt.Sprintf("mattsolo1/%s", opts.Name))
		if err := checkCmd.Run(); err == nil {
			return fmt.Errorf("repository mattsolo1/%s already exists on GitHub", opts.Name)
		}

		// Check for GROVE_PAT early
		if os.Getenv("GROVE_PAT") == "" {
			return fmt.Errorf("GROVE_PAT environment variable not set. This is required for setting up GitHub Actions secrets")
		}
	}

	return nil
}

func (c *Creator) generateSkeleton(opts CreateOptions) error {
	c.logger.Info("Generating repository skeleton...")

	// Get latest versions
	data := templates.TemplateData{
		RepoName:         opts.Name,
		BinaryAlias:      opts.Alias,
		BinaryAliasUpper: strings.ToUpper(opts.Alias),
		Description:      opts.Description,
		GoVersion:        "1.24.4",
		CoreVersion:      c.getLatestVersion("grove-core"),
		TendVersion:      c.getLatestVersion("grove-tend"),
	}

	// Always use external template (TemplatePath defaults to "go" which resolves to the Go template)
	return c.generateFromExternalTemplate(opts, data)
}

func (c *Creator) generateFromExternalTemplate(opts CreateOptions, data templates.TemplateData) error {
	c.logger.Infof("Using external template from: %s", opts.TemplatePath)

	// Find the grove root directory
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find grove root: %w", err)
	}

	var fetcher templates.Fetcher
	var templateDir string

	// Determine which fetcher to use based on the template path
	if templates.IsGitURL(opts.TemplatePath) {
		// Use GitFetcher for Git URLs
		gitFetcher, err := templates.NewGitFetcher()
		if err != nil {
			return fmt.Errorf("failed to create git fetcher: %w", err)
		}
		fetcher = gitFetcher
		defer func() {
			if err := fetcher.Cleanup(); err != nil {
				c.logger.Warnf("Failed to cleanup fetcher: %v", err)
			}
		}()
		
		templateDir, err = fetcher.Fetch(opts.TemplatePath)
		if err != nil {
			return fmt.Errorf("failed to fetch template: %w", err)
		}
	} else {
		// Use LocalFetcher for local paths
		localFetcher := templates.NewLocalFetcher()
		fetcher = localFetcher
		defer func() {
			if err := fetcher.Cleanup(); err != nil {
				c.logger.Warnf("Failed to cleanup fetcher: %v", err)
			}
		}()
		
		templateDir, err = fetcher.Fetch(opts.TemplatePath)
		if err != nil {
			return fmt.Errorf("failed to fetch template: %w", err)
		}
	}

	// Create renderer
	renderer := templates.NewRenderer()

	// Render template to target directory in the grove root
	targetPath := filepath.Join(rootDir, opts.Name)
	if err := renderer.Render(templateDir, targetPath, data); err != nil {
		return fmt.Errorf("failed to render template: %w", err)
	}

	// Format Go files (if any)
	c.logger.Info("Formatting Go files...")
	fmtCmd := exec.Command("gofmt", "-w", ".")
	fmtCmd.Dir = targetPath
	if err := fmtCmd.Run(); err != nil {
		// Don't fail if gofmt isn't available, just warn
		c.logger.Warnf("Failed to format Go files: %v", err)
	}

	// Initialize git repository
	c.logger.Info("Initializing git repository...")
	gitInit := exec.Command("git", "init")
	gitInit.Dir = targetPath
	if err := gitInit.Run(); err != nil {
		return fmt.Errorf("failed to initialize git: %w", err)
	}

	// Add all files
	gitAdd := exec.Command("git", "add", ".")
	gitAdd.Dir = targetPath
	if err := gitAdd.Run(); err != nil {
		return fmt.Errorf("failed to add files: %w", err)
	}

	// Create initial commit
	gitCommit := exec.Command("git", "commit", "-m", "feat: initial repository setup\n\nCreated new Grove repository with:\n- Standard project structure\n- CLI framework with version command\n- Testing setup (unit and e2e)\n- CI/CD workflows\n- Documentation templates")
	gitCommit.Dir = targetPath
	if err := gitCommit.Run(); err != nil {
		return fmt.Errorf("failed to create initial commit: %w", err)
	}

	// Install git hooks
	c.logger.Info("Installing git hooks...")
	hooksCmd := exec.Command("grove", "git-hooks", "install")
	hooksCmd.Dir = targetPath
	if output, err := hooksCmd.CombinedOutput(); err != nil {
		// Don't fail if hooks can't be installed, just warn
		c.logger.Warnf("Failed to install git hooks: %v\nOutput: %s", err, string(output))
		c.logger.Warn("You can install them manually later with: grove git-hooks install")
	}

	return nil
}

func (c *Creator) verifyLocal(opts CreateOptions) error {
	repoPath := filepath.Join(".", opts.Name)

	// Determine project type based on project files
	projectType := c.detectProjectType(repoPath)
	c.logger.WithField("type", projectType).Info("Detected project type")

	// Handle language-specific dependency resolution
	switch projectType {
	case "go":
		// Run go mod tidy first to resolve dependencies
		c.logger.Info("Resolving dependencies...")
		tidyCmd := exec.Command("go", "mod", "tidy")
		tidyCmd.Dir = repoPath
		tidyCmd.Env = append(os.Environ(),
			"GOPRIVATE=github.com/mattsolo1/*",
			"GOPROXY=direct",
		)
		if output, err := tidyCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go mod tidy failed: %w\nOutput: %s", err, string(output))
		}

		// Run go mod download
		c.logger.Info("Downloading dependencies...")
		modCmd := exec.Command("go", "mod", "download")
		modCmd.Dir = repoPath
		modCmd.Env = append(os.Environ(),
			"GOPRIVATE=github.com/mattsolo1/*",
			"GOPROXY=direct",
		)
		if err := modCmd.Run(); err != nil {
			return fmt.Errorf("go mod download failed: %w", err)
		}
		
	case "maturin":
		// For Python/Maturin projects, check if uv is available
		c.logger.Info("Checking for uv (Python package manager)...")
		if _, err := exec.LookPath("uv"); err != nil {
			c.logger.Warn("uv not found. Please install uv for faster Python package management: https://github.com/astral-sh/uv")
			c.logger.Info("Continuing with standard Python tools...")
		}
		// The Makefile will handle the actual setup
		c.logger.Info("Python environment setup will be handled by Makefile")
		
	case "node":
		// For Node.js projects, run npm install
		c.logger.Info("Installing Node.js dependencies...")
		setupCmd := exec.Command("make", "setup")
		setupCmd.Dir = repoPath
		if output, err := setupCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("make setup failed: %w\nOutput: %s", err, string(output))
		}
		
	default:
		c.logger.Info("No language-specific dependency resolution needed")
	}

	// Run make-based verification (works for all project types)
	// Check if Makefile exists first
	makefilePath := filepath.Join(repoPath, "Makefile")
	if _, err := os.Stat(makefilePath); err == nil {
		// Build the project
		c.logger.Info("Building project...")
		buildCmd := exec.Command("make", "build")
		buildCmd.Dir = repoPath
		if output, err := buildCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("make build failed: %w\nOutput: %s", err, string(output))
		}

		// Run tests if available
		c.logger.Info("Running tests...")
		testCmd := exec.Command("make", "test")
		testCmd.Dir = repoPath
		if output, err := testCmd.CombinedOutput(); err != nil {
			// Don't fail if test target doesn't exist
			if !strings.Contains(string(output), "No rule to make target") {
				return fmt.Errorf("make test failed: %w\nOutput: %s", err, string(output))
			}
			c.logger.Warn("No test target found, skipping tests")
		}

		// Run e2e tests if available
		c.logger.Info("Running e2e tests...")
		e2eCmd := exec.Command("make", "test-e2e")
		e2eCmd.Dir = repoPath
		if output, err := e2eCmd.CombinedOutput(); err != nil {
			// Don't fail if test-e2e target doesn't exist
			if !strings.Contains(string(output), "No rule to make target") {
				return fmt.Errorf("make test-e2e failed: %w\nOutput: %s", err, string(output))
			}
			c.logger.Warn("No test-e2e target found, skipping e2e tests")
		}
	} else {
		c.logger.Warn("No Makefile found, skipping build verification")
	}

	c.logger.Info("✅ Local verification passed")
	return nil
}

// detectProjectType determines the project type based on project files
func (c *Creator) detectProjectType(repoPath string) string {
	// Check for go.mod
	if _, err := os.Stat(filepath.Join(repoPath, "go.mod")); err == nil {
		return "go"
	}
	
	// Check for pyproject.toml (Maturin/Python projects)
	if _, err := os.Stat(filepath.Join(repoPath, "pyproject.toml")); err == nil {
		return "maturin"
	}
	
	// Check for package.json (Node projects)
	if _, err := os.Stat(filepath.Join(repoPath, "package.json")); err == nil {
		return "node"
	}
	
	// Default to unknown
	return "unknown"
}

func (c *Creator) createGitHubRepo(opts CreateOptions) error {
	c.logger.Info("Creating GitHub repository...")

	// Create the repository without pushing
	createCmd := exec.Command("gh", "repo", "create",
		fmt.Sprintf("mattsolo1/%s", opts.Name),
		"--private",
		"--source=.",
		"--remote=origin")
	createCmd.Dir = opts.Name

	if output, err := createCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create GitHub repository: %w\nOutput: %s", err, string(output))
	}

	// Ensure the remote URL is clean (no embedded tokens)
	c.logger.Info("Ensuring clean remote URL...")
	setUrlCmd := exec.Command("git", "remote", "set-url", "origin", 
		fmt.Sprintf("https://github.com/mattsolo1/%s.git", opts.Name))
	setUrlCmd.Dir = opts.Name
	if err := setUrlCmd.Run(); err != nil {
		c.logger.Warnf("Failed to set clean remote URL: %v", err)
	}

	c.logger.Info("✅ GitHub repository created")
	return nil
}

func (c *Creator) pushToGitHub(opts CreateOptions) error {
	c.logger.Info("Pushing to GitHub...")
	pushCmd := exec.Command("git", "push", "-u", "origin", "main")
	pushCmd.Dir = opts.Name
	if output, err := pushCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to push to GitHub: %w\nOutput: %s", err, string(output))
	}

	c.logger.Info("✅ Code pushed to GitHub")
	return nil
}

func (c *Creator) setupSecrets(opts CreateOptions) error {
	c.logger.Info("Setting up repository secrets...")

	// We already validated GROVE_PAT exists in the validate phase
	grovePAT := os.Getenv("GROVE_PAT")

	// Set the secret
	secretCmd := exec.Command("gh", "secret", "set", "GROVE_PAT", "--body", grovePAT)
	secretCmd.Dir = opts.Name

	if err := secretCmd.Run(); err != nil {
		return fmt.Errorf("failed to set GROVE_PAT secret: %w", err)
	}

	c.logger.Info("✅ Repository secrets configured")
	return nil
}

func (c *Creator) createInitialRelease(opts CreateOptions) error {
	c.logger.Info("Creating initial release v0.0.1...")

	// Create tag
	tagCmd := exec.Command("git", "tag", "v0.0.1", "-m", "Initial release")
	tagCmd.Dir = opts.Name
	if err := tagCmd.Run(); err != nil {
		return fmt.Errorf("failed to create tag: %w", err)
	}

	if !opts.SkipGitHub {
		// Push tag
		pushCmd := exec.Command("git", "push", "origin", "v0.0.1")
		pushCmd.Dir = opts.Name
		if err := pushCmd.Run(); err != nil {
			return fmt.Errorf("failed to push tag: %w", err)
		}
	}

	c.logger.Info("✅ Initial release created")
	return nil
}

func (c *Creator) waitForCI(opts CreateOptions) error {
	c.logger.Info("Waiting for release build to complete...")

	// Wait a moment for the workflow to start
	time.Sleep(2 * time.Second)

	// Get the workflow run ID
	cmd := exec.Command("gh", "run", "list",
		"--repo", fmt.Sprintf("mattsolo1/%s", opts.Name),
		"--workflow", "release.yml",
		"--limit", "1",
		"--json", "databaseId",
		"--jq", ".[0].databaseId")

	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get workflow run: %w", err)
	}

	runID := strings.TrimSpace(string(output))
	if runID == "" {
		c.logger.Warn("No release workflow found yet, it may still be starting...")
		return nil  // Don't fail, just continue
	}

	// Watch the workflow
	watchCmd := exec.Command("gh", "run", "watch", runID,
		"--repo", fmt.Sprintf("mattsolo1/%s", opts.Name))
	watchCmd.Stdout = os.Stdout
	watchCmd.Stderr = os.Stderr

	if err := watchCmd.Run(); err != nil {
		return fmt.Errorf("release build failed: %w", err)
	}

	c.logger.Info("✅ Release build completed successfully")
	return nil
}

func (c *Creator) addToEcosystem(opts CreateOptions) error {
	c.logger.Info("Adding repository to grove-ecosystem...")

	if !opts.SkipGitHub {
		// Check if submodule already exists
		checkCmd := exec.Command("git", "submodule", "status", opts.Name)
		if err := checkCmd.Run(); err == nil {
			c.logger.Warnf("Submodule %s already exists, updating it...", opts.Name)
			
			// Update the submodule URL in case it changed
			updateUrlCmd := exec.Command("git", "submodule", "set-url", opts.Name,
				fmt.Sprintf("git@github.com:mattsolo1/%s.git", opts.Name))
			if err := updateUrlCmd.Run(); err != nil {
				c.logger.Warnf("Failed to update submodule URL: %v", err)
			}
		} else {
			// Add as submodule only if it doesn't exist
			submoduleCmd := exec.Command("git", "submodule", "add",
				fmt.Sprintf("git@github.com:mattsolo1/%s.git", opts.Name),
				opts.Name)

			if output, err := submoduleCmd.CombinedOutput(); err != nil {
				// Check if it's the "already exists" error
				if strings.Contains(string(output), "already exists in the index") {
					c.logger.Warn("Submodule already in index, continuing...")
				} else {
					return fmt.Errorf("failed to add submodule: %w\nOutput: %s", err, string(output))
				}
			}
		}
	}

	// Update Makefile
	if err := c.updateMakefile(opts); err != nil {
		return fmt.Errorf("failed to update Makefile: %w", err)
	}

	// go.work was already updated earlier for local verification, so skip here

	c.logger.Info("✅ Repository added to ecosystem")
	return nil
}

func (c *Creator) updateMakefile(opts CreateOptions) error {
	// Makefile updates are no longer needed - grove.yml workspace discovery handles this
	return nil
}

func (c *Creator) updateGoWork(opts CreateOptions) error {
	// Only update go.work for Go-based templates
	if !isGoTemplate(opts.TemplatePath) {
		c.logger.Info("Skipping go.work update for non-Go template")
		return nil
	}
	return updateGoWork(opts.Name)
}

// isGoTemplate checks if the template is Go-based
func isGoTemplate(templatePath string) bool {
	// Check if it's the default "go" template or a path containing "go"
	return templatePath == "go" || strings.Contains(templatePath, "tmpl-go")
}

func (c *Creator) stageEcosystemChanges(opts CreateOptions) error {
	c.logger.Info("Staging ecosystem changes...")

	// Always stage Makefile
	filesToStage := []string{
		"Makefile",
	}
	
	// Only stage go.work if this is a Go project
	repoPath := filepath.Join(".", opts.Name)
	projectType := c.detectProjectType(repoPath)
	if projectType == "go" {
		filesToStage = append(filesToStage, "go.work")
	}

	if !opts.SkipGitHub {
		// Only stage submodule-related files if GitHub repo was created
		filesToStage = append(filesToStage, ".gitmodules", opts.Name)
	}

	args := append([]string{"add"}, filesToStage...)
	cmd := exec.Command("git", args...)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stage changes: %w", err)
	}

	c.logger.Info("✅ Changes staged successfully")
	return nil
}

func (c *Creator) showSummary(opts CreateOptions) error {
	fmt.Println("\n✅ All operations completed successfully!")
	fmt.Println("\nSUMMARY")
	fmt.Println("-------")
	fmt.Printf("- Local repository created at: ./%s\n", opts.Name)

	if !opts.SkipGitHub {
		fmt.Printf("- GitHub repository created:  https://github.com/mattsolo1/%s\n", opts.Name)
		fmt.Println("- Initial release v0.0.1 pushed and CI is running.")
	}

	fmt.Println("- Submodule added to grove-ecosystem.")
	fmt.Println("- Root Makefile and go.work have been updated.")

	fmt.Println("\nNEXT STEPS")
	fmt.Println("----------")
	
	if opts.StageChanges {
		fmt.Println("1. Review the staged changes in the ecosystem repo:")
		fmt.Println("   > git status")
		fmt.Println("   (You should see a modified Makefile, go.work, .gitmodules, and a new submodule entry)")
		fmt.Println("")
		fmt.Printf("2. Commit the integration to the main branch:\n")
		fmt.Printf("   > git commit -m \"feat: add %s to the ecosystem\"\n", opts.Name)
		fmt.Println("")
		fmt.Printf("3. Start developing your new tool:\n")
		fmt.Printf("   > cd %s\n", opts.Name)
		fmt.Printf("   > grove install %s\n", opts.Alias)
	} else {
		fmt.Println("1. Stage and commit the ecosystem changes:")
		fmt.Println("   > git add Makefile go.work .gitmodules " + opts.Name)
		fmt.Printf("   > git commit -m \"feat: add %s to the ecosystem\"\n", opts.Name)
		fmt.Println("")
		fmt.Printf("2. Start developing your new tool:\n")
		fmt.Printf("   > cd %s\n", opts.Name)
		fmt.Printf("   > grove install %s\n", opts.Alias)
	}

	return nil
}

func (c *Creator) rollback(state *creationState, opts CreateOptions) {
	c.logger.Warn("Error occurred, rolling back changes...")

	if state.githubRepoCreated && !opts.SkipGitHub {
		c.logger.Warnf("GitHub repository was created but setup failed.")
		c.logger.Warnf("Please delete it manually: https://github.com/mattsolo1/%s", opts.Name)
		c.logger.Warnf("Run: gh repo delete mattsolo1/%s", opts.Name)
	}

	if state.localRepoCreated {
		c.logger.Info("Removing local directory...")
		if err := os.RemoveAll(opts.Name); err != nil {
			c.logger.Errorf("Failed to remove directory: %v", err)
		}

		// Also remove from go.work if it was added
		c.logger.Info("Removing from go.work...")
		eco := &Ecosystem{logger: c.logger}
		if err := eco.removeFromGoWork(opts.Name); err != nil {
			c.logger.Errorf("Failed to remove from go.work: %v", err)
		}

		// Try to remove from git index if it was added as submodule
		c.logger.Info("Cleaning up git submodule...")
		rmCmd := exec.Command("git", "rm", "--cached", "-f", opts.Name)
		if err := rmCmd.Run(); err != nil {
			// This is okay if it wasn't added
			c.logger.Debugf("Submodule cleanup: %v", err)
		}
	}
}

func (c *Creator) dryRun(opts CreateOptions) error {
	c.logger.Info("DRY RUN MODE - No changes will be made")

	// Show what would be created
	c.logger.Infof("\nWould create repository structure in: ./%s", opts.Name)
	c.logger.Infof("Binary alias: %s", opts.Alias)
	c.logger.Infof("Description: %s", opts.Description)

	// Show commands that would be executed
	c.logger.Info("\nCommands that would be executed:")
	c.logger.Info("  git init")
	c.logger.Info("  git add .")
	c.logger.Info("  git commit -m 'feat: initial repository setup'")

	if !opts.SkipGitHub {
		c.logger.Infof("  gh repo create mattsolo1/%s --private", opts.Name)
		c.logger.Infof("  git remote add origin git@github.com:mattsolo1/%s.git", opts.Name)
		c.logger.Info("  git push -u origin main")
		c.logger.Info("  gh secret set GROVE_PAT --body <GROVE_PAT>")
		c.logger.Info("  git tag v0.0.1")
		c.logger.Info("  git push origin v0.0.1")
		c.logger.Info("  gh run watch")
	}

	c.logger.Info("\nEcosystem integration:")
	c.logger.Infof("  git submodule add git@github.com:mattsolo1/%s.git", opts.Name)
	c.logger.Infof("  Update Makefile: Add %s to PACKAGES", opts.Name)
	c.logger.Infof("  Update Makefile: Add %s to BINARIES", opts.Alias)
	c.logger.Infof("  Update go.work: Add use (./%s)", opts.Name)
	
	if opts.StageChanges {
		c.logger.Infof("  git add Makefile go.work .gitmodules %s", opts.Name)
	}

	return nil
}

func (c *Creator) getLatestVersion(repo string) string {
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/mattsolo1/%s/releases/latest", repo),
		"--jq", ".tag_name")
	output, err := cmd.Output()
	if err != nil {
		// Fallback to a reasonable default
		switch repo {
		case "grove-core":
			return "v0.2.10"
		case "grove-tend":
			return "v0.2.6"
		default:
			return "v0.0.1"
		}
	}
	return strings.TrimSpace(string(output))
}