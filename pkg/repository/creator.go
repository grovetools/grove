package repository

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/core/util/delegation"
	"github.com/grovetools/grove/pkg/gh"
	"github.com/grovetools/grove/pkg/templates"
	"github.com/sirupsen/logrus"
)

type Creator struct {
	logger *logrus.Logger
	tmpl   *templates.Manager
	gh     *gh.Client
}

// CreateOptions contains options for creating a new repository (local creation)
type CreateOptions struct {
	Name         string
	Alias        string
	Description  string
	SkipGitHub   bool // Deprecated: use CreateLocal for local-only, then InitializeGitHub separately
	DryRun       bool
	TemplatePath string
	Ecosystem    bool
	Public       bool
}

// GitHubInitOptions contains options for initializing GitHub integration
type GitHubInitOptions struct {
	Visibility string // "public" or "private" (default: "private")
	DryRun     bool
}

type creationState struct {
	localRepoCreated     bool
	githubRepoCreated    bool
	originalGoWorkContent []byte
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
		c.logger.Errorf("Validation failed: %v", err)
		return err
	}

	// Handle dry-run mode
	if opts.DryRun {
		return c.dryRun(opts)
	}

	// Determine target path based on mode
	var targetPath string
	if opts.Ecosystem {
		// In ecosystem mode, we'll use the current directory as the root
		// (grove.yml will be created in validate if it doesn't exist)
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		targetPath = filepath.Join(cwd, opts.Name)
	} else {
		// Standalone mode - create in current directory
		targetPath = filepath.Join(".", opts.Name)
	}

	// Check if directory already exists
	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("directory %s already exists", targetPath)
	}

	// Track state for rollback
	state := &creationState{}

	// Phase 2: Generate local skeleton
	if err := c.generateSkeleton(opts, targetPath); err != nil {
		// Since generateSkeleton creates the directory, we need to mark it as created
		// for rollback to clean it up
		state.localRepoCreated = true
		c.rollback(state, opts, targetPath)
		return fmt.Errorf("failed to generate skeleton: %w", err)
	}
	state.localRepoCreated = true

	// Phase 2.5: Detect project type early
	projectType := c.detectProjectType(targetPath)

	// Phase 2.6: Add to go.work temporarily for Go projects only (in ecosystem mode)
	if projectType == "go" && opts.Ecosystem {
		// Snapshot go.work before modifying it
		rootDir, err := workspace.FindEcosystemRoot("")
		if err == nil {
			goWorkPath := filepath.Join(rootDir, "go.work")
			if content, err := os.ReadFile(goWorkPath); err == nil {
				state.originalGoWorkContent = content
			}
		}
		
		if err := c.updateGoWork(opts); err != nil {
			c.rollback(state, opts, targetPath)
			return fmt.Errorf("failed to update go.work: %w", err)
		}
	}

	// Phase 2.7: Local verification
	c.logger.Info("Running local verification...")
	if err := c.verifyLocal(opts, targetPath); err != nil {
		c.rollback(state, opts, targetPath)
		return fmt.Errorf("local verification failed: %w", err)
	}

	// Phase 3: GitHub operations
	if !opts.SkipGitHub {
		// Create repo first
		if err := c.createGitHubRepo(opts, targetPath); err != nil {
			c.rollback(state, opts, targetPath)
			return err
		}
		state.githubRepoCreated = true

		// Set up secrets BEFORE pushing
		if err := c.setupSecrets(opts, targetPath); err != nil {
			c.rollback(state, opts, targetPath)
			return err
		}

		// Now push the code
		if err := c.pushToGitHub(opts, targetPath); err != nil {
			c.rollback(state, opts, targetPath)
			return err
		}
	}

	// Phase 4: Initial release
	if err := c.createInitialRelease(opts, targetPath); err != nil {
		c.rollback(state, opts, targetPath)
		return err
	}

	// Wait for CI to complete if GitHub repo was created and .github directory exists
	if !opts.SkipGitHub {
		githubDir := filepath.Join(targetPath, ".github")
		if _, err := os.Stat(githubDir); err == nil {
			// .github directory exists, wait for CI
			if err := c.waitForCI(opts); err != nil {
				c.logger.Warnf("Could not monitor CI build: %v", err)
				c.logger.Infof("Repository created successfully. Check CI status at: https://github.com/grovetools/%s/actions", opts.Name)
			}
		} else {
			// No .github directory, skip CI monitoring
			c.logger.Info("No .github directory found, skipping CI monitoring")
			c.logger.Infof("Repository created successfully at: https://github.com/grovetools/%s", opts.Name)
		}
	}

	// Phase 5: Add to ecosystem (only if requested)
	if opts.Ecosystem {
		if err := c.addToEcosystem(opts); err != nil {
			c.rollback(state, opts, targetPath)
			return fmt.Errorf("failed to add repository to ecosystem: %w\n\nNote: The grove-ecosystem may have been partially modified.\nYou can clean up with: git reset --hard", err)
		}
	}

	// Phase 6: Final summary
	return c.showSummary(opts, targetPath)
}

