package cmd

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/grovetools/grove/pkg/release"
)

// newTestReleaseModel builds a minimal, dependency-light release TUI model
// around a single repo "foo" for Update/View unit tests. The viewport is sized
// large so View() output is never clipped.
func newTestReleaseModel(t *testing.T, repo *release.RepoReleasePlan) releaseTuiModel {
	t.Helper()
	plan := &release.ReleasePlan{
		RootDir:       t.TempDir(),
		Type:          "full",
		Repos:         map[string]*release.RepoReleasePlan{"foo": repo},
		ReleaseLevels: [][]string{{"foo"}},
	}
	return releaseTuiModel{
		plan:          plan,
		keys:          releaseKeys,
		currentView:   viewTable,
		repoNames:     []string{"foo"},
		selectedIndex: 0,
		viewport:      viewport.New(200, 200),
	}
}

func keyMsg(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// TestApproveBlockedOnGenError asserts approval is refused while a repo carries
// a docs/changelog generation error, and the user is told why.
func TestApproveBlockedOnGenError(t *testing.T) {
	repo := &release.RepoReleasePlan{
		CurrentVersion: "v1.0.0",
		NextVersion:    "v1.1.0",
		Status:         "Pending Review",
		GenError:       "docgen: freeze-verify failed",
	}
	m := newTestReleaseModel(t, repo)

	next, _ := m.Update(keyMsg('a'))
	nm := next.(releaseTuiModel)

	if repo.Status == "Approved" {
		t.Fatalf("expected approval to be blocked, but status is Approved")
	}
	if !strings.Contains(nm.genProgress, "Cannot approve") {
		t.Fatalf("expected a 'Cannot approve' message, got %q", nm.genProgress)
	}
}

// TestApproveBlockedOnChangelogGenError covers the changelog-only failure case.
func TestApproveBlockedOnChangelogGenError(t *testing.T) {
	repo := &release.RepoReleasePlan{
		CurrentVersion:    "v1.0.0",
		NextVersion:       "v1.1.0",
		Status:            "Pending Review",
		ChangelogGenError: "changelog: model overloaded",
	}
	m := newTestReleaseModel(t, repo)

	next, _ := m.Update(keyMsg('a'))
	nm := next.(releaseTuiModel)

	if repo.Status == "Approved" {
		t.Fatalf("expected approval to be blocked on changelog error")
	}
	if !strings.Contains(nm.genProgress, "Cannot approve") {
		t.Fatalf("expected a 'Cannot approve' message, got %q", nm.genProgress)
	}
}

// TestApproveSucceedsWhenClean verifies a clean repo can be approved (docs +
// changelog together). Redirects StateDir via GROVE_HOME so SavePlan does not
// touch the real state directory.
func TestApproveSucceedsWhenClean(t *testing.T) {
	t.Setenv("GROVE_HOME", t.TempDir())
	repo := &release.RepoReleasePlan{
		CurrentVersion: "v1.0.0",
		NextVersion:    "v1.1.0",
		Status:         "Pending Review",
	}
	m := newTestReleaseModel(t, repo)

	next, _ := m.Update(keyMsg('a'))
	_ = next.(releaseTuiModel)

	if repo.Status != "Approved" {
		t.Fatalf("expected approval to succeed, got status %q", repo.Status)
	}
}

// TestApprovalBlocker exercises the pure approval gate.
func TestApprovalBlocker(t *testing.T) {
	cases := []struct {
		name    string
		repo    *release.RepoReleasePlan
		blocked bool
	}{
		{"clean", &release.RepoReleasePlan{}, false},
		{"gen error", &release.RepoReleasePlan{GenError: "boom"}, true},
		{"changelog error", &release.RepoReleasePlan{ChangelogGenError: "boom"}, true},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := approvalBlocker(c.repo) != ""
			if got != c.blocked {
				t.Fatalf("approvalBlocker blocked=%v, want %v", got, c.blocked)
			}
		})
	}
}

// TestRenderRepoGenDetail asserts the usage line surfaces cache tokens, cost,
// and the greyed check-stage slot.
func TestRenderRepoGenDetail(t *testing.T) {
	repo := &release.RepoReleasePlan{
		CacheWriteTokens: 12345,
		CacheReadTokens:  67890,
		GenEstCostUSD:    0.0421,
	}
	out := renderRepoGenDetail(repo)
	for _, want := range []string{"12345", "67890", "$0.0421", "check: skipped"} {
		if !strings.Contains(out, want) {
			t.Fatalf("detail line missing %q; got: %s", want, out)
		}
	}
}

