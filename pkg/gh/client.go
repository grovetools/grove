package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	waitingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("75")).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).
			Bold(true)

	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")).
			Bold(true)

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("12"))
)

type GHClient struct{}

// Client represents a GitHub API client
type Client struct{}

// NewClient creates a new GitHub client
func NewClient() *Client {
	return &Client{}
}

var gitURLRegex = regexp.MustCompile(`(?:git@github\.com:|https://github\.com/)([^/]+)/([^/]+?)(?:\.git)?$`)

func getRepoSlug(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "config", "--get", "remote.origin.url")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get remote URL: %w", err)
	}

	url := strings.TrimSpace(string(output))
	matches := gitURLRegex.FindStringSubmatch(url)
	if len(matches) != 3 {
		return "", fmt.Errorf("unable to parse repository URL: %s", url)
	}

	return fmt.Sprintf("%s/%s", matches[1], matches[2]), nil
}

type WorkflowRun struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

func GetCurrentBranchCIStatus(repoPath string) (string, error) {
	slug, err := getRepoSlug(repoPath)
	if err != nil {
		return "? Unknown", err
	}

	// Get current branch
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	branchOutput, err := cmd.Output()
	if err != nil {
		return "? Unknown", fmt.Errorf("failed to get current branch: %w", err)
	}
	currentBranch := strings.TrimSpace(string(branchOutput))

	// Get current HEAD commit
	cmd = exec.Command("git", "-C", repoPath, "rev-parse", "HEAD")
	headOutput, err := cmd.Output()
	if err != nil {
		return "? Unknown", fmt.Errorf("failed to get HEAD commit: %w", err)
	}
	headSha := strings.TrimSpace(string(headOutput))

	// Get workflow runs for the specific commit on the current branch
	cmd = exec.Command("gh", "run", "list", "--branch", currentBranch, "--commit", headSha, "--limit", "1", "--json", "status,conclusion", "--repo", slug)
	output, err := cmd.Output()
	if err != nil {
		return "? Unknown", fmt.Errorf("failed to get CI status: %w", err)
	}

	var runs []WorkflowRun
	if err := json.Unmarshal(output, &runs); err != nil {
		return "? Unknown", fmt.Errorf("failed to parse CI status: %w", err)
	}

	if len(runs) == 0 {
		return "-", nil
	}

	run := runs[0]
	switch run.Status {
	case "completed":
		switch run.Conclusion {
		case "success":
			return "✅ Passed", nil
		case "failure":
			return "❌ Failed", nil
		case "cancelled":
			return "⚠️ Cancelled", nil
		case "skipped":
			return "⏭️ Skipped", nil
		default:
			return "? Unknown", nil
		}
	case "in_progress", "queued", "requested", "waiting", "pending":
		return "⌛ Pending", nil
	default:
		return "? Unknown", nil
	}
}

type PullRequest struct {
	Number            int                 `json:"number"`
	State             string              `json:"state"`
	IsDraft           bool                `json:"isDraft"`
	StatusCheckRollup []StatusCheckRollup `json:"statusCheckRollup"`
}

type StatusCheckRollup struct {
	State string `json:"state"`
}

func GetMyPRsStatus(repoPath string) (string, error) {
	slug, err := getRepoSlug(repoPath)
	if err != nil {
		return "? Unknown", err
	}

	cmd := exec.Command("gh", "pr", "list", "--author", "@me", "--json", "number,state,isDraft,statusCheckRollup", "--repo", slug)
	output, err := cmd.Output()
	if err != nil {
		return "? Unknown", fmt.Errorf("failed to get PR status: %w", err)
	}

	var prs []PullRequest
	if err := json.Unmarshal(output, &prs); err != nil {
		return "? Unknown", fmt.Errorf("failed to parse PR status: %w", err)
	}

	if len(prs) == 0 {
		return "-", nil
	}

	successCount := 0
	failureCount := 0
	pendingCount := 0
	draftCount := 0

	for _, pr := range prs {
		if pr.IsDraft {
			draftCount++
			continue
		}

		if len(pr.StatusCheckRollup) == 0 {
			pendingCount++
			continue
		}

		state := pr.StatusCheckRollup[0].State
		switch state {
		case "SUCCESS":
			successCount++
		case "FAILURE", "ERROR":
			failureCount++
		case "PENDING":
			pendingCount++
		default:
			pendingCount++
		}
	}

	total := len(prs)
	if total == 1 {
		pr := prs[0]
		if pr.IsDraft {
			return "1 ⌛ (Draft)", nil
		}
		if len(pr.StatusCheckRollup) == 0 {
			return "1 ⌛", nil
		}
		state := pr.StatusCheckRollup[0].State
		switch state {
		case "SUCCESS":
			return "1 ✅", nil
		case "FAILURE", "ERROR":
			return "1 ❌", nil
		default:
			return "1 ⌛", nil
		}
	}

	parts := []string{}
	if successCount > 0 {
		parts = append(parts, fmt.Sprintf("%d✅", successCount))
	}
	if failureCount > 0 {
		parts = append(parts, fmt.Sprintf("%d❌", failureCount))
	}
	if pendingCount > 0 {
		parts = append(parts, fmt.Sprintf("%d⌛", pendingCount))
	}
	if draftCount > 0 {
		parts = append(parts, fmt.Sprintf("%d📝", draftCount))
	}

	return fmt.Sprintf("%d (%s)", total, strings.Join(parts, ", ")), nil
}

