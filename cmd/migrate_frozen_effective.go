package cmd

// This file contains the independent, read-only side of the migration proof.
// It intentionally does not consume legacyMigration or migration candidates:
// it replays the frozen source precedence into the old effective topology.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/paths"
)

func loadFrozenLegacyEffectiveConfig() (*config.Config, error) {
	dir := paths.ConfigDir()
	globals, err := legacyGlobalFiles(dir)
	if err != nil {
		return nil, err
	}
	dead, err := legacyDeadMachineFiles(dir)
	if err != nil {
		return nil, err
	}
	cfg := &config.Config{Groves: map[string]config.GroveSourceConfig{}, Notebooks: &config.NotebooksConfig{Definitions: map[string]*config.Notebook{}, Rules: &config.NotebookRules{}}}
	// Dead imports are lowest precedence, followed by the live global cascade.
	for _, src := range append(dead, globals...) {
		for name, nb := range src.Config.Notebooks.Definitions {
			copy := nb.Notebook
			if copy.RootDir == "" {
				copy.RootDir = nb.Root
			}
			if copy.RootDir != "" {
				cfg.Notebooks.Definitions[name] = &copy
			}
		}
		if d := src.Config.Notebooks.Rules.Default; d != "" {
			cfg.Notebooks.Rules.Default = d
		}
		if global := src.Config.Notebooks.Rules.Global; global != nil {
			copy := *global
			cfg.Notebooks.Rules.Global = &copy
		}
		for name, s := range src.Config.SearchPaths {
			cfg.Groves[name] = config.GroveSourceConfig{Path: s.Path, Enabled: effectiveEnabled(s.Enabled), Description: s.Description}
		}
		for name, g := range src.Config.Groves {
			repos, exclude := g.Repos, g.Exclude
			if len(repos) == 0 {
				repos = g.IncludeRepos
			}
			if len(exclude) == 0 {
				exclude = g.ExcludeRepos
			}
			notebook := g.Notebook
			if config.FindEcosystemManifest(expandPath(g.Path)) != "" && notebook == "" {
				notebook, _, err = legacyCardDefault(g.Path)
				if err != nil {
					return nil, err
				}
			}
			cfg.Groves[name] = config.GroveSourceConfig{Path: g.Path, Enabled: effectiveEnabled(g.Enabled), Description: g.Description, Notebook: notebook, Depth: g.Depth, IncludeRepos: append([]string(nil), repos...), ExcludeRepos: append([]string(nil), exclude...)}
		}
	}
	// Frozen machine topology filled absent global names only.
	machinePath := filepath.Join(dir, "machine.toml")
	if st, statErr := os.Stat(machinePath); statErr == nil && !st.IsDir() {
		var mc legacyMachineConfig
		if err := parseLegacyFile(machinePath, &mc); err != nil {
			return nil, err
		}
		for name, e := range mc.Topology.Ecosystems {
			if _, exists := cfg.Groves[name]; exists {
				continue
			}
			nb := e.Notebook
			if nb == "" {
				nb, _, err = legacyCardDefault(e.Path)
				if err != nil {
					return nil, err
				}
			}
			cfg.Groves[name] = config.GroveSourceConfig{Path: e.Path, Enabled: effectiveEnabled(e.Enabled), Description: e.Description, Notebook: nb, IncludeRepos: append([]string(nil), e.Repos...), ExcludeRepos: append([]string(nil), e.Exclude...)}
		}
		for name, r := range mc.Topology.Roots {
			if _, exists := cfg.Groves[name]; exists {
				continue
			}
			cfg.Groves[name] = config.GroveSourceConfig{Path: r.Path, Enabled: effectiveEnabled(r.Enabled), Description: r.Description, Notebook: r.Notebook, Depth: r.Depth, IncludeRepos: append([]string(nil), r.Repos...), ExcludeRepos: append([]string(nil), r.Exclude...)}
		}
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}

	recorded, err := coderoot.Load()
	if err != nil {
		return nil, err
	}
	for name, nb := range recorded.Notebooks {
		cfg.Notebooks.Definitions[name] = &config.Notebook{RootDir: nb.Root}
	}
	if recorded.Default != "" {
		cfg.Notebooks.Rules.Default = recorded.Default
	}
	for name, r := range recorded.Roots {
		cfg.Groves[name] = config.GroveSourceConfig{Path: r.Path, Enabled: effectiveEnabled(r.Enabled), Description: r.Description, Notebook: r.Notebook, Depth: r.Depth, IncludeRepos: append([]string(nil), r.Repos...), ExcludeRepos: append([]string(nil), r.Exclude...)}
	}
	for name, grove := range cfg.Groves {
		if grove.Notebook == "" {
			grove.Notebook = cfg.Notebooks.Rules.Default
			cfg.Groves[name] = grove
		}
		if grove.Notebook == "" {
			return nil, fmt.Errorf("frozen effective root %s has no notebook binding", name)
		}
	}
	return cfg, nil
}

func effectiveEnabled(value *bool) *bool {
	if value != nil {
		copy := *value
		return &copy
	}
	yes := true
	return &yes
}
