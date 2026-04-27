// Package envdrift reconciles a grove terraform-backed environment profile
// against deployed cloud state by running `terraform plan -detailed-exitcode
// -json` and parsing the emitted events into a drift summary. It is consumed
// by both the `grove env drift` CLI and the env TUI so they share one drift
// engine.
package envdrift

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/config"
	coreenv "github.com/grovetools/core/pkg/env"
	"github.com/grovetools/core/pkg/envtf"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/core/state"
)

// TerraformDriftExitCode is the exit status `grove env drift` uses when
// Terraform reports pending changes. Matches `terraform plan -detailed-exitcode`.
const TerraformDriftExitCode = 2

// DriftResource summarizes a single planned change. We intentionally drop
// Terraform's `change.before`/`change.after` attribute payloads — those
// contain actual resource values (including potential secrets) and are
// unnecessary for "what resources would change?" reconciliation.
type DriftResource struct {
	Address string `json:"address"`
	Action  string `json:"action"` // "create" | "update" | "delete" | "replace" | "no-op"
}

// DriftSummary is the shape emitted by `grove env drift --json` and consumed
// by the TUI Overview page.
type DriftSummary struct {
	Profile   string          `json:"profile"`
	Provider  string          `json:"provider"`
	HasDrift  bool            `json:"has_drift"`
	Add       int             `json:"add"`
	Change    int             `json:"change"`
	Destroy   int             `json:"destroy"`
	Resources []DriftResource `json:"resources"`
}

