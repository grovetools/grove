package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
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

// WaitForReleaseWorkflow waits for a GitHub Actions workflow triggered by a tag push to complete successfully
func WaitForReleaseWorkflow(ctx context.Context, repoPath, versionTag string) error {
	// Get repository slug (owner/repo)
	slug, err := getRepoSlug(repoPath)
	if err != nil {
		return fmt.Errorf("failed to get repository slug: %w", err)
	}

	// Create a context with 10-minute timeout for the entire operation
	watchCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	// First, we need to find the workflow run triggered by our tag push
	// GitHub Actions may have a delay, so we'll poll for up to 3 minutes
	var runID string
	findTimeout := time.After(3 * time.Minute)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	attemptCount := 0

	for {
		select {
		case <-watchCtx.Done():
			return watchCtx.Err()
		case <-findTimeout:
			return fmt.Errorf("timeout after 3 minutes waiting for workflow run to appear for tag %s (tried %d times)", versionTag, attemptCount)
		case <-ticker.C:
			attemptCount++
			
			// Try to find the workflow run
			// First try with the tag as branch
			cmd := exec.CommandContext(watchCtx, "gh", "run", "list", 
				"--repo", slug,
				"--event", "push",
				"--branch", versionTag,
				"--limit", "1",
				"--json", "databaseId")
			
			output, err := cmd.Output()
			if err != nil {
				// Not found yet, continue polling
				if attemptCount%4 == 0 { // Log every 20 seconds
					fmt.Printf("Still waiting for workflow run to appear for %s (attempt %d)...\n", versionTag, attemptCount)
				}
				continue
			}

			var runs []struct {
				DatabaseID int64 `json:"databaseId"`
			}
			if err := json.Unmarshal(output, &runs); err != nil {
				if attemptCount%4 == 0 { // Log every 20 seconds
					fmt.Printf("Failed to parse workflow run data (attempt %d): %v\n", attemptCount, err)
				}
				continue
			}

			if len(runs) > 0 {
				runID = fmt.Sprintf("%d", runs[0].DatabaseID)
				fmt.Printf("Found workflow run %s for %s\n", runID, versionTag)
				break
			}
			
			// If not found with branch filter, try without it (broader search)
			if attemptCount > 2 { // After 10 seconds, try broader search
				cmd = exec.CommandContext(watchCtx, "gh", "run", "list", 
					"--repo", slug,
					"--limit", "10",
					"--json", "databaseId,headBranch,event")
				
				output, err = cmd.Output()
				if err == nil {
					var allRuns []struct {
						DatabaseID int64  `json:"databaseId"`
						HeadBranch string `json:"headBranch"`
						Event      string `json:"event"`
					}
					if err := json.Unmarshal(output, &allRuns); err == nil {
						// Look for our tag in the results
						for _, run := range allRuns {
							if run.HeadBranch == versionTag || run.HeadBranch == "refs/tags/"+versionTag {
								runID = fmt.Sprintf("%d", run.DatabaseID)
								fmt.Printf("Found workflow run %s for %s (broader search)\n", runID, versionTag)
								break
							}
						}
						if runID != "" {
							break
						}
					}
				}
			}
			
			if attemptCount%4 == 0 { // Log every 20 seconds
				fmt.Printf("No workflow runs found yet for tag %s (attempt %d)...\n", versionTag, attemptCount)
			}
		}
	}

	// Now watch the workflow run until it completes
	// The --exit-status flag makes gh exit with non-zero status if the workflow fails
	cmd := exec.CommandContext(watchCtx, "gh", "run", "watch", runID,
		"--repo", slug,
		"--exit-status")

	// Execute the command which will stream the CI logs and wait for completion
	if err := cmd.Run(); err != nil {
		// Check if it's a context timeout
		if watchCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("timeout after 10 minutes waiting for CI workflow to complete for %s@%s", slug, versionTag)
		}
		// Check if it's a workflow failure vs other error
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("CI workflow failed for %s@%s (exit code: %d)", slug, versionTag, exitErr.ExitCode())
		}
		return fmt.Errorf("failed to watch workflow run: %w", err)
	}

	return nil
}