// WaitForReleaseWorkflow waits for both CI and release workflows triggered by a tag push to complete successfully
func WaitForReleaseWorkflow(ctx context.Context, repoPath, versionTag string) error {
	// Get repository slug (owner/repo)
	slug, err := getRepoSlug(repoPath)
	if err != nil {
		return fmt.Errorf("failed to get repository slug: %w", err)
	}

	// Create a context with 60-minute timeout for the entire operation
	// (30 minutes to find workflows + 30 minutes to watch them complete)
	watchCtx, cancel := context.WithTimeout(ctx, 60*time.Minute)
	defer cancel()

	// Wait for both CI and release workflows to complete
	return waitForBothWorkflows(watchCtx, slug, versionTag)
}

// waitForBothWorkflows waits for both CI and release workflows to complete
// It first waits for any existing CI workflow to complete, then waits for the release workflow
func waitForBothWorkflows(ctx context.Context, slug, versionTag string) error {
	// Extract repo name from slug (owner/repo -> repo)
	repoName := slug
	if parts := strings.Split(slug, "/"); len(parts) == 2 {
		repoName = parts[1]
	}

	fmt.Printf("%s\n", waitingStyle.Render("⏳ Waiting for workflows to complete for "+repoName+"@"+versionTag+"..."))

	// Step 1: Check for and wait for CI workflow completion first
	// CI workflows run on push to main branch, which happens before tagging
	if err := waitForCIWorkflow(ctx, slug, repoName); err != nil {
		fmt.Printf("%s\n", warningStyle.Render("⚠️  CI workflow check failed: "+err.Error()))
		// Don't fail the entire process for CI issues, but log it
	}

	// Step 2: Wait for the release workflow triggered by the tag
	fmt.Printf("%s\n", infoStyle.Render("🚀 Now waiting for release workflow for "+repoName+"@"+versionTag+"..."))
	return waitForReleaseWorkflow(ctx, slug, repoName, versionTag)
}