// RunEnvDrift resolves the profile, sets up the Terraform workspace the same
// way `grove env up` would, and runs `terraform plan -detailed-exitcode -json`.
// It returns a summary on success (no-drift and drift both counted as success);
// only infrastructure failures are surfaced as errors.
//
// Configuration + workspace are discovered from the current working directory,
// matching the CLI's behaviour. Callers invoking this from a TUI should already
// have chdir'd to (or be running in) the target worktree.
func RunEnvDrift(ctx context.Context, profile string) (*DriftSummary, error) {
	resolved, activeProfile, cfg, err := resolveEnvConfig(profile)
	if err != nil {
		return nil, err
	}
	if resolved.Provider != "terraform" {
		return nil, fmt.Errorf("grove env drift only supports the 'terraform' provider (profile %q uses %q)",
			DisplayProfile(activeProfile), resolved.Provider)
	}

	stateDir, err := filepath.Abs(envStateDir())
	if err != nil {
		return nil, fmt.Errorf("failed to resolve state dir: %w", err)
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	req := coreenv.EnvRequest{
		Provider: resolved.Provider,
		StateDir: stateDir,
		PlanDir:  stateDir,
		Config:   resolved.Config,
	}
	if node := currentWorkspace(); node != nil {
		req.Workspace = node
	}
	if req.Workspace == nil {
		return nil, fmt.Errorf("grove env drift must be run inside a grove workspace")
	}

	// Inject shared_backend_config exactly like `grove env up` does so that
	// shared-infra outputs surface in the plan's tfvars.
	if sharedEnv, ok := resolved.Config["shared_env"].(string); ok && sharedEnv != "" {
		sharedRef, _ := resolved.Config["shared_ref"].(string)
		if sharedRef == "" {
			sharedRef = "main"
		}
		stateBackend, _ := resolved.Config["state_backend"].(string)
		stateBucket, _ := resolved.Config["state_bucket"].(string)

		if stateBackend == "gcs" && stateBucket != "" {
			ecosystem := ""
			if req.Workspace.ParentEcosystemPath != "" {
				ecosystem = filepath.Base(req.Workspace.ParentEcosystemPath)
			}
			if ecosystem == "" && req.Workspace.RootEcosystemPath != "" {
				ecosystem = filepath.Base(req.Workspace.RootEcosystemPath)
			}
			prefix := sharedEnv + "/" + sharedRef
			if ecosystem != "" {
				prefix = ecosystem + "/" + sharedEnv + "/" + sharedRef
			}
			req.Config["shared_backend_config"] = map[string]interface{}{
				"state_backend": stateBackend,
				"state_bucket":  stateBucket,
				"state_prefix":  prefix,
			}
		}
	}

	// Re-use image URIs persisted by the last `grove env up`. Rebuilding here
	// would produce new tags and falsely flag Cloud Run services as drifted.
	imageVars := readImageVarsFromState()

	// Fetch shared outputs when the profile inherits from shared infra. This
	// is a read-only `terraform output` call, so it's safe from the CLI and
	// gives the plan an accurate picture of what the shared VPC/registry/etc.
	// look like today.
	var sharedOutputs map[string]interface{}
	if sharedCfg, ok := req.Config["shared_backend_config"].(map[string]interface{}); ok {
		sharedOutputs, err = envtf.FetchSharedOutputs(ctx, sharedCfg)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch shared outputs: %w", err)
		}
	}

	payload, err := envtf.BuildTfVarsPayload(req, imageVars, sharedOutputs)
	if err != nil {
		return nil, err
	}
	varsPath, err := envtf.WriteTfVars(stateDir, payload)
	if err != nil {
		return nil, err
	}

	modulePath, _ := req.Config["path"].(string)
	if modulePath == "" {
		modulePath = "./infra"
	}
	// Shared-infra profiles live at the ecosystem root; a per-worktree cwd
	// doesn't own the TF module, so resolve the path relative to the root
	// ecosystem checkout rather than the invoking worktree.
	baseDir := req.Workspace.Path
	if config.IsSharedProfile(cfg, activeProfile) {
		if req.Workspace.RootEcosystemPath != "" {
			baseDir = req.Workspace.RootEcosystemPath
		} else if req.Workspace.ParentEcosystemPath != "" {
			baseDir = req.Workspace.ParentEcosystemPath
		}
	}
	moduleAbs := filepath.Join(baseDir, modulePath)

	bc := envtf.ResolveBackend(req)
	overridePath, err := envtf.WriteBackendOverride(moduleAbs, bc)
	if err != nil {
		return nil, err
	}
	if overridePath != "" {
		defer os.Remove(overridePath)
	}

	resolvedEnv, err := coreenv.ResolveConfigEnv(ctx, req.Config, req.Workspace.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve environment variables: %w", err)
	}
	tfEnv := append(resolvedEnv, "TF_DATA_DIR="+filepath.Join(stateDir, ".terraform"))

	initArgs := envtf.BuildInitArgs(bc)
	initCmd := exec.CommandContext(ctx, "terraform", initArgs...)
	initCmd.Dir = moduleAbs
	initCmd.Env = tfEnv
	if output, err := initCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("terraform init failed: %w\nOutput: %s", err, string(output))
	}

	planArgs := []string{"plan", "-detailed-exitcode", "-input=false", "-json", "-var-file=" + varsPath}
	if bc.Type == "local" {
		planArgs = append(planArgs, "-state="+filepath.Join(stateDir, "terraform.tfstate"))
	}
	if extraVarFile, ok := req.Config["var_file"].(string); ok && extraVarFile != "" {
		planArgs = append(planArgs, "-var-file="+filepath.Join(req.Workspace.Path, extraVarFile))
	}

	summary, err := runTerraformPlan(ctx, moduleAbs, tfEnv, planArgs)
	if err != nil {
		return nil, err
	}
	summary.Profile = DisplayProfile(activeProfile)
	summary.Provider = resolved.Provider

	// Persist the result so the ecosystem-mode TUI and repeat CLI invocations
	// can skip a full plan when the cache is still fresh. A failure here is
	// non-fatal — the drift result itself is already valid.
	_ = SaveCache(stateDir, summary)

	return summary, nil
}

// runTerraformPlan executes `terraform plan -detailed-exitcode -json` and
// parses the streamed JSON events into a drift summary.
func runTerraformPlan(ctx context.Context, dir string, env []string, args []string) (*DriftSummary, error) {
	cmd := exec.CommandContext(ctx, "terraform", args...)
	cmd.Dir = dir
	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to capture terraform stdout: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start terraform: %w", err)
	}

	summary, parseErr := parsePlanJSONStream(stdout)
	waitErr := cmd.Wait()

	if parseErr != nil && !errors.Is(parseErr, io.EOF) {
		return nil, fmt.Errorf("failed to parse terraform plan output: %w\nStderr: %s", parseErr, stderr.String())
	}

	exitCode := 0
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			return nil, fmt.Errorf("terraform plan failed: %w\nStderr: %s", waitErr, stderr.String())
		}
	}

	switch exitCode {
	case 0:
		summary.HasDrift = false
	case 2:
		summary.HasDrift = true
	default:
		return nil, fmt.Errorf("terraform plan failed (exit %d): %s", exitCode, stderr.String())
	}

	return summary, nil
}