// CreateLocal creates a new local repository without any GitHub integration.
// This is the first step in the incremental workflow: create locally, then optionally
// call InitializeGitHub to add GitHub integration later.
func (c *Creator) CreateLocal(opts CreateOptions) (string, error) {
	// Force SkipGitHub for local-only creation
	opts.SkipGitHub = true

	// Phase 1: Validation (local-only)
	if err := c.validateLocal(opts); err != nil {
		c.logger.Errorf("Validation failed: %v", err)
		return "", err
	}

	// Handle dry-run mode
	if opts.DryRun {
		return "", c.dryRunLocal(opts)
	}

	// Determine target path based on mode
	var targetPath string
	if opts.Ecosystem {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current directory: %w", err)
		}
		targetPath = filepath.Join(cwd, opts.Name)
	} else {
		targetPath = filepath.Join(".", opts.Name)
	}

	// Check if directory already exists
	if _, err := os.Stat(targetPath); err == nil {
		return "", fmt.Errorf("directory %s already exists", targetPath)
	}

	// Track state for rollback
	state := &creationState{}

	// Phase 2: Generate local skeleton
	if err := c.generateSkeleton(opts, targetPath); err != nil {
		state.localRepoCreated = true
		c.rollback(state, opts, targetPath)
		return "", fmt.Errorf("failed to generate skeleton: %w", err)
	}
	state.localRepoCreated = true

	// Phase 2.5: Detect project type early
	projectType := c.detectProjectType(targetPath)

	// Phase 2.6: Add to go.work temporarily for Go projects only (in ecosystem mode)
	if projectType == "go" && opts.Ecosystem {
		rootDir, err := workspace.FindEcosystemRoot("")
		if err == nil {
			goWorkPath := filepath.Join(rootDir, "go.work")
			if content, err := os.ReadFile(goWorkPath); err == nil {
				state.originalGoWorkContent = content
			}
		}

		if err := c.updateGoWork(opts); err != nil {
			c.rollback(state, opts, targetPath)
			return "", fmt.Errorf("failed to update go.work: %w", err)
		}
	}

	// Phase 2.7: Local verification
	c.logger.Info("Running local verification...")
	if err := c.verifyLocal(opts, targetPath); err != nil {
		c.rollback(state, opts, targetPath)
		return "", fmt.Errorf("local verification failed: %w", err)
	}

	// Phase 3: Add to ecosystem (only if requested)
	if opts.Ecosystem {
		if err := c.addToEcosystemLocal(opts); err != nil {
			c.rollback(state, opts, targetPath)
			return "", fmt.Errorf("failed to add repository to ecosystem: %w\n\nNote: The grove-ecosystem may have been partially modified.\nYou can clean up with: git reset --hard", err)
		}
	}

	// Phase 5: Show local summary
	c.showLocalSummary(opts, targetPath)

	return targetPath, nil
}

// InitializeGitHub adds GitHub integration to an existing local Grove repository.
// This should be called from within the repository directory (where grove.yml exists).
func (c *Creator) InitializeGitHub(opts GitHubInitOptions) error {
	// Set default visibility
	if opts.Visibility == "" {
		opts.Visibility = "private"
	}

	// Get current directory (must be the repo root)
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Validate: must be a Grove repository
	groveYmlPath := filepath.Join(cwd, "grove.yml")
	if _, err := os.Stat(groveYmlPath); os.IsNotExist(err) {
		return fmt.Errorf("not a Grove repository: grove.yml not found in current directory\n\nRun this command from within a Grove repository created with 'grove repo add'")
	}

	// Get repo name from directory name
	repoName := filepath.Base(cwd)

	// Validate: must not already have origin remote
	checkOriginCmd := exec.Command("git", "remote", "get-url", "origin")
	if err := checkOriginCmd.Run(); err == nil {
		return fmt.Errorf("repository already has a remote 'origin' configured\n\nTo change the remote, use 'git remote remove origin' first")
	}

	// Validate GitHub prerequisites
	if err := c.validateGitHubPrereqs(repoName, opts.Visibility == "public"); err != nil {
		return err
	}

	// Handle dry-run mode
	if opts.DryRun {
		return c.dryRunGitHubInit(repoName, opts)
	}

	// Create GitHub repository
	c.logger.Infof("Creating GitHub repository grovetools/%s...", repoName)
	if err := c.createGitHubRepoFromCwd(repoName, opts.Visibility == "public"); err != nil {
		return err
	}

	// Set up secrets (only for private repos)
	if opts.Visibility != "public" {
		if err := c.setupSecretsFromCwd(repoName); err != nil {
			c.logger.Warnf("Failed to set up secrets: %v", err)
			c.logger.Warn("You may need to manually set up GROVE_PAT secret")
		}
	}

	// Push code and tags
	c.logger.Info("Pushing code to GitHub...")
	if err := c.pushToGitHubFromCwd(); err != nil {
		return err
	}

	// Wait for CI if .github directory exists
	githubDir := filepath.Join(cwd, ".github")
	if _, err := os.Stat(githubDir); err == nil {
		c.logger.Info("Waiting for CI to complete...")
		if err := c.waitForCIFromCwd(repoName); err != nil {
			c.logger.Warnf("Could not monitor CI build: %v", err)
			c.logger.Infof("Check CI status at: https://github.com/grovetools/%s/actions", repoName)
		}
	}

	// Show success summary
	fmt.Println("\n GitHub integration complete!")
	fmt.Printf("Repository URL: https://github.com/grovetools/%s\n", repoName)

	return nil
}