// waitForCIWorkflow checks for recent CI workflows and waits for them to complete
func waitForCIWorkflow(ctx context.Context, slug, repoName string) error {
	// Look for recent CI workflows (last 5 runs)
	cmd := exec.CommandContext(ctx, "gh", "run", "list",
		"--repo", slug,
		"--workflow", "CI",
		"--limit", "5",
		"--json", "databaseId,status,conclusion,createdAt")

	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list CI workflows: %w", err)
	}

	var runs []struct {
		DatabaseID int64  `json:"databaseId"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		CreatedAt  string `json:"createdAt"`
	}

	if err := json.Unmarshal(output, &runs); err != nil {
		return fmt.Errorf("failed to parse CI workflow data: %w", err)
	}

	// Find the most recent CI workflow that's still running
	var runningCIID string
	for _, run := range runs {
		if run.Status == "in_progress" || run.Status == "queued" || run.Status == "requested" {
			runningCIID = fmt.Sprintf("%d", run.DatabaseID)
			fmt.Printf("%s\n", infoStyle.Render("🔄 Found running CI workflow for "+repoName+", waiting for completion..."))
			break
		}
	}

	// If no running CI workflow, check if the most recent one failed
	if runningCIID == "" && len(runs) > 0 {
		latest := runs[0]
		if latest.Status == "completed" {
			if latest.Conclusion == "success" {
				fmt.Printf("%s\n", successStyle.Render("✅ Latest CI workflow for "+repoName+" completed successfully"))
				return nil
			} else {
				return fmt.Errorf("latest CI workflow failed with conclusion: %s", latest.Conclusion)
			}
		}
		// If not completed and not running, it might be queued
		fmt.Printf("%s\n", infoStyle.Render("ℹ️  No actively running CI workflow found for "+repoName))
		return nil
	}

	if runningCIID != "" {
		// Wait for the CI workflow to complete
		cmd = exec.CommandContext(ctx, "gh", "run", "watch", runningCIID,
			"--repo", slug,
			"--exit-status")

		if err := cmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return fmt.Errorf("CI workflow %s failed (exit code: %d)", runningCIID, exitErr.ExitCode())
			}
			return fmt.Errorf("failed to watch CI workflow: %w", err)
		}
		fmt.Printf("%s\n", successStyle.Render("✅ CI workflow for "+repoName+" completed successfully"))
	}

	return nil
}

// waitForReleaseWorkflow waits specifically for the release workflow triggered by the tag
func waitForReleaseWorkflow(ctx context.Context, slug, repoName, versionTag string) error {
	// Poll for the release workflow run triggered by our tag push
	var runID string
	findTimeout := time.After(30 * time.Minute)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	attemptCount := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-findTimeout:
			return fmt.Errorf("timeout after 30 minutes waiting for release workflow to appear for tag %s (tried %d times)", versionTag, attemptCount)
		case <-ticker.C:
			attemptCount++

			// Look for release workflow runs triggered by our tag
			cmd := exec.CommandContext(ctx, "gh", "run", "list",
				"--repo", slug,
				"--workflow", "Release",
				"--limit", "10",
				"--json", "databaseId,headBranch,event,workflowName")

			output, err := cmd.Output()
			if err != nil {
				if attemptCount%4 == 0 { // Log every 20 seconds
					fmt.Printf("%s\n", waitingStyle.Render("🔍 Still searching for release workflow for "+repoName+"@"+versionTag+" (attempt "+fmt.Sprintf("%d", attemptCount)+")..."))
				}
				continue
			}

			var runs []struct {
				DatabaseID   int64  `json:"databaseId"`
				HeadBranch   string `json:"headBranch"`
				Event        string `json:"event"`
				WorkflowName string `json:"workflowName"`
			}

			if err := json.Unmarshal(output, &runs); err != nil {
				if attemptCount%4 == 0 {
					fmt.Printf("%s\n", warningStyle.Render("⚠️  Failed to parse release workflow data for "+repoName+"@"+versionTag+" (attempt "+fmt.Sprintf("%d", attemptCount)+"): "+err.Error()))
				}
				continue
			}

			// Look for our tag in the release workflow runs
			for _, run := range runs {
				if (run.HeadBranch == versionTag || run.HeadBranch == "refs/tags/"+versionTag) &&
					(run.WorkflowName == "Release" || run.WorkflowName == "release") {
					runID = fmt.Sprintf("%d", run.DatabaseID)
					fmt.Printf("%s\n", successStyle.Render("🎯 Found release workflow run "+runID+" for "+repoName+"@"+versionTag))
					goto found // Use goto to break out of both loops
				}
			}

			if runID != "" {
				goto found
			}

			if attemptCount%4 == 0 { // Log every 20 seconds
				fmt.Printf("%s\n", waitingStyle.Render("⏳ No release workflow found yet for "+repoName+"@"+versionTag+" (attempt "+fmt.Sprintf("%d", attemptCount)+")..."))
			}
		}
	}

found:
	// Now watch the release workflow run until it completes
	fmt.Printf("%s\n", infoStyle.Render("👀 Watching release workflow "+runID+" for "+repoName+"@"+versionTag+"..."))
	cmd := exec.CommandContext(ctx, "gh", "run", "watch", runID,
		"--repo", slug,
		"--exit-status")

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("timeout waiting for release workflow to complete for %s@%s", repoName, versionTag)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("release workflow failed for %s@%s (exit code: %d)", repoName, versionTag, exitErr.ExitCode())
		}
		return fmt.Errorf("failed to watch release workflow: %w", err)
	}

	fmt.Printf("%s\n", successStyle.Render("🎉 Release workflow completed successfully for "+repoName+"@"+versionTag))
	return nil
}

// WaitForCIWorkflow waits for CI workflow to complete for the latest commit on main branch
func WaitForCIWorkflow(ctx context.Context, repoPath string) error {
	slug, err := getRepoSlug(repoPath)
	if err != nil {
		return err
	}
	repoName := slug
	if parts := strings.Split(slug, "/"); len(parts) == 2 {
		repoName = parts[1]
	}
	
	// Get latest commit on main
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "origin/main")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get latest commit on main for %s: %w", repoName, err)
	}
	commitSHA := strings.TrimSpace(string(output))
	
	// Poll for the CI run for this commit to appear
	var runID string
	findTimeout := time.After(5 * time.Minute)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-findTimeout:
			return fmt.Errorf("timeout waiting for CI workflow for commit %s to appear", commitSHA)
		case <-ticker.C:
			cmd = exec.CommandContext(ctx, "gh", "run", "list",
				"--repo", slug,
				"--workflow", "CI",
				"--commit", commitSHA,
				"--limit", "1",
				"--json", "databaseId")
			
			output, err := cmd.Output()
			if err != nil {
				continue // Try again
			}

			var runs []struct {
				DatabaseID int64 `json:"databaseId"`
			}
			if err := json.Unmarshal(output, &runs); err == nil && len(runs) > 0 {
				runID = fmt.Sprintf("%d", runs[0].DatabaseID)
				goto foundCI
			}
		}
	}

foundCI:
	// Watch the workflow run
	fmt.Printf("%s\n", infoStyle.Render("👀 Watching CI workflow "+runID+" for "+repoName+"..."))
	cmd = exec.CommandContext(ctx, "gh", "run", "watch", runID,
		"--repo", slug,
		"--exit-status")

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("CI workflow %s failed (exit code: %d)", runID, exitErr.ExitCode())
		}
		return fmt.Errorf("failed to watch CI workflow: %w", err)
	}
	
	fmt.Printf("%s\n", successStyle.Render("✅ CI workflow for "+repoName+" completed successfully"))
	return nil
}
