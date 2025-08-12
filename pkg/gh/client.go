package gh

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

type GHClient struct{}

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

func GetMainCIStatus(repoPath string) (string, error) {
	slug, err := getRepoSlug(repoPath)
	if err != nil {
		return "? Unknown", err
	}

	// Get current HEAD commit
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD")
	headOutput, err := cmd.Output()
	if err != nil {
		return "? Unknown", fmt.Errorf("failed to get HEAD commit: %w", err)
	}
	headSha := strings.TrimSpace(string(headOutput))

	// Get workflow runs for the specific commit
	cmd = exec.Command("gh", "run", "list", "--branch", "main", "--commit", headSha, "--limit", "1", "--json", "status,conclusion", "--repo", slug)
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