// parsePlanJSONStream reads the newline-delimited JSON stream emitted by
// `terraform plan -json` and aggregates `planned_change` events into a
// drift summary. All other event types (log, version, etc.) are ignored.
// We intentionally never touch the `change.before` / `change.after` payloads
// — those contain actual attribute values and can include secrets.
func parsePlanJSONStream(r io.Reader) (*DriftSummary, error) {
	summary := &DriftSummary{Resources: []DriftResource{}}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var event struct {
			Type   string `json:"type"`
			Change struct {
				Resource struct {
					Addr string `json:"addr"`
				} `json:"resource"`
				Action string `json:"action"`
			} `json:"change"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		if event.Type != "planned_change" {
			continue
		}
		action := event.Change.Action
		if action == "" || action == "no-op" || action == "read" {
			continue
		}
		summary.Resources = append(summary.Resources, DriftResource{
			Address: event.Change.Resource.Addr,
			Action:  action,
		})
		switch action {
		case "create":
			summary.Add++
		case "update":
			summary.Change++
		case "delete":
			summary.Destroy++
		case "replace":
			summary.Add++
			summary.Destroy++
		}
	}
	if err := scanner.Err(); err != nil {
		return summary, err
	}

	sort.SliceStable(summary.Resources, func(i, j int) bool {
		return summary.Resources[i].Address < summary.Resources[j].Address
	})
	return summary, nil
}

// readImageVarsFromState returns the image_* keys stored in
// .grove/env/state.json by the last successful `terraform up`. Returns nil
// when the state file is missing or the state map does not contain any
// image keys.
func readImageVarsFromState() map[string]string {
	stateFile, err := readEnvState()
	if err != nil || stateFile == nil {
		return nil
	}
	if len(stateFile.State) == 0 {
		return nil
	}
	out := make(map[string]string)
	for k, v := range stateFile.State {
		if strings.HasPrefix(k, "image_") {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// EmitSummary renders the summary as JSON or a terse human-readable block.
// The human block intentionally mirrors `terraform plan`'s vocabulary.
func EmitSummary(w io.Writer, s *DriftSummary, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(s)
	}
	if !s.HasDrift {
		fmt.Fprintf(w, "No drift detected for profile %q (provider: %s)\n", s.Profile, s.Provider)
		return nil
	}
	fmt.Fprintf(w, "Drift detected for profile %q (provider: %s)\n", s.Profile, s.Provider)
	fmt.Fprintf(w, "Plan: %d to add, %d to change, %d to destroy.\n", s.Add, s.Change, s.Destroy)
	if len(s.Resources) > 0 {
		fmt.Fprintln(w, "\nResources:")
		for _, r := range s.Resources {
			fmt.Fprintf(w, "  %-10s %s\n", r.Action, r.Address)
		}
	}
	return nil
}

// DisplayProfile maps the empty "config default" sentinel to a human label.
func DisplayProfile(profile string) string {
	if profile == "" {
		return "default"
	}
	return profile
}

// envStateDir returns the path to .grove/env/ in the current working directory.
func envStateDir() string {
	return filepath.Join(".", ".grove", "env")
}

// envStatePath returns the path to .grove/env/state.json in the current working directory.
func envStatePath() string {
	return filepath.Join(envStateDir(), "state.json")
}

// readEnvState reads and parses .grove/env/state.json from the cwd.
func readEnvState() (*coreenv.EnvStateFile, error) {
	data, err := os.ReadFile(envStatePath())
	if err != nil {
		return nil, err
	}
	var stateFile coreenv.EnvStateFile
	if err := json.Unmarshal(data, &stateFile); err != nil {
		return nil, fmt.Errorf("failed to parse state.json: %w", err)
	}
	return &stateFile, nil
}

// resolveEnvConfig resolves the environment config for the active/specified
// profile using cwd-based config + sticky-default state. Returns the raw
// *config.Config alongside the resolved profile so callers can answer
// cross-profile questions (e.g. IsSharedProfile) without a second load.
func resolveEnvConfig(envProfile string) (*config.EnvironmentConfig, string, *config.Config, error) {
	cfg, err := config.LoadDefault()
	if err != nil {
		return nil, "", nil, err
	}

	profile := envProfile
	if profile == "" {
		profile, _ = state.GetString("environment")
	}

	resolved, err := config.ResolveEnvironment(cfg, profile)
	if err != nil {
		return nil, "", nil, err
	}

	return resolved, profile, cfg, nil
}

// currentWorkspace returns the workspace node for the current directory.
func currentWorkspace() *workspace.WorkspaceNode {
	cwd, _ := os.Getwd()
	node, err := workspace.GetProjectByPath(cwd)
	if err != nil {
		return nil
	}
	return node
}
