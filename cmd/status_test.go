package cmd

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/grovetools/core/pkg/models"
)

// captureStatusStdout runs fn while capturing everything it writes to
// os.Stdout (the status render functions print via fmt.Println).
func captureStatusStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return string(out)
}

// Zero workspaces must not suppress the Satellites/Jobs sections: satellite
// health and federated jobs come from the daemon, not from workspace
// discovery.
func TestRunStatusTableZeroWorkspacesRendersSatellitesAndJobs(t *testing.T) {
	sats := map[string]*models.SatelliteStatus{
		"gpu-1": {
			Name:  "gpu-1",
			State: "connected",
			Addr:  "203.0.113.7:22",
			Since: time.Now().Add(-5 * time.Minute),
		},
	}
	jobs := []*models.JobInfo{
		{
			ID:          "job-1",
			JobFile:     "01-impl.md",
			PlanName:    "satellite-poc",
			Status:      "running",
			Origin:      "gpu-1",
			SubmittedAt: time.Now().Add(-2 * time.Minute),
		},
	}

	out := captureStatusStdout(t, func() {
		if err := runStatusTable(nil, nil, false, jobs, sats); err != nil {
			t.Errorf("runStatusTable: %v", err)
		}
	})

	if !strings.Contains(out, "No workspaces found.") {
		t.Errorf("expected zero-workspaces message, got:\n%s", out)
	}
	if !strings.Contains(out, "Satellites:") {
		t.Errorf("expected Satellites section to render with zero workspaces, got:\n%s", out)
	}
	if !strings.Contains(out, "gpu-1") {
		t.Errorf("expected satellite row for gpu-1, got:\n%s", out)
	}
	if !strings.Contains(out, "Jobs:") {
		t.Errorf("expected Jobs section to render with zero workspaces, got:\n%s", out)
	}
	if !strings.Contains(out, "01-impl.md") {
		t.Errorf("expected job row for 01-impl.md, got:\n%s", out)
	}
}

// With nothing federated configured, the zero-workspaces output stays a plain
// message with no Satellites/Jobs sections (preserves pre-C17 behavior).
func TestRunStatusTableZeroWorkspacesNoSatellites(t *testing.T) {
	out := captureStatusStdout(t, func() {
		if err := runStatusTable(nil, nil, false, nil, nil); err != nil {
			t.Errorf("runStatusTable: %v", err)
		}
	})

	if !strings.Contains(out, "No workspaces found.") {
		t.Errorf("expected zero-workspaces message, got:\n%s", out)
	}
	if strings.Contains(out, "Satellites:") {
		t.Errorf("did not expect a Satellites section, got:\n%s", out)
	}
	if strings.Contains(out, "Jobs:") {
		t.Errorf("did not expect a Jobs section, got:\n%s", out)
	}
}