// validateLocal validates options for local-only repository creation
func (c *Creator) validateLocal(opts CreateOptions) error {
	c.logger.Info("Validating repository configuration...")

	// Validate repository name
	if !isValidRepoName(opts.Name) {
		return fmt.Errorf("invalid repository name: must only contain lowercase letters, numbers, and hyphens")
	}

	// Validate alias
	if opts.Alias == "" {
		return fmt.Errorf("binary alias cannot be empty")
	}

	// Check if we're in grove-ecosystem root (only if ecosystem mode)
	if opts.Ecosystem {
		if _, err := os.Stat("grove.yml"); err != nil {
			return fmt.Errorf("no grove.yml found in the current directory.\n\nTo create a new Grove ecosystem, run:\n  grove ws init\n\nOr to create a standalone repository without an ecosystem:\n  grove repo add %s --alias %s (without --ecosystem flag)", opts.Name, opts.Alias)
		}
		// Check for alias conflicts in existing ecosystem
		if err := checkBinaryAliasConflict(opts.Alias); err != nil {
			return err
		}
	}

	// Check dependencies
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is not installed or not in PATH")
	}

	return nil
}

// validateGitHubPrereqs validates GitHub prerequisites for InitializeGitHub
func (c *Creator) validateGitHubPrereqs(repoName string, isPublic bool) error {
	// Check for gh CLI
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("GitHub CLI (gh) is not installed or not in PATH")
	}

	// Check GitHub authentication
	cmd := exec.Command("gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("not authenticated with GitHub. Run 'gh auth login' first")
	}

	// Check if repo already exists on GitHub
	checkCmd := exec.Command("gh", "repo", "view", fmt.Sprintf("grovetools/%s", repoName))
	if err := checkCmd.Run(); err == nil {
		return fmt.Errorf("repository grovetools/%s already exists on GitHub", repoName)
	}

	// Check for GROVE_PAT (only for private repos)
	if !isPublic && os.Getenv("GROVE_PAT") == "" {
		return fmt.Errorf("GROVE_PAT environment variable not set. This is required for setting up GitHub Actions secrets for private repositories.\n\nOptions:\n  1. Set GROVE_PAT and try again\n  2. Use --visibility=public for a public repository")
	}

	return nil
}

// createLocalTag creates the initial v0.0.1 tag locally (without pushing)
func (c *Creator) createLocalTag(opts CreateOptions, targetPath string) error {
	c.logger.Info("Creating initial release tag v0.0.1...")

	tagCmd := exec.Command("git", "tag", "v0.0.1", "-m", "Initial release")
	tagCmd.Dir = targetPath
	if err := tagCmd.Run(); err != nil {
		return fmt.Errorf("failed to create tag: %w", err)
	}

	c.logger.Info(" Initial release tag created")
	return nil
}

// addToEcosystemLocal adds the repository to the ecosystem without GitHub submodule URL
func (c *Creator) addToEcosystemLocal(opts CreateOptions) error {
	c.logger.Info("Adding repository to grove-ecosystem...")

	// Check if submodule already exists
	checkCmd := exec.Command("git", "submodule", "status", opts.Name)
	if err := checkCmd.Run(); err == nil {
		c.logger.Warnf("Submodule %s already exists", opts.Name)
		return nil
	}

	// Add as submodule using local path
	submoduleUrl := "./" + opts.Name
	submoduleCmd := exec.Command("git", "submodule", "add", submoduleUrl, opts.Name)

	if output, err := submoduleCmd.CombinedOutput(); err != nil {
		if strings.Contains(string(output), "already exists in the index") {
			c.logger.Warn("Submodule already in index, continuing...")
		} else {
			return fmt.Errorf("failed to add submodule: %w\nOutput: %s", err, string(output))
		}
	}

	c.logger.Info(" Repository added to ecosystem")
	return nil
}

// showLocalSummary displays summary after local-only creation
func (c *Creator) showLocalSummary(opts CreateOptions, targetPath string) {
	fmt.Println("\n Local repository created successfully!")
	fmt.Printf("\nRepository: %s\n", targetPath)

	if opts.Ecosystem {
		fmt.Println("\nNext steps:")
		fmt.Println("  git add go.work .gitmodules " + opts.Name)
		fmt.Printf("  git commit -m \"feat: add %s\"\n", opts.Name)
		fmt.Printf("  cd %s\n", opts.Name)
	} else {
		fmt.Printf("\ncd %s\n", opts.Name)
	}
}

