package cmd

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

	coreenv "github.com/grovetools/core/pkg/env"
	"github.com/grovetools/core/pkg/envtf"
	"github.com/spf13/cobra"
)

// terraformDriftExitCode is the exit status `grove env drift` uses when
// Terraform reports pending changes. It mirrors `terraform plan
// -detailed-exitcode`'s `2` and is intentionally emitted via os.Exit so the
// Cobra wrapper doesn't print its own "command failed" usage message.
const terraformDriftExitCode = 2

// driftResource summarizes a single planned change. We intentionally drop
// Terraform's `change.before`/`change.after` attribute payloads — those
// contain actual resource values (including potential secrets) and are
// unnecessary for "what resources would change?" reconciliation.
type driftResource struct {
	Address string `json:"address"`
	Action  string `json:"action"` // "create" | "update" | "delete" | "replace" | "no-op"
}

// driftSummary is the shape emitted by `grove env drift --json`.
type driftSummary struct {
	Profile   string          `json:"profile"`
	Provider  string          `json:"provider"`
	HasDrift  bool            `json:"has_drift"`
	Add       int             `json:"add"`
	Change    int             `json:"change"`
	Destroy   int             `json:"destroy"`
	Resources []driftResource `json:"resources"`
}

func newEnvDriftCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "drift [profile]",
		Short: "Detect drift between local configuration and deployed cloud state",
		Long: `Reconcile the configuration against deployed cloud state.

This command runs 'terraform plan' to detect drift between your local
configuration and the live cloud resources. It does not modify any
infrastructure. It never builds images: if the profile defines 'images',
the URIs built on the last successful 'grove env up' are read from
.grove/env/state.json and passed in as tfvars so an image rebuild does
not masquerade as cloud drift.

Exit codes match Terraform semantics:
  0 = Succeeded, no drift detected
  1 = Error occurred
  2 = Succeeded, drift detected`,
		Example: `  # Check drift for a specific profile
  grove env drift terraform

  # Machine-readable summary for a CI script
  grove env drift terraform --json | jq '{add, change, destroy}'

  # List the resources that are drifting
  grove env drift hybrid-api --json | jq '.resources[].address'`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := ""
			if len(args) == 1 {
				profile = args[0]
				if profile == "default" {
					profile = ""
				}
			}

			summary, err := runEnvDrift(context.Background(), profile)
			if err != nil {
				return err
			}

			if err := emitDriftSummary(os.Stdout, summary, jsonOutput); err != nil {
				return err
			}

			if summary.HasDrift {
				os.Exit(terraformDriftExitCode)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit JSON summary instead of human-readable output")
	return cmd
}

// runEnvDrift resolves the profile, sets up the Terraform workspace the same
// way `grove env up` would, and runs `terraform plan -detailed-exitcode -json`.
// It returns a summary on success (no-drift and drift both counted as success);
// only infrastructure failures are surfaced as errors.
func runEnvDrift(ctx context.Context, profile string) (*driftSummary, error) {
	resolved, activeProfile, err := resolveEnvConfig(profile)
	if err != nil {
		return nil, err
	}
	if resolved.Provider != "terraform" {
		return nil, fmt.Errorf("grove env drift only supports the 'terraform' provider (profile %q uses %q)",
			displayProfile(activeProfile), resolved.Provider)
	}

	stateDir, err := filepath.Abs(envStateDir())
	if err != nil {
		return nil, fmt.Errorf("failed to resolve state dir: %w", err)
	}
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	req := coreenv.EnvRequest{
		Provider: resolved.Provider,
		StateDir: stateDir,
		PlanDir:  stateDir,
		Config:   resolved.Config,
	}
	if node := getWorkspaceNode(); node != nil {
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
	// If state.json does not exist yet, we pass nil and Terraform uses the
	// module's default (typically empty string), which accurately plans a
	// create for the image-backed resources.
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
	moduleAbs := filepath.Join(req.Workspace.Path, modulePath)

	bc := envtf.ResolveBackend(req)
	overridePath, err := envtf.WriteBackendOverride(moduleAbs, bc)
	if err != nil {
		return nil, err
	}
	if overridePath != "" {
		defer os.Remove(overridePath)
	}

	// Resolve env (including cmd-based secrets) the same way the daemon does
	// so any `TF_VAR_*` or GOOGLE_APPLICATION_CREDENTIALS-style vars produced
	// by `env = { KEY = { cmd = "..." } }` land in terraform's environment.
	resolvedEnv, err := coreenv.ResolveConfigEnv(ctx, req.Config, req.Workspace.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve environment variables: %w", err)
	}
	tfEnv := append(resolvedEnv, "TF_DATA_DIR="+filepath.Join(stateDir, ".terraform"))

	// terraform init -reconfigure (matches the daemon's init). This is cheap
	// if .terraform/ is already populated from a prior `grove env up`.
	initArgs := envtf.BuildInitArgs(bc)
	initCmd := exec.CommandContext(ctx, "terraform", initArgs...)
	initCmd.Dir = moduleAbs
	initCmd.Env = tfEnv
	if output, err := initCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("terraform init failed: %w\nOutput: %s", err, string(output))
	}

	// terraform plan -detailed-exitcode -json
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
	summary.Profile = displayProfile(activeProfile)
	summary.Provider = resolved.Provider
	return summary, nil
}

// runTerraformPlan executes `terraform plan -detailed-exitcode -json` and
// parses the streamed JSON events into a drift summary. It is split out so
// env_drift_test.go can target it with a mocked terraform binary.
func runTerraformPlan(ctx context.Context, dir string, env []string, args []string) (*driftSummary, error) {
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

	// Interpret the exit code: 0 = no drift, 2 = drift, anything else = error.
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
func parsePlanJSONStream(r io.Reader) (*driftSummary, error) {
	summary := &driftSummary{Resources: []driftResource{}}

	scanner := bufio.NewScanner(r)
	// Terraform plan events can be large (module graphs, long descriptions);
	// bump the buffer from the default 64KB to something comfortable.
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
			// Ignore malformed lines; Terraform occasionally emits non-JSON
			// preambles under certain init conditions.
			continue
		}
		if event.Type != "planned_change" {
			continue
		}
		action := event.Change.Action
		if action == "" || action == "no-op" || action == "read" {
			continue
		}
		summary.Resources = append(summary.Resources, driftResource{
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
			// Replace = destroy + create.
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
// image keys, so non-image profiles (e.g. `terraform-infra`) behave identically
// to today.
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

// emitDriftSummary renders the summary as JSON or a terse human-readable
// block. The human block intentionally mirrors `terraform plan`'s
// "Plan: X to add, Y to change, Z to destroy." vocabulary for familiarity.
func emitDriftSummary(w io.Writer, s *driftSummary, asJSON bool) error {
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

// displayProfile maps the empty "config default" sentinel to a human label.
func displayProfile(profile string) string {
	if profile == "" {
		return "default"
	}
	return profile
}
