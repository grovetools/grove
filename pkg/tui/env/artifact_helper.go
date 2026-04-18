package env

import (
	"fmt"
	"path/filepath"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/env"
)

// ArtifactGroup is a labelled collection of filesystem / remote paths
// associated with a single profile, grouped by purpose (config sources,
// terraform, docker, runtime). The Summary page renders these groups as
// stacked label/path rows matching tui-mockup-b.html.
type ArtifactGroup struct {
	Group string
	Rows  []ArtifactRow
}

// ArtifactRow is one entry in an ArtifactGroup. Kind drives styling:
//   - ""           : normal local path
//   - "remote"     : cloud/GCS/tunnel path
//   - "generated"  : written on up (not present until env starts)
//   - "missing"    : would be created on up; render muted/strikethrough
type ArtifactRow struct {
	Label string
	Path  string
	Anno  string // optional right-hand annotation
	Kind  string
}

// deriveArtifacts returns the Sources & Artifacts groups for a profile,
// per the mockup. It pulls from three places: grove.toml (via the resolved
// EnvironmentConfig), the filesystem root of the workspace, and — when the
// profile is the locally-running one — the persisted state.json.
//
// workspaceRoot may be empty (no workspace bound yet); paths then render
// without a leading directory.
func deriveArtifacts(
	profileName string,
	provider string,
	resolved *config.EnvironmentConfig,
	state *env.EnvStateFile,
	workspaceRoot string,
	isRunning bool,
	allProfiles map[string]*config.EnvironmentConfig,
) []ArtifactGroup {
	var groups []ArtifactGroup

	// ---- config sources -----------------------------------------------
	cfgRows := []ArtifactRow{
		{
			Label: "ecosystem",
			Path:  pathJoin(workspaceRoot, "grove.toml"),
			Anno:  configBracket(profileName),
		},
	}
	groups = append(groups, ArtifactGroup{Group: "config sources", Rows: cfgRows})

	// ---- provider-specific groups -------------------------------------
	switch provider {
	case "terraform":
		tfRows := terraformRows(resolved, workspaceRoot)
		if len(tfRows) > 0 {
			groups = append(groups, ArtifactGroup{Group: "terraform", Rows: tfRows})
		}
		if shared := sharedInfraRows(resolved, allProfiles); len(shared) > 0 {
			groups = append(groups, ArtifactGroup{Group: "shared infra", Rows: shared})
		}
		if images := terraformImageRows(resolved, state, workspaceRoot); len(images) > 0 {
			groups = append(groups, ArtifactGroup{Group: "images", Rows: images})
		}
	case "docker":
		if rows := dockerRows(resolved, workspaceRoot); len(rows) > 0 {
			groups = append(groups, ArtifactGroup{Group: "docker artifacts", Rows: rows})
		}
	case "native":
		// Native has no provider-specific paths beyond the runtime group.
	}

	// ---- runtime group -------------------------------------------------
	runtimeGroup := runtimeRows(workspaceRoot, profileName, provider, state, isRunning)
	if len(runtimeGroup) > 0 {
		label := "runtime (active)"
		if !isRunning {
			label = "runtime (on up)"
		}
		groups = append(groups, ArtifactGroup{Group: label, Rows: runtimeGroup})
	}

	return groups
}

func configBracket(profile string) string {
	if profile == "" || profile == "default" {
		return "[environment]"
	}
	return fmt.Sprintf("[environments.%s]", profile)
}

// terraformRows surfaces the TF working dir, main.tf, variables.tf, the
// generated tfvars and the state backend (if configured via `backend`/
// `state_bucket`/etc. in the resolved config).
func terraformRows(resolved *config.EnvironmentConfig, root string) []ArtifactRow {
	if resolved == nil {
		return nil
	}
	workDir := stringFromConfig(resolved.Config, "path")
	var rows []ArtifactRow
	if workDir != "" {
		rows = append(rows, ArtifactRow{Label: "working dir", Path: pathJoin(root, workDir)})
		rows = append(rows, ArtifactRow{Label: "main", Path: pathJoin(root, workDir, "main.tf")})
		rows = append(rows, ArtifactRow{Label: "variables", Path: pathJoin(root, workDir, "variables.tf")})
		rows = append(rows, ArtifactRow{
			Label: "generated",
			Path:  pathJoin(root, workDir, "grove_context.auto.tfvars.json"),
			Anno:  "written on up",
			Kind:  "generated",
		})
	}
	if bucket := stringFromConfig(resolved.Config, "state_bucket"); bucket != "" {
		prefix := stringFromConfig(resolved.Config, "state_prefix")
		p := fmt.Sprintf("gcs://%s/", bucket)
		if prefix != "" {
			p = fmt.Sprintf("gcs://%s/%s/", bucket, prefix)
		}
		anno := ""
		if skip, ok := resolved.Config["skip_destroy"].(bool); ok && skip {
			anno = "skip_destroy=true"
		}
		rows = append(rows, ArtifactRow{Label: "state backend", Path: p, Anno: anno, Kind: "remote"})
	}
	return rows
}