// dryRunLocal shows what would be created in local-only mode
func (c *Creator) dryRunLocal(opts CreateOptions) error {
	c.logger.Info("DRY RUN MODE - No changes will be made")

	c.logger.Infof("\nWould create repository in: ./%s", opts.Name)
	c.logger.Infof("Description: %s", opts.Description)

	if opts.TemplatePath == "" {
		c.logger.Info("\nFiles that would be created (minimal repo):")
		c.logger.Info("  README.md")
		c.logger.Info("  grove.yml")
	} else {
		c.logger.Infof("\nTemplate: %s", opts.TemplatePath)
		c.logger.Info("Files would be created from template")
	}

	c.logger.Info("\nGit commands:")
	c.logger.Info("  git init")
	c.logger.Info("  git add .")
	c.logger.Info("  git commit -m 'feat: initial repository setup'")

	if opts.Ecosystem {
		c.logger.Info("\nEcosystem integration:")
		c.logger.Infof("  git submodule add ./%s %s", opts.Name, opts.Name)
	}

	return nil
}

// dryRunGitHubInit shows what would be done for GitHub initialization
func (c *Creator) dryRunGitHubInit(repoName string, opts GitHubInitOptions) error {
	c.logger.Info("DRY RUN MODE - No changes will be made")

	c.logger.Infof("\nWould initialize GitHub for repository: %s", repoName)
	c.logger.Infof("Visibility: %s", opts.Visibility)

	c.logger.Info("\nCommands that would be executed:")
	if opts.Visibility == "public" {
		c.logger.Infof("  gh repo create grovetools/%s --public --source=. --remote=origin", repoName)
	} else {
		c.logger.Infof("  gh repo create grovetools/%s --private --source=. --remote=origin", repoName)
		c.logger.Info("  gh secret set GROVE_PAT --body <GROVE_PAT>")
	}
	c.logger.Info("  git push -u origin main")
	c.logger.Info("  git push origin --tags")
	c.logger.Info("  gh run watch (if .github workflows exist)")

	return nil
}

// createGitHubRepoFromCwd creates GitHub repo when running from the repo directory
func (c *Creator) createGitHubRepoFromCwd(repoName string, isPublic bool) error {
	createArgs := []string{"repo", "create", fmt.Sprintf("grovetools/%s", repoName)}
	if isPublic {
		createArgs = append(createArgs, "--public")
	} else {
		createArgs = append(createArgs, "--private")
	}
	createArgs = append(createArgs, "--source=.", "--remote=origin")
	createCmd := exec.Command("gh", createArgs...)

	if output, err := createCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create GitHub repository: %w\nOutput: %s", err, string(output))
	}

	// Ensure the remote URL is clean
	setUrlCmd := exec.Command("git", "remote", "set-url", "origin",
		fmt.Sprintf("https://github.com/grovetools/%s.git", repoName))
	if err := setUrlCmd.Run(); err != nil {
		c.logger.Warnf("Failed to set clean remote URL: %v", err)
	}

	c.logger.Info(" GitHub repository created")
	return nil
}

// setupSecretsFromCwd sets up secrets when running from the repo directory
func (c *Creator) setupSecretsFromCwd(repoName string) error {
	c.logger.Info("Setting up repository secrets...")

	grovePAT := os.Getenv("GROVE_PAT")
	if grovePAT == "" {
		return fmt.Errorf("GROVE_PAT not set")
	}

	secretCmd := exec.Command("gh", "secret", "set", "GROVE_PAT", "--body", grovePAT)
	if err := secretCmd.Run(); err != nil {
		return fmt.Errorf("failed to set GROVE_PAT secret: %w", err)
	}

	c.logger.Info(" Repository secrets configured")
	return nil
}

// pushToGitHubFromCwd pushes code and tags when running from the repo directory
func (c *Creator) pushToGitHubFromCwd() error {
	// Push main branch
	pushCmd := exec.Command("git", "push", "-u", "origin", "main")
	if output, err := pushCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to push to GitHub: %w\nOutput: %s", err, string(output))
	}

	// Push tags
	pushTagsCmd := exec.Command("git", "push", "origin", "--tags")
	if output, err := pushTagsCmd.CombinedOutput(); err != nil {
		c.logger.Warnf("Failed to push tags: %v\nOutput: %s", err, string(output))
	}

	c.logger.Info(" Code pushed to GitHub")
	return nil
}

