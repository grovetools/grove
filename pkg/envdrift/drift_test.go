package envdrift

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	coreenv "github.com/grovetools/core/pkg/env"
)

// TestHelperDrift is invoked by the drift unit tests to impersonate the
// `terraform` binary. Like core/pkg/env's TestHelperProcess trick, it only
// does work when GO_WANT_DRIFT_HELPER=1 is set in the environment, so running
// it as part of the normal test suite is a no-op.
func TestHelperDrift(t *testing.T) {
	if os.Getenv("GO_WANT_DRIFT_HELPER") != "1" {
		return
	}
	defer os.Exit(0)

	mode := os.Getenv("DRIFT_HELPER_MODE")
	switch mode {
	case "plan_no_drift":
		fmt.Fprintln(os.Stdout, `{"@level":"info","type":"version","terraform":"1.5.0"}`)
		fmt.Fprintln(os.Stdout, `{"type":"planned_change","change":{"resource":{"addr":"google_storage_bucket.state"},"action":"no-op"}}`)
		os.Exit(0)
	case "plan_drift":
		fmt.Fprintln(os.Stdout, `{"type":"planned_change","change":{"resource":{"addr":"google_compute_instance.api"},"action":"create"}}`)
		fmt.Fprintln(os.Stdout, `{"type":"planned_change","change":{"resource":{"addr":"google_cloud_run_service.web"},"action":"update"}}`)
		fmt.Fprintln(os.Stdout, `{"type":"planned_change","change":{"resource":{"addr":"google_sql_database.old"},"action":"delete"}}`)
		fmt.Fprintln(os.Stdout, `{"type":"planned_change","change":{"resource":{"addr":"google_artifact_registry.repo"},"action":"replace"}}`)
		os.Exit(2)
	case "plan_error":
		fmt.Fprintln(os.Stderr, "Error: Failed to load backend config")
		os.Exit(1)
	case "plan_malformed":
		fmt.Fprintln(os.Stdout, "garbage line without json")
		fmt.Fprintln(os.Stdout, `{"malformed":`)
		fmt.Fprintln(os.Stdout, `{"type":"planned_change","change":{"resource":{"addr":"a.b"},"action":"create"}}`)
		os.Exit(2)
	default:
		fmt.Fprintf(os.Stderr, "unknown drift helper mode: %s\n", mode)
		os.Exit(99)
	}
}

func newDriftHelperArgs(t *testing.T, mode string) (scriptPath string, env []string) {
	t.Helper()
	tmpDir := t.TempDir()
	scriptPath = filepath.Join(tmpDir, "terraform")

	script := fmt.Sprintf("#!/bin/sh\nexec %s -test.run=TestHelperDrift -- \"$@\"\n", os.Args[0])
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to create terraform shim: %v", err)
	}

	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	env = append(os.Environ(),
		"GO_WANT_DRIFT_HELPER=1",
		"DRIFT_HELPER_MODE="+mode,
	)
	return scriptPath, env
}

