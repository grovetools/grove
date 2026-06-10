// Package doctorchecks registers grove-specific diagnostics with the shared
// core doctor registry (github.com/grovetools/core/pkg/doctor). Import it for
// side effects:
//
//	import _ "github.com/grovetools/grove/pkg/doctorchecks"
//
// All checks here are strictly read-only with respect to the user's
// environment: they never modify config, spawn daemons, or write files.
package doctorchecks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/doctor"
	"github.com/grovetools/core/pkg/paths"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func init() {
	doctor.Register(&configLayersCheck{})
}

// layerFile is one config file that participates in layered config loading.
type layerFile struct {
	Path string
	Kind string
}

// collectLayerFiles enumerates every config file that core's LoadLayered
// considers, mirroring its lookup order: global config, global fragments,
// global overrides, GROVE_CONFIG_OVERLAY, project config, ecosystem config,
// and project overrides. Only files that exist are returned.
func collectLayerFiles(startDir string) []layerFile {
	seen := map[string]bool{}
	var out []layerFile
	add := func(kind, path string) {
		if path == "" {
			return
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		if seen[path] {
			return
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return
		}
		seen[path] = true
		out = append(out, layerFile{Path: path, Kind: kind})
	}

	if configDir := paths.ConfigDir(); configDir != "" {
		// Global config: TOML preferred, YAML read-compat (mirrors getXDGConfigPath).
		for _, name := range []string{"grove.toml", "grove.yml"} {
			p := filepath.Join(configDir, name)
			if _, err := os.Stat(p); err == nil {
				add("global config", p)
				break
			}
		}

		// Global fragments: modular *.toml files next to the global config.
		if files, err := filepath.Glob(filepath.Join(configDir, "*.toml")); err == nil {
			sort.Strings(files)
			for _, f := range files {
				switch filepath.Base(f) {
				case "grove.toml", "grove.yml", "grove.override.toml":
					continue
				}
				add("global fragment", f)
			}
		}

		// Global overrides. Core loads the first file that both exists and
		// parses, so a broken earlier file silently falls through; probe all.
		for _, name := range []string{"grove.override.yml", "grove.override.yaml", "grove.override.toml"} {
			add("global override", filepath.Join(configDir, name))
		}
	}

	if overlay := os.Getenv("GROVE_CONFIG_OVERLAY"); overlay != "" {
		add("env overlay (GROVE_CONFIG_OVERLAY)", expandUserPath(overlay))
	}

	if projectPath, err := config.FindConfigFile(startDir); err == nil && projectPath != "" {
		add("project config", projectPath)
		projectDir := filepath.Dir(projectPath)

		if eco := config.FindEcosystemConfig(projectDir); eco != "" {
			add("ecosystem config", eco)
		}

		// Mirrors core's projectOverrideFiles (incl. legacy .grove-work.* names).
		for _, name := range []string{
			"grove.override.yml", "grove.override.yaml", "grove.override.toml",
			".grove.override.yml", ".grove.override.yaml", ".grove.override.toml",
			".grove-work.yml", ".grove-work.yaml", ".grove-work.toml",
		} {
			add("project override", filepath.Join(projectDir, name))
		}
	}

	return out
}

var envVarRe = regexp.MustCompile(`\$\{([^}]+)\}`)

// expandLayerEnvVars replaces ${VAR} (and ${VAR:-default}) the same way core's
// config loader does before parsing.
func expandLayerEnvVars(content string) string {
	return envVarRe.ReplaceAllStringFunc(content, func(match string) string {
		varName := envVarRe.FindStringSubmatch(match)[1]
		parts := strings.SplitN(varName, ":-", 2)
		varName = parts[0]
		defaultValue := ""
		if len(parts) > 1 {
			defaultValue = parts[1]
		}
		if value := os.Getenv(varName); value != "" {
			return value
		}
		return defaultValue
	})
}

// expandUserPath expands env vars and a leading ~/ in a path.
func expandUserPath(path string) string {
	path = os.ExpandEnv(path)
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	return path
}

// parseLayerFile parses one config layer file with the same semantics as
// core's unmarshalConfig (env expansion, then TOML or YAML into the typed
// Config). It also returns the raw generic document for schema validation.
func parseLayerFile(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	expanded := []byte(expandLayerEnvVars(string(data)))

	var typed config.Config
	var raw map[string]interface{}
	if strings.HasSuffix(path, ".toml") {
		if err := toml.Unmarshal(expanded, &typed); err != nil {
			return nil, err
		}
		if err := toml.Unmarshal(expanded, &raw); err != nil {
			return nil, err
		}
	} else {
		if err := yaml.Unmarshal(expanded, &typed); err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(expanded, &raw); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

// configLayersCheck re-loads every config layer file and reports any that
// fail to parse. Core's LoadLayered silently warn-and-skips broken global
// fragments and overrides, so a typo'd file means its settings just vanish;
// this check surfaces that as a hard failure with the path and parse error.
type configLayersCheck struct{}

func (c *configLayersCheck) ID() string   { return "config_fragments" }
func (c *configLayersCheck) Name() string { return "config layer files parse cleanly" }

func (c *configLayersCheck) Run(ctx context.Context, opts doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}

	startDir, err := os.Getwd()
	if err != nil {
		res.Status = doctor.StatusWarn
		res.Message = "could not determine working directory; skipping config layer check"
		res.Error = err.Error()
		return res
	}

	files := collectLayerFiles(startDir)
	if len(files) == 0 {
		res.Status = doctor.StatusOK
		res.Message = "no grove config files found; nothing to validate"
		return res
	}

	var failures []string
	for _, f := range files {
		if _, err := parseLayerFile(f.Path); err != nil {
			failures = append(failures, fmt.Sprintf("%s [%s]: %v", f.Path, f.Kind, compactError(err)))
		}
	}

	if len(failures) > 0 {
		res.Status = doctor.StatusFail
		res.Message = fmt.Sprintf("%d of %d config file(s) failed to parse (broken layers are silently skipped at load time)", len(failures), len(files))
		res.Error = strings.Join(failures, "; ")
		res.Resolution = "fix the syntax in the listed file(s); until then their settings are not applied"
		return res
	}

	res.Status = doctor.StatusOK
	res.Message = fmt.Sprintf("%d config layer file(s) parsed cleanly", len(files))
	return res
}

func (c *configLayersCheck) AutoFix(ctx context.Context) error {
	return fmt.Errorf("%w: edit the broken config file(s) by hand", doctor.ErrNotFixable)
}

// compactError flattens a (possibly multi-line) parse error onto one line.
func compactError(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}