// waitForCIFromCwd waits for CI when running from the repo directory
func (c *Creator) waitForCIFromCwd(repoName string) error {
	// Wait a moment for the workflow to start
	time.Sleep(2 * time.Second)

	// Get the workflow run ID
	cmd := exec.Command("gh", "run", "list",
		"--repo", fmt.Sprintf("grovetools/%s", repoName),
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
		return nil
	}

	// Watch the workflow
	watchCmd := exec.Command("gh", "run", "watch", runID,
		"--repo", fmt.Sprintf("grovetools/%s", repoName))
	watchCmd.Stdout = os.Stdout
	watchCmd.Stderr = os.Stderr

	if err := watchCmd.Run(); err != nil {
		return fmt.Errorf("release build failed: %w", err)
	}

	c.logger.Info(" Release build completed successfully")
	return nil
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

	// Check if we're in grove-ecosystem root (only if ecosystem mode)
	if opts.Ecosystem {
		if _, err := os.Stat("grove.yml"); err != nil {
			// grove.yml doesn't exist - tell user to initialize first
			return fmt.Errorf("no grove.yml found in the current directory.\n\nTo create a new Grove ecosystem, run:\n  grove ws init\n\nOr to create a standalone repository without an ecosystem:\n  grove add-repo %s --alias %s (without --ecosystem flag)", opts.Name, opts.Alias)
		}
		// Check for alias conflicts in existing ecosystem
		if err := checkBinaryAliasConflict(opts.Alias); err != nil {
			return err
		}
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
		checkCmd := exec.Command("gh", "repo", "view", fmt.Sprintf("grovetools/%s", opts.Name))
		if err := checkCmd.Run(); err == nil {
			return fmt.Errorf("repository grovetools/%s already exists on GitHub", opts.Name)
		}

		// Check for GROVE_PAT early, but only for private repos
		if !opts.Public && os.Getenv("GROVE_PAT") == "" {
			return fmt.Errorf("GROVE_PAT environment variable not set. This is required for setting up GitHub Actions secrets for private repositories. Use --public for public repos.")
		}
	}

	return nil
}

func (c *Creator) generateSkeleton(opts CreateOptions, targetPath string) error {
	c.logger.Info("Generating repository skeleton...")

	// If no template specified, create a minimal repository
	if opts.TemplatePath == "" {
		return c.generateMinimalSkeleton(opts, targetPath)
	}

	// Determine module path based on mode
	var modulePath string
	if opts.Ecosystem {
		// In ecosystem mode, it's part of grove-meta
		modulePath = fmt.Sprintf("github.com/grovetools/grove/%s", opts.Name)
	} else {
		// In standalone mode, it's its own module
		modulePath = fmt.Sprintf("github.com/grovetools/%s", opts.Name)
	}

	// Sanitize alias for use as environment variable
	safeAliasUpper := strings.ToUpper(opts.Alias)
	safeAliasUpper = strings.ReplaceAll(safeAliasUpper, "-", "_")

	// Get latest versions
	data := templates.TemplateData{
		RepoName:         opts.Name,
		BinaryAlias:      opts.Alias,
		BinaryAliasUpper: safeAliasUpper,
		Description:      opts.Description,
		GoVersion:        "1.24",
		CoreVersion:      c.getLatestVersion("core"),
		TendVersion:      c.getLatestVersion("tend"),
		ModulePath:       modulePath,
		IsPublic:         opts.Public,
	}

	// Use external template
	return c.generateFromExternalTemplate(opts, data, targetPath)
}