// TestDocsDiffCommand asserts the diff-view command is a git diff --no-index over
// the repo docs (left) and notebook docs (right), run from the repo dir.
func TestDocsDiffCommand(t *testing.T) {
	cmd := docsDiffCommand("/work/foo", "/work/foo/docs", "/nb/foo/docgen/docs")
	got := strings.Join(cmd.Args, " ")
	want := "git diff --no-index -- /work/foo/docs /nb/foo/docgen/docs"
	if !strings.Contains(got, want) {
		t.Fatalf("diff command args = %q, want to contain %q", got, want)
	}
	if cmd.Dir != "/work/foo" {
		t.Fatalf("diff command dir = %q, want /work/foo", cmd.Dir)
	}
}

// TestViewDocsRendersSections asserts the docs panel renders section rows with
// their notebook status and staged state from model state.
func TestViewDocsRendersSections(t *testing.T) {
	repo := &release.RepoReleasePlan{
		CurrentVersion: "v1.0.0",
		NextVersion:    "v1.1.0",
		DocsGenerated:  true,
	}
	m := newTestReleaseModel(t, repo)
	m.currentView = viewDocs
	m.docsRepo = "foo"
	m.docsSections = []docsSectionRow{
		{Name: "overview", Title: "Overview", Status: "production", Generated: true},
		{Name: "internals", Title: "Internals", Status: "draft", Generated: false},
	}

	out := m.viewDocs()
	for _, want := range []string{"Overview", "overview", "production", "staged", "Internals", "draft", "not staged"} {
		if !strings.Contains(out, want) {
			t.Fatalf("docs panel missing %q; got:\n%s", want, out)
		}
	}
}

// TestRegenHintAfterRepeatedRegens asserts the --cache-ttl 1h hint appears once
// the user has regenerated repeatedly in a session.
func TestRegenHintAfterRepeatedRegens(t *testing.T) {
	repo := &release.RepoReleasePlan{
		CurrentVersion: "v1.0.0",
		NextVersion:    "v1.1.0",
		Status:         "Pending Review",
	}
	m := newTestReleaseModel(t, repo)

	if strings.Contains(m.viewTable(), "cache-ttl 1h") {
		t.Fatal("hint should not show before repeated regens")
	}
	m.regenCount = 2
	if !strings.Contains(m.viewTable(), "cache-ttl 1h") {
		t.Fatal("expected --cache-ttl 1h hint after repeated regens")
	}
}

// TestDocsRegenMsgUpdatesProgress asserts the regen completion message updates
// usage totals in the progress line and clears the generating flag.
func TestDocsRegenMsgUpdatesProgress(t *testing.T) {
	repo := &release.RepoReleasePlan{CurrentVersion: "v1.0.0", NextVersion: "v1.1.0"}
	m := newTestReleaseModel(t, repo)
	m.generating = true

	next, _ := m.Update(docsRegenMsg{
		repoName:   "foo",
		section:    "overview",
		cacheWrite: 100,
		cacheRead:  200,
		estCostUSD: 0.01,
	})
	nm := next.(releaseTuiModel)

	if nm.generating {
		t.Fatal("expected generating flag cleared after regen msg")
	}
	if !strings.Contains(nm.genProgress, "section overview") || !strings.Contains(nm.genProgress, "foo") {
		t.Fatalf("expected regen success message, got %q", nm.genProgress)
	}
}

// TestViewDocsKeyEntersFromTable asserts pressing the ViewDocs key from the
// table switches into the docs panel for the selected repo.
func TestViewDocsKeyEntersFromTable(t *testing.T) {
	repo := &release.RepoReleasePlan{
		CurrentVersion: "v1.0.0",
		NextVersion:    "v1.1.0",
		DocsGenerated:  true,
		DocsSections:   []string{"overview"},
	}
	m := newTestReleaseModel(t, repo)

	next, _ := m.Update(keyMsg('V'))
	nm := next.(releaseTuiModel)

	if nm.currentView != viewDocs {
		t.Fatalf("expected viewDocs after 'V', got %q", nm.currentView)
	}
	if nm.docsRepo != "foo" {
		t.Fatalf("expected docsRepo=foo, got %q", nm.docsRepo)
	}
}