func TestRunTerraformPlan_NoDrift(t *testing.T) {
	_, env := newDriftHelperArgs(t, "plan_no_drift")
	summary, err := runTerraformPlan(context.Background(), t.TempDir(), env, []string{"plan"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.HasDrift {
		t.Errorf("expected HasDrift=false, got true")
	}
	if summary.Add != 0 || summary.Change != 0 || summary.Destroy != 0 {
		t.Errorf("expected zero counts, got add=%d change=%d destroy=%d", summary.Add, summary.Change, summary.Destroy)
	}
	if len(summary.Resources) != 0 {
		t.Errorf("expected empty resources list, got %+v", summary.Resources)
	}
}

func TestRunTerraformPlan_Drift(t *testing.T) {
	_, env := newDriftHelperArgs(t, "plan_drift")
	summary, err := runTerraformPlan(context.Background(), t.TempDir(), env, []string{"plan"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !summary.HasDrift {
		t.Fatal("expected HasDrift=true")
	}
	if summary.Add != 2 {
		t.Errorf("expected add=2 (create + replace), got %d", summary.Add)
	}
	if summary.Change != 1 {
		t.Errorf("expected change=1, got %d", summary.Change)
	}
	if summary.Destroy != 2 {
		t.Errorf("expected destroy=2 (delete + replace), got %d", summary.Destroy)
	}
	if len(summary.Resources) != 4 {
		t.Fatalf("expected 4 resources, got %d: %+v", len(summary.Resources), summary.Resources)
	}
	expectedAddrs := []string{
		"google_artifact_registry.repo",
		"google_cloud_run_service.web",
		"google_compute_instance.api",
		"google_sql_database.old",
	}
	for i, addr := range expectedAddrs {
		if summary.Resources[i].Address != addr {
			t.Errorf("resource %d: want %q, got %q", i, addr, summary.Resources[i].Address)
		}
	}
}

func TestRunTerraformPlan_Error(t *testing.T) {
	_, env := newDriftHelperArgs(t, "plan_error")
	_, err := runTerraformPlan(context.Background(), t.TempDir(), env, []string{"plan"})
	if err == nil {
		t.Fatal("expected error for exit code 1")
	}
	if !strings.Contains(err.Error(), "Failed to load backend config") {
		t.Errorf("expected stderr to propagate, got: %v", err)
	}
}

func TestRunTerraformPlan_TolerantOfMalformedLines(t *testing.T) {
	_, env := newDriftHelperArgs(t, "plan_malformed")
	summary, err := runTerraformPlan(context.Background(), t.TempDir(), env, []string{"plan"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !summary.HasDrift {
		t.Error("expected HasDrift=true from the one valid planned_change")
	}
	if summary.Add != 1 || len(summary.Resources) != 1 {
		t.Errorf("expected exactly one create resource, got add=%d resources=%+v", summary.Add, summary.Resources)
	}
	if summary.Resources[0].Address != "a.b" {
		t.Errorf("unexpected address: %+v", summary.Resources[0])
	}
}

func TestParsePlanJSONStream_IgnoresNonPlannedChange(t *testing.T) {
	input := strings.Join([]string{
		`{"@level":"info","type":"version","terraform":"1.7.0"}`,
		`{"@level":"info","type":"log","message":"hi"}`,
		`{"type":"planned_change","change":{"resource":{"addr":"aws_s3_bucket.b"},"action":"create"}}`,
	}, "\n")
	summary, err := parsePlanJSONStream(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if summary.Add != 1 || len(summary.Resources) != 1 {
		t.Fatalf("expected one create, got %+v", summary)
	}
}

func TestParsePlanJSONStream_StripsActionValues(t *testing.T) {
	line := `{"type":"planned_change","change":{"resource":{"addr":"x.y"},"action":"update","before":{"api_key":"supersecret"},"after":{"api_key":"newsecret"}}}`
	summary, err := parsePlanJSONStream(strings.NewReader(line))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(summary.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(summary.Resources))
	}
	buf, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(buf), "supersecret") || strings.Contains(string(buf), "newsecret") {
		t.Errorf("drift summary leaked secrets from change.before/after: %s", string(buf))
	}
}

func TestReadImageVarsFromState_MissingStateFile(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(cwd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	if got := readImageVarsFromState(); got != nil {
		t.Errorf("expected nil when state.json missing, got %v", got)
	}
}

func TestReadImageVarsFromState_ExtractsImageKeys(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(cwd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	stateFile := &coreenv.EnvStateFile{
		Provider: "terraform",
		State: map[string]string{
			"image_api":   "gcr.io/proj/api:tag-1",
			"image_web":   "gcr.io/proj/web:tag-1",
			"other_field": "irrelevant",
		},
	}
	if err := os.MkdirAll(".grove/env", 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(stateFile)
	if err := os.WriteFile(".grove/env/state.json", data, 0o600); err != nil {
		t.Fatal(err)
	}

	got := readImageVarsFromState()
	if got == nil {
		t.Fatal("expected non-nil image vars")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 image keys, got %d: %v", len(got), got)
	}
	if got["image_api"] != "gcr.io/proj/api:tag-1" {
		t.Errorf("missing image_api: %v", got)
	}
	if got["image_web"] != "gcr.io/proj/web:tag-1" {
		t.Errorf("missing image_web: %v", got)
	}
	if _, exists := got["other_field"]; exists {
		t.Errorf("unexpected non-image key leaked through: %v", got)
	}
}

func TestReadImageVarsFromState_NoImageKeys(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(cwd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	stateFile := &coreenv.EnvStateFile{
		Provider: "terraform",
		State:    map[string]string{"other": "val"},
	}
	if err := os.MkdirAll(".grove/env", 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(stateFile)
	if err := os.WriteFile(".grove/env/state.json", data, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := readImageVarsFromState(); got != nil {
		t.Errorf("expected nil when no image_ keys present, got %v", got)
	}
}

func TestEmitSummary_JSON(t *testing.T) {
	summary := &DriftSummary{
		Profile:   "terraform",
		Provider:  "terraform",
		HasDrift:  true,
		Add:       1,
		Change:    0,
		Destroy:   0,
		Resources: []DriftResource{{Address: "foo.bar", Action: "create"}},
	}
	var buf strings.Builder
	if err := EmitSummary(&buf, summary, true); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var round DriftSummary
	if err := json.Unmarshal([]byte(buf.String()), &round); err != nil {
		t.Fatalf("summary was not valid JSON: %v\nOutput: %s", err, buf.String())
	}
	if !round.HasDrift || round.Add != 1 || len(round.Resources) != 1 {
		t.Errorf("round-tripped summary missing fields: %+v", round)
	}
}

func TestEmitSummary_HumanNoDrift(t *testing.T) {
	summary := &DriftSummary{Profile: "terraform", Provider: "terraform"}
	var buf strings.Builder
	if err := EmitSummary(&buf, summary, false); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(buf.String(), "No drift detected") {
		t.Errorf("expected 'No drift detected' message, got: %q", buf.String())
	}
}

func TestDisplayProfile_EmptyBecomesDefault(t *testing.T) {
	if got := DisplayProfile(""); got != "default" {
		t.Errorf("expected 'default', got %q", got)
	}
	if got := DisplayProfile("hybrid-api"); got != "hybrid-api" {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestRunTerraformPlan_PropagatesExitError(t *testing.T) {
	_, env := newDriftHelperArgs(t, "plan_error")
	_, err := runTerraformPlan(context.Background(), t.TempDir(), env, []string{"plan"})
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		t.Errorf("expected wrapped error string, got raw *exec.ExitError: %v", err)
	}
	if err == nil {
		t.Fatal("expected non-nil error")
	}
}