// generateMinimalSkeleton creates a minimal repository with just README.md and grove.yml
func (c *Creator) generateMinimalSkeleton(opts CreateOptions, targetPath string) error {
	c.logger.Info("Creating minimal repository...")

	// Create directory
	if err := os.MkdirAll(targetPath, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Create README.md
	readmeContent := fmt.Sprintf("# %s\n\n%s\n", opts.Name, opts.Description)
	if err := os.WriteFile(filepath.Join(targetPath, "README.md"), []byte(readmeContent), 0644); err != nil {
		return fmt.Errorf("failed to create README.md: %w", err)
	}

	// Create grove.yml
	groveYmlContent := fmt.Sprintf(`name: %s
description: %s
`, opts.Name, opts.Description)
	if err := os.WriteFile(filepath.Join(targetPath, "grove.yml"), []byte(groveYmlContent), 0644); err != nil {
		return fmt.Errorf("failed to create grove.yml: %w", err)
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
	gitCommit := exec.Command("git", "commit", "-m", "feat: initial repository setup")
	gitCommit.Dir = targetPath
	if err := gitCommit.Run(); err != nil {
		return fmt.Errorf("failed to create initial commit: %w", err)
	}

	c.logger.Info(" Minimal repository created")
	return nil
}

func (c *Creator) generateFromExternalTemplate(opts CreateOptions, data templates.TemplateData, targetPath string) error {
	c.logger.Infof("Using external template from: %s", opts.TemplatePath)

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
			if cleanupErr := fetcher.Cleanup(); cleanupErr != nil {
				c.logger.Warnf("Failed to cleanup fetcher: %v", cleanupErr)
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
			if cleanupErr := fetcher.Cleanup(); cleanupErr != nil {
				c.logger.Warnf("Failed to cleanup fetcher: %v", cleanupErr)
			}
		}()

		var err error
		templateDir, err = fetcher.Fetch(opts.TemplatePath)
		if err != nil {
			return fmt.Errorf("failed to fetch template: %w", err)
		}
	}

	// Create renderer
	renderer := templates.NewRenderer()

	// Render template to target directory
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
	hooksCmd := delegation.Command("git-hooks", "install")
	hooksCmd.Dir = targetPath
	if output, err := hooksCmd.CombinedOutput(); err != nil {
		// Don't fail if hooks can't be installed, just warn
		c.logger.Warnf("Failed to install git hooks: %v\nOutput: %s", err, string(output))
		c.logger.Warn("You can install them manually later with: grove git-hooks install")
	}

	// Create .grove directory and workspace file
	c.logger.Info("Creating workspace marker file...")
	groveDir := filepath.Join(targetPath, ".grove")
	if err := os.MkdirAll(groveDir, 0755); err != nil {
		return fmt.Errorf("failed to create .grove directory: %w", err)
	}
	
	timestamp := time.Now().UTC().Format(time.RFC3339)
	workspaceContent := fmt.Sprintf(`branch: main
plan: %s-main-repo
created_at: %s
ecosystem: false
`, opts.Name, timestamp)
	
	workspaceFile := filepath.Join(groveDir, "workspace")
	if err := os.WriteFile(workspaceFile, []byte(workspaceContent), 0644); err != nil {
		return fmt.Errorf("failed to create .grove/workspace: %w", err)
	}

	return nil
}

func (c *Creator) verifyLocal(opts CreateOptions, targetPath string) error {
	// Determine project type based on project files
	projectType := c.detectProjectType(targetPath)
	c.logger.WithField("type", projectType).Info("Detected project type")

	// Handle language-specific dependency resolution
	switch projectType {
	case "go":
		// Prepare environment for Go commands
		goEnv := append(os.Environ(),
			"GOPRIVATE=github.com/grovetools/*",
			"GOPROXY=direct",
		)
		if !opts.Ecosystem {
			goEnv = append(goEnv, "GOWORK=off")
		}

		// Run go mod tidy first to resolve dependencies
		c.logger.Info("Resolving dependencies...")
		tidyCmd := exec.Command("go", "mod", "tidy")
		tidyCmd.Dir = targetPath
		tidyCmd.Env = goEnv
		if output, err := tidyCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go mod tidy failed: %w\nOutput: %s", err, string(output))
		}

		// Run go mod download
		c.logger.Info("Downloading dependencies...")
		modCmd := exec.Command("go", "mod", "download")
		modCmd.Dir = targetPath
		modCmd.Env = goEnv
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
		setupCmd.Dir = targetPath
		if output, err := setupCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("make setup failed: %w\nOutput: %s", err, string(output))
		}

	default:
		c.logger.Info("No language-specific dependency resolution needed")
	}

	// Run make-based verification (works for all project types)
	// Check if Makefile exists first
	makefilePath := filepath.Join(targetPath, "Makefile")
	if _, err := os.Stat(makefilePath); err == nil {
		// Build the project
		c.logger.Info("Building project...")
		buildCmd := exec.Command("make", "build")
		buildCmd.Dir = targetPath
		if projectType == "go" && !opts.Ecosystem {
			buildCmd.Env = append(os.Environ(), "GOWORK=off")
		}
		if output, err := buildCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("make build failed: %w\nOutput: %s", err, string(output))
		}

		// Run tests if available
		c.logger.Info("Running tests...")
		testCmd := exec.Command("make", "test")
		testCmd.Dir = targetPath
		if projectType == "go" && !opts.Ecosystem {
			testCmd.Env = append(os.Environ(), "GOWORK=off")
		}
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
		e2eCmd.Dir = targetPath
		if projectType == "go" && !opts.Ecosystem {
			e2eCmd.Env = append(os.Environ(), "GOWORK=off")
		}
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

	c.logger.Info(" Local verification passed")
	return nil
}

// detectProjectType determines the project type based on project files
func (c *Creator) detectProjectType(targetPath string) string {
	// Check for go.mod
	if _, err := os.Stat(filepath.Join(targetPath, "go.mod")); err == nil {
		return "go"
	}

	// Check for pyproject.toml (Maturin/Python projects)
	if _, err := os.Stat(filepath.Join(targetPath, "pyproject.toml")); err == nil {
		return "maturin"
	}

	// Check for package.json (Node projects)
	if _, err := os.Stat(filepath.Join(targetPath, "package.json")); err == nil {
		return "node"
	}

	// Default to unknown
	return "unknown"
}

func (c *Creator) createGitHubRepo(opts CreateOptions, targetPath string) error {
	c.logger.Info("Creating GitHub repository...")

	// Create the repository without pushing
	createArgs := []string{"repo", "create", fmt.Sprintf("grovetools/%s", opts.Name)}
	if opts.Public {
		createArgs = append(createArgs, "--public")
	} else {
		createArgs = append(createArgs, "--private")
	}
	createArgs = append(createArgs, "--source=.", "--remote=origin")
	createCmd := exec.Command("gh", createArgs...)
	createCmd.Dir = targetPath

	if output, err := createCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create GitHub repository: %w\nOutput: %s", err, string(output))
	}

	// Ensure the remote URL is clean (no embedded tokens)
	c.logger.Info("Ensuring clean remote URL...")
	setUrlCmd := exec.Command("git", "remote", "set-url", "origin",
		fmt.Sprintf("https://github.com/grovetools/%s.git", opts.Name))
	setUrlCmd.Dir = targetPath
	if err := setUrlCmd.Run(); err != nil {
		c.logger.Warnf("Failed to set clean remote URL: %v", err)
	}

	c.logger.Info(" GitHub repository created")
	return nil
}

