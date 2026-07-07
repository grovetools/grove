package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/grovetools/core/config"
	"github.com/sirupsen/logrus"
)

// Website finalize flags. The website stage is the last stage of `grove release
// apply`: after the parent superrepo gitlink bump, it (optionally) rebuilds the
// docs site from the freshly-published docs + changelogs and — only behind an
// explicit deploy knob — publishes it to Cloudflare Pages.
//
// Enablement precedence: any of the three flags below force the stage on;
// otherwise the ecosystem grove.toml `[release] website = true` default decides.
// A build failure never fails the release (deliverable: "Website failure must
// NOT fail the release").
var (
	// --website: run the website build stage (no deploy unless --website-deploy).
	releaseWebsite bool
	// --website-dry-run: run the build stage but never deploy (build-only, explicit).
	releaseWebsiteDryRun bool
	// --website-deploy: the ADDITIONAL explicit knob that permits `wrangler pages
	// deploy`. Without it (or with any dry-run) the stage stops after `npm run
	// build`. Deploy is intentionally gated behind its own flag so a normal
	// apply — even with --website — never touches Cloudflare.
	releaseWebsiteDeploy bool
)

// releaseWebsiteConfig is the ecosystem-level default for the website finalize
// stage, read from grove.toml `[release]`.
type releaseWebsiteConfig struct {
	// Website, when true, makes `grove release apply` run the website build
	// stage by default (equivalent to always passing --website). Deploy still
	// requires the explicit --website-deploy knob.
	Website bool `yaml:"website"`
}

// websiteStageEnabled reports whether the website finalize stage should run for
// this apply, and whether it is allowed to deploy. Any website flag forces the
// stage on; otherwise the grove.toml `[release] website` default decides.
// Deploy is permitted only when --website-deploy is set AND no dry-run is in
// effect (neither --website-dry-run nor the global --dry-run).
func websiteStageEnabled(rootDir string) (enabled, deploy bool) {
	enabled = releaseWebsite || releaseWebsiteDryRun || releaseWebsiteDeploy
	if !enabled {
		// Fall back to the ecosystem grove.toml [release] default.
		if cfg, err := config.LoadFrom(rootDir); err == nil {
			var rc releaseWebsiteConfig
			if cfg.UnmarshalExtension("release", &rc) == nil && rc.Website {
				enabled = true
			}
		}
	}
	deploy = releaseWebsiteDeploy && !releaseWebsiteDryRun && !releaseDryRun
	return enabled, deploy
}

// runWebsiteFinalize is the ecosystem website finalize stage. It runs in the
// grove-website workspace after the parent superrepo finalize. It is
// best-effort: every error is returned to the caller, which reports it and
// CONTINUES — a website failure must never roll back or fail the release.
//
// Steps:
//  1. Remove docgen-output-prod (MANDATORY — prepare-content silently skips
//     aggregation when manifest.json already exists, so a stale manifest would
//     freeze the site content; `npm run build` also cleans it, but we remove it
//     up front so the invariant holds even if the build script changes).
//  2. `npm run build` (clean:dist → prepare:prod → astro build). GROVE_ECOSYSTEM_PATH
//     is pinned to rootDir so `docgen aggregate` scans this ecosystem/worktree.
//  3. Deploy — ONLY when allowDeploy is true (i.e. --website-deploy and no
//     dry-run). Otherwise the stage stops here with an explicit build-only note.
func runWebsiteFinalize(ctx context.Context, rootDir string, logger *logrus.Logger) error {
	enabled, allowDeploy := websiteStageEnabled(rootDir)
	if !enabled {
		return nil // stage not requested and not defaulted on — silent no-op
	}

	displaySection("Website Finalize")

	websiteDir := filepath.Join(rootDir, "grove-website")
	if info, err := os.Stat(websiteDir); err != nil || !info.IsDir() {
		return fmt.Errorf("grove-website workspace not found at %s (skipping website build)", websiteDir)
	}

	if releaseDryRun {
		displayInfo(fmt.Sprintf("[DRY RUN] would remove %s, run 'npm run build' in %s%s",
			filepath.Join(websiteDir, "docgen-output-prod"), websiteDir,
			map[bool]string{true: ", then 'wrangler pages deploy'", false: " (build only)"}[allowDeploy]))
		return nil
	}

	// (1) MANDATORY: remove docgen-output-prod so prepare-content re-aggregates.
	prodOut := filepath.Join(websiteDir, "docgen-output-prod")
	if err := os.RemoveAll(prodOut); err != nil {
		return fmt.Errorf("failed to remove %s: %w", prodOut, err)
	}
	displayInfo(fmt.Sprintf("Cleaned %s (forces docgen re-aggregation)", prodOut))

	// (2) Build the site. Pin GROVE_ECOSYSTEM_PATH to this ecosystem root so the
	// aggregate step scans the just-published repos in this worktree/checkout.
	displayInfo("Building docs site (npm run build)...")
	buildCmd := exec.CommandContext(ctx, "npm", "run", "build")
	buildCmd.Dir = websiteDir
	buildCmd.Env = append(os.Environ(), "GROVE_ECOSYSTEM_PATH="+rootDir)
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("npm run build failed: %w", err)
	}
	displaySuccess("Website build complete (dist/ regenerated)")

	// (3) Deploy — strictly opt-in behind --website-deploy.
	if !allowDeploy {
		displayInfo("Website deploy skipped (build only — pass --website-deploy to publish)")
		return nil
	}

	displayWarning("Deploying website to Cloudflare Pages (wrangler pages deploy)...")
	deployCmd := exec.CommandContext(ctx, "npx", "wrangler", "pages", "deploy", "dist", "--project-name=grove-website")
	deployCmd.Dir = websiteDir
	deployCmd.Env = os.Environ()
	deployCmd.Stdout = os.Stdout
	deployCmd.Stderr = os.Stderr
	if err := deployCmd.Run(); err != nil {
		return fmt.Errorf("wrangler pages deploy failed: %w", err)
	}
	displaySuccess("Website deployed to Cloudflare Pages")
	return nil
}