// sharedInfraRows lists the profile this one depends on (via `shared_env`)
// plus that shared profile's TF state path if resolvable.
func sharedInfraRows(resolved *config.EnvironmentConfig, all map[string]*config.EnvironmentConfig) []ArtifactRow {
	if resolved == nil {
		return nil
	}
	shared := stringFromConfig(resolved.Config, "shared_env")
	if shared == "" {
		return nil
	}
	rows := []ArtifactRow{{
		Label: "depends on",
		Path:  fmt.Sprintf("%s profile", shared),
		Anno:  "shared_env",
	}}
	if sc, ok := all[shared]; ok && sc != nil {
		if bucket := stringFromConfig(sc.Config, "state_bucket"); bucket != "" {
			prefix := stringFromConfig(sc.Config, "state_prefix")
			p := fmt.Sprintf("gcs://%s/", bucket)
			if prefix != "" {
				p = fmt.Sprintf("gcs://%s/%s/", bucket, prefix)
			}
			rows = append(rows, ArtifactRow{
				Label: "shared state",
				Path:  p,
				Anno:  "read for outputs",
				Kind:  "remote",
			})
		}
	}
	return rows
}

// terraformImageRows lists Dockerfiles under `images.<name>.dockerfile`
// plus any persisted image URIs stored in state.State by key image_*.
func terraformImageRows(resolved *config.EnvironmentConfig, state *env.EnvStateFile, root string) []ArtifactRow {
	if resolved == nil {
		return nil
	}
	images, _ := resolved.Config["images"].(map[string]interface{})
	var rows []ArtifactRow
	for name, v := range images {
		sub, _ := v.(map[string]interface{})
		if sub == nil {
			continue
		}
		if df, ok := sub["dockerfile"].(string); ok && df != "" {
			anno := ""
			if state != nil {
				if uri, ok := state.State["image_"+name]; ok && uri != "" {
					anno = "→ " + uri
				}
			}
			rows = append(rows, ArtifactRow{
				Label: name + " Dockerfile",
				Path:  pathJoin(root, df),
				Anno:  anno,
			})
		}
	}
	return rows
}

// dockerRows surfaces the compose file path declared in the profile config.
func dockerRows(resolved *config.EnvironmentConfig, root string) []ArtifactRow {
	if resolved == nil {
		return nil
	}
	compose := stringFromConfig(resolved.Config, "compose_file")
	var rows []ArtifactRow
	if compose != "" {
		rows = append(rows, ArtifactRow{Label: "compose", Path: pathJoin(root, compose)})
	}
	return rows
}

// runtimeRows shows state.json and .env.local. When the profile isn't the
// active one these are annotated as "would be written" with Kind=missing.
func runtimeRows(root, profile, provider string, state *env.EnvStateFile, isRunning bool) []ArtifactRow {
	statePath := pathJoin(root, ".grove/env/state.json")
	envPath := pathJoin(root, ".env.local")

	if !isRunning {
		return []ArtifactRow{
			{Label: "state.json", Path: statePath, Anno: "would be written", Kind: "missing"},
			{Label: ".env.local", Path: envPath, Anno: "would be written", Kind: "missing"},
		}
	}
	stateAnno := ""
	if state != nil {
		p := state.Provider
		if p == "" {
			p = provider
		}
		stateAnno = fmt.Sprintf("provider=%s, env=%s", p, profile)
	}
	return []ArtifactRow{
		{Label: "state.json", Path: statePath, Anno: stateAnno},
		{Label: ".env.local", Path: envPath, Anno: "sourced by shells in this worktree"},
	}
}

func stringFromConfig(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func pathJoin(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return filepath.Join(kept...)
}