func (c *Creator) pushToGitHub(opts CreateOptions, targetPath string) error {
	c.logger.Info("Pushing to GitHub...")
	pushCmd := exec.Command("git", "push", "-u", "origin", "main")
	pushCmd.Dir = targetPath
	if output, err := pushCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to push to GitHub: %w\nOutput: %s", err, string(output))
	}

	c.logger.Info(" Code pushed to GitHub")
	return nil
}

func (c *Creator) setupSecrets(opts CreateOptions, targetPath string) error {
	if opts.Public {
		c.logger.Info("Skipping secret setup for public repository.")
		return nil
	}
	c.logger.Info("Setting up repository secrets...")

	// We already validated GROVE_PAT exists in the validate phase
	grovePAT := os.Getenv("GROVE_PAT")

	// Set the secret
	secretCmd := exec.Command("gh", "secret", "set", "GROVE_PAT", "--body", grovePAT)
	secretCmd.Dir = targetPath

	if err := secretCmd.Run(); err != nil {
		return fmt.Errorf("failed to set GROVE_PAT secret: %w", err)
	}

	c.logger.Info(" Repository secrets configured")
	return nil
}

func (c *Creator) createInitialRelease(opts CreateOptions, targetPath string) error {
	c.logger.Info("Creating initial release v0.0.1...")

	// Create tag
	tagCmd := exec.Command("git", "tag", "v0.0.1", "-m", "Initial release")
	tagCmd.Dir = targetPath
	if err := tagCmd.Run(); err != nil {
		return fmt.Errorf("failed to create tag: %w", err)
	}

	if !opts.SkipGitHub {
		// Push tag
		pushCmd := exec.Command("git", "push", "origin", "v0.0.1")
		pushCmd.Dir = targetPath
		if err := pushCmd.Run(); err != nil {
			return fmt.Errorf("failed to push tag: %w", err)
		}
	}

	c.logger.Info(" Initial release created")
	return nil
}

func (c *Creator) waitForCI(opts CreateOptions) error {
	c.logger.Info("Waiting for release build to complete...")

	// Wait a moment for the workflow to start
	time.Sleep(2 * time.Second)

	// Get the workflow run ID
	cmd := exec.Command("gh", "run", "list",
		"--repo", fmt.Sprintf("grovetools/%s", opts.Name),
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
		return nil // Don't fail, just continue
	}

	// Watch the workflow
	watchCmd := exec.Command("gh", "run", "watch", runID,
		"--repo", fmt.Sprintf("grovetools/%s", opts.Name))
	watchCmd.Stdout = os.Stdout
	watchCmd.Stderr = os.Stderr

	if err := watchCmd.Run(); err != nil {
		return fmt.Errorf("release build failed: %w", err)
	}

	c.logger.Info(" Release build completed successfully")
	return nil
}

func (c *Creator) addToEcosystem(opts CreateOptions) error {
	c.logger.Info("Adding repository to grove-ecosystem...")

	// Always add as submodule in ecosystem mode
	// Check if submodule already exists
	checkCmd := exec.Command("git", "submodule", "status", opts.Name)
	if err := checkCmd.Run(); err == nil {
		c.logger.Warnf("Submodule %s already exists, updating it...", opts.Name)

		// Update the submodule URL in case it changed (only if GitHub is enabled)
		if !opts.SkipGitHub {
			updateUrlCmd := exec.Command("git", "submodule", "set-url", opts.Name,
				fmt.Sprintf("git@github.com:grovetools/%s.git", opts.Name))
			if err := updateUrlCmd.Run(); err != nil {
				c.logger.Warnf("Failed to update submodule URL: %v", err)
			}
		}
	} else {
		// Add as submodule only if it doesn't exist
		// Use local path if SkipGitHub is true, otherwise use GitHub URL
		var submoduleUrl string
		if opts.SkipGitHub {
			submoduleUrl = "./" + opts.Name
		} else {
			submoduleUrl = fmt.Sprintf("git@github.com:grovetools/%s.git", opts.Name)
		}
		
		submoduleCmd := exec.Command("git", "submodule", "add", submoduleUrl, opts.Name)

		if output, err := submoduleCmd.CombinedOutput(); err != nil {
			// Check if it's the "already exists" error
			if strings.Contains(string(output), "already exists in the index") {
				c.logger.Warn("Submodule already in index, continuing...")
			} else {
				return fmt.Errorf("failed to add submodule: %w\nOutput: %s", err, string(output))
			}
		}
	}

	// Update Makefile
	if err := c.updateMakefile(opts); err != nil {
		return fmt.Errorf("failed to update Makefile: %w", err)
	}

	// go.work was already updated earlier for local verification, so skip here

	c.logger.Info(" Repository added to ecosystem")
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
	// Check if it's the default "go" template or a path containing grove-project-tmpl-go
	return templatePath == "go" || strings.HasSuffix(templatePath, "grove-project-tmpl-go")
}

func (c *Creator) showSummary(opts CreateOptions, targetPath string) error {
	fmt.Println("\n All operations completed successfully!")
	fmt.Println("\nSUMMARY")
	fmt.Println("-------")
	fmt.Printf("- Local repository created at: %s\n", targetPath)

	if !opts.SkipGitHub {
		fmt.Printf("- GitHub repository created:  https://github.com/grovetools/%s\n", opts.Name)
		fmt.Println("- Initial release v0.0.1 pushed and CI is running.")
	}

	if opts.Ecosystem {
		fmt.Println("- Submodule added to grove-ecosystem.")
		fmt.Println("- Root Makefile and go.work have been updated.")
	}

	fmt.Println("\nNEXT STEPS")
	fmt.Println("----------")

	if opts.Ecosystem {
		fmt.Println("1. Stage and commit the ecosystem changes:")
		fmt.Println("   > git add Makefile go.work .gitmodules " + opts.Name)
		fmt.Printf("   > git commit -m \"feat: add %s to the ecosystem\"\n", opts.Name)
		fmt.Println("")
		fmt.Printf("2. Start developing your new tool:\n")
		fmt.Printf("   > cd %s\n", opts.Name)
		fmt.Printf("   > grove install %s\n", opts.Alias)
	} else {
		// Standalone mode
		fmt.Printf("1. Start developing your new tool:\n")
		fmt.Printf("   > cd %s\n", opts.Name)
		fmt.Printf("   > grove install %s\n", opts.Alias)
	}

	return nil
}

func (c *Creator) rollback(state *creationState, opts CreateOptions, targetPath string) {
	c.logger.Warn("Error occurred, rolling back changes...")

	if state.githubRepoCreated && !opts.SkipGitHub {
		c.logger.Warnf("GitHub repository was created but setup failed.")
		c.logger.Warnf("Please delete it manually: https://github.com/grovetools/%s", opts.Name)
		c.logger.Warnf("Run: gh repo delete grovetools/%s", opts.Name)
	}

	if state.localRepoCreated {
		c.logger.Info("Removing local directory...")
		if err := os.RemoveAll(targetPath); err != nil {
			c.logger.Errorf("Failed to remove directory: %v", err)
		}

		// Restore go.work if we have a snapshot
		if opts.Ecosystem && state.originalGoWorkContent != nil {
			c.logger.Info("Restoring go.work from snapshot...")
			rootDir, err := workspace.FindEcosystemRoot("")
			if err == nil {
				goWorkPath := filepath.Join(rootDir, "go.work")
				if err := os.WriteFile(goWorkPath, state.originalGoWorkContent, 0644); err != nil {
					c.logger.Errorf("Failed to restore go.work: %v", err)
				}
			}
		} else if opts.Ecosystem {
			// Fallback to the old removal method if no snapshot
			c.logger.Info("Removing from go.work...")
			eco := &Ecosystem{logger: c.logger}
			if err := eco.removeFromGoWork(opts.Name); err != nil {
				c.logger.Errorf("Failed to remove from go.work: %v", err)
			}
		}

		// Try to remove from git index if it was added as submodule (only in ecosystem mode)
		if opts.Ecosystem {
			c.logger.Info("Cleaning up git submodule...")
			rmCmd := exec.Command("git", "rm", "--cached", "-f", opts.Name)
			if err := rmCmd.Run(); err != nil {
				// This is okay if it wasn't added
				c.logger.Debugf("Submodule cleanup: %v", err)
			}
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
		c.logger.Infof("  gh repo create grovetools/%s --private", opts.Name)
		c.logger.Infof("  git remote add origin git@github.com:grovetools/%s.git", opts.Name)
		c.logger.Info("  git push -u origin main")
		c.logger.Info("  gh secret set GROVE_PAT --body <GROVE_PAT>")
		c.logger.Info("  git tag v0.0.1")
		c.logger.Info("  git push origin v0.0.1")
		c.logger.Info("  gh run watch")
	}

	if opts.Ecosystem {
		c.logger.Info("\nEcosystem integration:")
		c.logger.Infof("  git submodule add git@github.com:grovetools/%s.git", opts.Name)
		c.logger.Infof("  Update Makefile: Add %s to PACKAGES", opts.Name)
		c.logger.Infof("  Update Makefile: Add %s to BINARIES", opts.Alias)
		c.logger.Infof("  Update go.work: Add use (./%s)", opts.Name)
	} else {
		c.logger.Info("\nEcosystem integration: SKIPPED (use --ecosystem flag to enable)")
	}

	return nil
}

func (c *Creator) getLatestVersion(repo string) string {
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/grovetools/%s/releases/latest", repo),
		"--jq", ".tag_name")
	output, err := cmd.Output()
	if err != nil {
		// Fallback to a reasonable default
		switch repo {
		case "core":
			return "v0.5.0"
		case "tend":
			return "v0.5.0"
		default:
			return "v0.0.1"
		}
	}
	return strings.TrimSpace(string(output))
}

