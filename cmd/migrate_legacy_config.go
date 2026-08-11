package cmd

// This file is the deliberately frozen legacy boundary for `grove migrate`.
// Keep these DTOs private: normal config loading must never regain an
// authoring dependency on the schemas being removed.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
	"gopkg.in/yaml.v3"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/paths"
)

type legacySearchPath struct {
	Path        string `toml:"path" yaml:"path"`
	Enabled     *bool  `toml:"enabled" yaml:"enabled"`
	Description string `toml:"description" yaml:"description"`
}

type legacyGrove struct {
	Path         string   `toml:"path" yaml:"path"`
	Notebook     string   `toml:"notebook" yaml:"notebook"`
	Enabled      *bool    `toml:"enabled" yaml:"enabled"`
	Description  string   `toml:"description" yaml:"description"`
	Depth        *int     `toml:"depth" yaml:"depth"`
	IncludeRepos []string `toml:"include_repos" yaml:"include_repos"`
	ExcludeRepos []string `toml:"exclude_repos" yaml:"exclude_repos"`
	Repos        []string `toml:"repos" yaml:"repos"`
	Exclude      []string `toml:"exclude" yaml:"exclude"`
}

type legacyNotebook struct {
	// Embed the complete frozen notebook shape so the independent equivalence
	// side retains every setting, while Root accepts the oldest root alias.
	config.Notebook `toml:",inline" yaml:",inline"`
	Root            string `toml:"root" yaml:"root"`
}

type legacyNotebookRules struct {
	Default string                       `toml:"default" yaml:"default"`
	Global  *config.GlobalNotebookConfig `toml:"global" yaml:"global"`
}

type legacyNotebooks struct {
	Definitions map[string]legacyNotebook `toml:"definitions" yaml:"definitions"`
	Rules       legacyNotebookRules       `toml:"rules" yaml:"rules"`
}

type legacyConfig struct {
	Groves      map[string]legacyGrove      `toml:"groves" yaml:"groves"`
	SearchPaths map[string]legacySearchPath `toml:"search_paths" yaml:"search_paths"`
	Notebooks   legacyNotebooks             `toml:"notebooks" yaml:"notebooks"`
	GroveMeta   struct {
		Priority int `toml:"priority" yaml:"priority"`
	} `toml:"_grove" yaml:"_grove"`
}

type legacyEcosystem struct {
	Path        string   `toml:"path" yaml:"path"`
	Notebook    string   `toml:"notebook" yaml:"notebook"`
	Enabled     *bool    `toml:"enabled" yaml:"enabled"`
	Description string   `toml:"description" yaml:"description"`
	Repos       []string `toml:"repos" yaml:"repos"`
	Exclude     []string `toml:"exclude" yaml:"exclude"`
}

type legacyRoot struct {
	Path        string   `toml:"path" yaml:"path"`
	Notebook    string   `toml:"notebook" yaml:"notebook"`
	Enabled     *bool    `toml:"enabled" yaml:"enabled"`
	Description string   `toml:"description" yaml:"description"`
	Depth       *int     `toml:"depth" yaml:"depth"`
	Repos       []string `toml:"repos" yaml:"repos"`
	Exclude     []string `toml:"exclude" yaml:"exclude"`
}

type legacyMachineConfig struct {
	Topology struct {
		Ecosystems map[string]legacyEcosystem `toml:"ecosystems" yaml:"ecosystems"`
		Roots      map[string]legacyRoot      `toml:"roots" yaml:"roots"`
	} `toml:"machine" yaml:"machine"`
}

type legacySource struct {
	Path, Resolved, Label string
	Config                legacyConfig
	TOML                  bool // dialect follows the logical source, never its symlink target
	RemoveGlobal          bool
	RemoveMachine         bool
	RemoveSync            bool
	AnnotateCard          bool
	// ReplaceCanonical marks a legacy fragment which collided with a recorded
	// filename. Migration backs it up and replaces the whole file with the
	// modern candidate instead of trying to strip tables in place.
	ReplaceCanonical bool
}

type legacyRootCandidate struct {
	Root   coderoot.Root
	Source string
	Kind   string
}

type legacyMigration struct {
	Roots         map[string]legacyRootCandidate
	Notebooks     map[string]coderoot.Notebook
	Default       string
	Sources       []legacySource
	RootsPath     string
	NotebooksPath string
	SyncPath      string
	SyncStagePath string
	// Compatibility holds migration-window notebook behavior (types,
	// templates and global rules) displaced when a canonical-name legacy
	// fragment is replaced by the root-only recorded schema.
	Compatibility map[string][]byte
}

func parseLegacyFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.EqualFold(filepath.Ext(path), ".toml") {
		if err := toml.Unmarshal(data, out); err != nil {
			return err
		}
	} else if err := yaml.Unmarshal(data, out); err != nil {
		return err
	}
	return nil
}

type canonicalFileShape uint8

const (
	canonicalMissing canonicalFileShape = iota
	canonicalModern
	canonicalLegacy
)

// classifyCanonicalFile is migration-only. Normal loading remains strict: the
// exception exists solely so `grove migrate` can rescue a legacy fragment
// whose basename happens to be a modern recorded target. Modern files win the
// classification first; only an unambiguous frozen-schema marker permits the
// legacy path, and that path is decoded strictly so malformed/unknown input is
// never silently treated as an empty fragment.
func classifyCanonicalFile(path, kind string) (canonicalFileShape, *legacyConfig, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return canonicalMissing, nil, nil
	}
	if err != nil {
		return canonicalMissing, nil, err
	}
	var modernErr error
	switch kind {
	case coderoot.RootsFileName:
		_, modernErr = coderoot.ParseRoots(path, data)
	case coderoot.NotebooksFileName:
		_, modernErr = coderoot.ParseNotebooks(path, data)
	default:
		return canonicalMissing, nil, fmt.Errorf("unknown canonical config kind %q", kind)
	}
	if modernErr == nil {
		return canonicalModern, nil, nil
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return canonicalMissing, nil, modernErr
	}
	if !hasLegacyCanonicalMarker(raw) {
		return canonicalMissing, nil, modernErr
	}
	if err := validateLegacyCanonicalValue("top level", raw, reflect.TypeOf(legacyConfig{})); err != nil {
		return canonicalMissing, nil, fmt.Errorf("%s looks legacy-shaped but is malformed: %w", path, err)
	}
	var legacy legacyConfig
	if err := toml.Unmarshal(data, &legacy); err != nil {
		return canonicalMissing, nil, fmt.Errorf("%s looks legacy-shaped but is malformed: %w", path, err)
	}
	return canonicalLegacy, &legacy, nil
}

func validateLegacyCanonicalValue(scope string, value any, typ reflect.Type) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Map:
		entries, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be a table", scope)
		}
		for key, child := range entries {
			if err := validateLegacyCanonicalValue(scope+"."+key, child, typ.Elem()); err != nil {
				return err
			}
		}
	case reflect.Struct:
		entries, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be a table", scope)
		}
		fields := map[string]reflect.Type{}
		collectLegacyTOMLFields(typ, fields)
		var unknown []string
		for key, child := range entries {
			fieldType, ok := fields[key]
			if !ok {
				unknown = append(unknown, key)
				continue
			}
			if err := validateLegacyCanonicalValue(scope+"."+key, child, fieldType); err != nil {
				return err
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return fmt.Errorf("%s contains unknown field(s): %s", scope, strings.Join(unknown, ", "))
		}
	}
	return nil
}

func collectLegacyTOMLFields(typ reflect.Type, out map[string]reflect.Type) {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("toml")
		name, options, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if field.Anonymous && (name == "" || strings.Contains(options, "inline")) {
			inline := field.Type
			for inline.Kind() == reflect.Pointer {
				inline = inline.Elem()
			}
			if inline.Kind() == reflect.Struct {
				collectLegacyTOMLFields(inline, out)
			}
			continue
		}
		if name == "" {
			name = field.Name
		}
		out[name] = field.Type
	}
}

func hasLegacyCanonicalMarker(raw map[string]any) bool {
	for _, key := range []string{"_grove", "groves", "search_paths"} {
		if _, ok := raw[key]; ok {
			return true
		}
	}
	notebooks, ok := raw["notebooks"].(map[string]any)
	if !ok {
		return false
	}
	_, definitions := notebooks["definitions"]
	_, rules := notebooks["rules"]
	return definitions || rules
}

func resolvedLegacyPath(path string) string {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		return p
	}
	return path
}

func legacyGlobalFiles(dir string) ([]legacySource, error) {
	var out []legacySource
	main := ""
	for _, name := range []string{"grove.toml", "grove.yml", "grove.yaml"} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			main = p
			break
		}
	}
	add := func(path, label string) error {
		var c legacyConfig
		if err := parseLegacyFile(path, &c); err != nil {
			return fmt.Errorf("%s (%s): %w", label, path, err)
		}
		out = append(out, legacySource{Path: path, Resolved: resolvedLegacyPath(path), Label: label, Config: c, TOML: strings.EqualFold(filepath.Ext(path), ".toml"), RemoveGlobal: len(c.Groves) > 0 || len(c.SearchPaths) > 0})
		return nil
	}
	if main != "" {
		if err := add(main, "global"); err != nil {
			return nil, err
		}
	}

	files, _ := filepath.Glob(filepath.Join(dir, "*.toml"))
	type fragment struct {
		path            string
		priority        int
		canonicalLegacy *legacyConfig
	}
	var frags []fragment
	// A pre-cutover fragment could already be named after either canonical
	// target. Classify both explicitly rather than feeding them to coderoot's
	// intentionally strict post-cutover pair loader.
	for _, name := range []string{coderoot.NotebooksFileName, coderoot.RootsFileName} {
		p := filepath.Join(dir, name)
		shape, legacy, err := classifyCanonicalFile(p, name)
		if err != nil {
			return nil, fmt.Errorf("classify canonical collision %s: %w", p, err)
		}
		if shape == canonicalLegacy {
			priority := legacy.GroveMeta.Priority
			if priority == 0 {
				priority = 50
			}
			frags = append(frags, fragment{path: p, priority: priority, canonicalLegacy: legacy})
		}
	}
	for _, p := range files {
		base := filepath.Base(p)
		switch base {
		case "grove.toml", "grove.override.toml", "machine.toml", "sync.toml", "roots.toml", "notebooks.toml":
			continue
		}
		var c legacyConfig
		if err := parseLegacyFile(p, &c); err != nil {
			return nil, fmt.Errorf("fragment %s: %w", p, err)
		}
		priority := c.GroveMeta.Priority
		if priority == 0 {
			priority = 50
		}
		frags = append(frags, fragment{path: p, priority: priority})
	}
	sort.Slice(frags, func(i, j int) bool {
		if frags[i].priority != frags[j].priority {
			return frags[i].priority < frags[j].priority
		}
		return frags[i].path < frags[j].path
	})
	for _, f := range frags {
		if f.canonicalLegacy != nil {
			c := *f.canonicalLegacy
			out = append(out, legacySource{Path: f.path, Resolved: f.path, Label: fmt.Sprintf("canonical-name legacy fragment priority %d", f.priority), Config: c, TOML: true, RemoveGlobal: len(c.Groves) > 0 || len(c.SearchPaths) > 0, ReplaceCanonical: true})
			continue
		}
		if err := add(f.path, fmt.Sprintf("fragment priority %d", f.priority)); err != nil {
			return nil, err
		}
	}
	for _, name := range []string{"grove.override.yml", "grove.override.yaml", "grove.override.toml"} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			if err := add(p, "override"); err != nil {
				return nil, err
			}
			break
		}
	}
	return out, nil
}

func legacyDeadMachineFiles(dir string) ([]legacySource, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "machines"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []legacySource
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".toml" && ext != ".yml" && ext != ".yaml" {
			continue
		}
		p := filepath.Join(dir, "machines", e.Name())
		var c legacyConfig
		if err := parseLegacyFile(p, &c); err != nil {
			return nil, fmt.Errorf("legacy machine %s: %w", p, err)
		}
		out = append(out, legacySource{Path: p, Resolved: resolvedLegacyPath(p), Label: "machines/", Config: c, TOML: strings.EqualFold(filepath.Ext(p), ".toml"), RemoveGlobal: len(c.Groves) > 0 || len(c.SearchPaths) > 0})
	}
	return out, nil
}

func legacySyncRequiresLaterMigration(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	for _, key := range []string{"workspaces", "notebooks"} {
		if v, ok := raw[key]; ok && v != nil {
			return true, nil
		}
	}
	return false, nil
}

// loadModernCanonicalForMigration loads each modern-shaped canonical file
// independently. Pair validation is intentionally deferred until legacy
// candidates have filled a colliding counterpart; each modern file still uses
// coderoot's exact strict parser, and normal coderoot.Load is unchanged.
func loadModernCanonicalForMigration(dir string) (coderoot.Table, error) {
	table := coderoot.Table{Roots: map[string]coderoot.Root{}, Notebooks: map[string]coderoot.Notebook{}}
	rootsPath := filepath.Join(dir, coderoot.RootsFileName)
	shape, _, err := classifyCanonicalFile(rootsPath, coderoot.RootsFileName)
	if err != nil {
		return coderoot.Table{}, err
	}
	if shape == canonicalModern {
		data, err := os.ReadFile(rootsPath)
		if err != nil {
			return coderoot.Table{}, err
		}
		rf, err := coderoot.ParseRoots(rootsPath, data)
		if err != nil {
			return coderoot.Table{}, err
		}
		table.Roots, table.RootsFilePath = rf.Roots, rootsPath
	}
	notebooksPath := filepath.Join(dir, coderoot.NotebooksFileName)
	shape, _, err = classifyCanonicalFile(notebooksPath, coderoot.NotebooksFileName)
	if err != nil {
		return coderoot.Table{}, err
	}
	if shape == canonicalModern {
		data, err := os.ReadFile(notebooksPath)
		if err != nil {
			return coderoot.Table{}, err
		}
		nf, err := coderoot.ParseNotebooks(notebooksPath, data)
		if err != nil {
			return coderoot.Table{}, err
		}
		table.Notebooks, table.Default, table.NotebooksFilePath = nf.Notebooks, nf.Default, notebooksPath
	}
	return table, nil
}

func collectLegacyMigration() (*legacyMigration, error) {
	return collectLegacyMigrationWithOptions(false)
}

func collectLegacyMigrationWithOptions(stageSync bool) (*legacyMigration, error) {
	dir := paths.ConfigDir()
	if dir == "" {
		return nil, fmt.Errorf("cannot resolve the grove config directory")
	}
	syncPath := filepath.Join(dir, "sync.toml")
	blocked, err := legacySyncRequiresLaterMigration(syncPath)
	if err != nil {
		return nil, err
	}
	if blocked && !stageSync {
		return nil, fmt.Errorf("%s contains legacy workspace/notebook sync intent; run `grove migrate --stage-sync` to park an exact copy at %s, complete P1, then use that parked file as P2 input", syncPath, syncPath+".p2-staged")
	}
	if blocked {
		if _, err := os.Stat(syncPath + ".p2-staged"); err == nil {
			return nil, fmt.Errorf("sync staging target %s already exists; preserve or remove it explicitly before retrying", syncPath+".p2-staged")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}

	globals, err := legacyGlobalFiles(dir)
	if err != nil {
		return nil, err
	}
	dead, err := legacyDeadMachineFiles(dir)
	if err != nil {
		return nil, err
	}
	m := &legacyMigration{Roots: map[string]legacyRootCandidate{}, Notebooks: map[string]coderoot.Notebook{}, RootsPath: coderoot.RootsPath(), NotebooksPath: coderoot.NotebooksPath(), Compatibility: map[string][]byte{}}
	if blocked {
		m.SyncPath, m.SyncStagePath = syncPath, syncPath+".p2-staged"
		m.Sources = append(m.Sources, legacySource{Path: syncPath, Resolved: resolvedLegacyPath(syncPath), Label: "sync intent (parked for P2)", TOML: true, RemoveSync: true})
	}
	// Dead files are import-only and lowest precedence. The live global cascade follows.
	all := append(dead, globals...)
	m.Sources = append(m.Sources, all...)
	for _, src := range all {
		if src.ReplaceCanonical && legacyNotebooksHaveCompatibility(src.Config.Notebooks) {
			compatPath := filepath.Join(dir, strings.TrimSuffix(filepath.Base(src.Path), ".toml")+".legacy-compat.toml")
			if _, statErr := os.Stat(compatPath); statErr == nil {
				return nil, fmt.Errorf("canonical collision compatibility target %s already exists; preserve or remove it explicitly before migrating", compatPath)
			} else if !os.IsNotExist(statErr) {
				return nil, statErr
			}
			compat, err := marshalLegacyNotebookCompatibility(src.Config)
			if err != nil {
				return nil, fmt.Errorf("prepare compatibility declarations for %s: %w", src.Path, err)
			}
			m.Compatibility[compatPath] = compat
		}

		for name, nb := range src.Config.Notebooks.Definitions {
			root := nb.RootDir
			if root == "" {
				root = nb.Root
			}
			if root != "" {
				m.Notebooks[name] = coderoot.Notebook{Root: root}
			}
		}
		if src.Config.Notebooks.Rules.Default != "" {
			m.Default = src.Config.Notebooks.Rules.Default
		}
		for name, s := range src.Config.SearchPaths {
			m.Roots[name] = legacyRootCandidate{Root: coderoot.Root{Path: s.Path, Scan: true, Enabled: s.Enabled, Description: s.Description}, Source: src.Label + ": " + src.Path, Kind: "root"}
		}
		for name, g := range src.Config.Groves {
			repos := g.Repos
			if len(repos) == 0 {
				repos = g.IncludeRepos
			}
			exclude := g.Exclude
			if len(exclude) == 0 {
				exclude = g.ExcludeRepos
			}
			scan, kind, notebook := true, "root", g.Notebook
			if config.FindEcosystemManifest(expandPath(g.Path)) != "" {
				scan, kind = false, "ecosystem"
				if notebook == "" {
					cardDefault, manifest, err := legacyCardDefault(g.Path)
					if err != nil {
						return nil, fmt.Errorf("%s [groves.%s]: %w", src.Path, name, err)
					}
					notebook = cardDefault
					if cardDefault != "" {
						m.Sources = appendCardSource(m.Sources, manifest)
					}
				}
			}
			m.Roots[name] = legacyRootCandidate{Root: coderoot.Root{Path: g.Path, Scan: scan, Notebook: notebook, Enabled: g.Enabled, Description: g.Description, Depth: g.Depth, Repos: repos, Exclude: exclude}, Source: src.Label + ": " + src.Path, Kind: kind}
		}
	}

	machinePath := filepath.Join(dir, "machine.toml")
	if st, statErr := os.Stat(machinePath); statErr == nil && !st.IsDir() {
		var mc legacyMachineConfig
		if err := parseLegacyFile(machinePath, &mc); err != nil {
			return nil, fmt.Errorf("machine config %s: %w", machinePath, err)
		}
		src := legacySource{Path: machinePath, Resolved: resolvedLegacyPath(machinePath), Label: "machine", TOML: strings.EqualFold(filepath.Ext(machinePath), ".toml"), RemoveMachine: len(mc.Topology.Ecosystems) > 0 || len(mc.Topology.Roots) > 0}
		m.Sources = append(m.Sources, src)
		for name, e := range mc.Topology.Ecosystems {
			if _, exists := m.Roots[name]; exists {
				continue
			}
			nb := e.Notebook
			if nb == "" {
				card, manifest, err := legacyCardDefault(e.Path)
				if err != nil {
					return nil, fmt.Errorf("%s [machine.ecosystems.%s]: %w", machinePath, name, err)
				}
				nb = card
				if card != "" {
					m.Sources = appendCardSource(m.Sources, manifest)
				}
			}
			m.Roots[name] = legacyRootCandidate{Root: coderoot.Root{Path: e.Path, Scan: false, Notebook: nb, Enabled: e.Enabled, Description: e.Description, Repos: e.Repos, Exclude: e.Exclude}, Source: "machine: " + machinePath, Kind: "ecosystem"}
		}
		for name, r := range mc.Topology.Roots {
			if _, exists := m.Roots[name]; exists {
				continue
			}
			m.Roots[name] = legacyRootCandidate{Root: coderoot.Root{Path: r.Path, Scan: true, Notebook: r.Notebook, Enabled: r.Enabled, Description: r.Description, Depth: r.Depth, Repos: r.Repos, Exclude: r.Exclude}, Source: "machine: " + machinePath, Kind: "root"}
		}
	}

	current, err := loadModernCanonicalForMigration(dir)
	if err != nil {
		return nil, err
	}
	// Successfully parsed modern recorded declarations are authoritative over
	// every frozen source, including a legacy collision in the sibling file.
	for name, nb := range current.Notebooks {
		m.Notebooks[name] = nb
	}
	if current.Default != "" {
		m.Default = current.Default
	}
	for name, r := range current.Roots {
		m.Roots[name] = legacyRootCandidate{Root: r, Source: current.RootsFilePath, Kind: "recorded"}
	}
	for name, c := range m.Roots {
		if c.Root.Notebook == "" {
			c.Root.Notebook = m.Default
			m.Roots[name] = c
		}
		if c.Root.Notebook == "" {
			return nil, fmt.Errorf("%s [root %s]: no notebook binding and no legacy recorded default", c.Source, name)
		}
		if _, ok := m.Notebooks[c.Root.Notebook]; !ok {
			return nil, fmt.Errorf("%s [root %s]: notebook %q has no migrated definition; refusing to infer a path", c.Source, name, c.Root.Notebook)
		}
	}
	if m.Default != "" {
		if _, ok := m.Notebooks[m.Default]; !ok {
			return nil, fmt.Errorf("legacy notebook default %q has no migrated definition", m.Default)
		}
	}
	return m, nil
}

func legacyNotebooksHaveCompatibility(n legacyNotebooks) bool {
	return len(n.Definitions) > 0 || n.Rules.Default != "" || n.Rules.Global != nil
}

func marshalLegacyNotebookCompatibility(c legacyConfig) ([]byte, error) {
	// Keep the original priority so the displaced declarations retain their
	// exact place in the frozen fragment cascade. Recorded notebooks.toml still
	// overrides membership, roots and default during modern compilation.
	compat := struct {
		GroveMeta struct {
			Priority int `toml:"priority"`
		} `toml:"_grove"`
		Notebooks legacyNotebooks `toml:"notebooks"`
	}{Notebooks: c.Notebooks}
	compat.GroveMeta.Priority = c.GroveMeta.Priority
	data, err := toml.Marshal(compat)
	if err != nil {
		return nil, err
	}
	return append([]byte("# Migration-window notebook behavior; notebooks.toml remains authoritative for names, roots, and default.\n"), data...), nil
}

func legacyCardDefault(path string) (string, string, error) {
	manifest := config.FindEcosystemManifest(expandPath(path))
	if manifest == "" {
		return "", "", nil
	}
	var raw struct {
		Ecosystem struct {
			Notebooks map[string]struct {
				Default bool `toml:"default" yaml:"default"`
			} `toml:"notebooks" yaml:"notebooks"`
		} `toml:"ecosystem" yaml:"ecosystem"`
	}
	if err := parseLegacyFile(manifest, &raw); err != nil {
		return "", manifest, err
	}
	var names []string
	for name, nb := range raw.Ecosystem.Notebooks {
		if nb.Default {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > 1 {
		return "", manifest, fmt.Errorf("ecosystem card %s declares multiple default notebooks", manifest)
	}
	if len(names) == 1 {
		return names[0], manifest, nil
	}
	return "", manifest, nil
}

func appendCardSource(sources []legacySource, manifest string) []legacySource {
	resolved := resolvedLegacyPath(manifest)
	for i := range sources {
		p := sources[i].Resolved
		if p == "" {
			p = sources[i].Path
		}
		if p == resolved {
			sources[i].AnnotateCard = true
			return sources
		}
	}
	return append(sources, legacySource{Path: manifest, Resolved: resolved, Label: "ecosystem card default", TOML: strings.EqualFold(filepath.Ext(manifest), ".toml"), AnnotateCard: true})
}

func migrationSourceTargets(sources []legacySource) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range sources {
		if !s.RemoveGlobal && !s.RemoveMachine && !s.RemoveSync && !s.AnnotateCard && !s.ReplaceCanonical {
			continue
		}
		p := s.Resolved
		if p == "" {
			p = s.Path
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func removeLegacyDeclarations(source legacySource) ([]byte, error) {
	path := source.Resolved
	if path == "" {
		path = source.Path
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !source.TOML {
		if source.RemoveGlobal {
			data, err = removeYAMLTopKeys(data, map[string]bool{"groves": true, "search_paths": true})
			if err != nil {
				return nil, err
			}
		}
		if source.AnnotateCard {
			data, err = annotateDeprecatedCardYAML(data)
			if err != nil {
				return nil, err
			}
		}
		return data, nil
	}
	var prefixes []string
	if source.RemoveGlobal {
		prefixes = append(prefixes, "groves", "search_paths")
	}
	if source.RemoveMachine {
		prefixes = append(prefixes, "machine.ecosystems", "machine.roots")
	}
	if source.RemoveSync {
		prefixes = append(prefixes, "workspaces", "notebooks")
	}
	updated, err := removeTOMLPrefixes(data, prefixes)
	if err != nil {
		return nil, err
	}
	if source.AnnotateCard {
		updated, err = annotateDeprecatedCardTOML(updated)
		if err != nil {
			return nil, err
		}
	}
	return updated, nil
}

type (
	tomlRemoval    struct{ start, end int }
	tomlExpression struct {
		start  int
		remove bool
		nested []tomlRemoval
	}
)

// removeTOMLPrefixes uses parsed semantic paths rather than textual header
// guesses, covering tables with comments, dotted assignments, and inline
// tables while preserving unrelated source bytes.
func removeTOMLPrefixes(data []byte, prefixes []string) ([]byte, error) {
	var parser unstable.Parser
	parser.KeepComments = true
	parser.Reset(data)
	var expressions []tomlExpression
	var tablePath []string
	tableRemoved := false
	for parser.NextExpression() {
		n := parser.Expression()
		expr := tomlExpression{start: tomlNodeLineStart(data, n), remove: tableRemoved && n.Kind != unstable.Table && n.Kind != unstable.ArrayTable}
		switch n.Kind {
		case unstable.Table, unstable.ArrayTable:
			tablePath = tomlNodeKey(n)
			tableRemoved = pathHasPrefix(tablePath, prefixes)
			expr.remove = tableRemoved
		case unstable.KeyValue:
			path := append(append([]string{}, tablePath...), tomlNodeKey(n)...)
			if pathHasPrefix(path, prefixes) {
				expr.remove = true
			} else if n.Value().Kind == unstable.InlineTable {
				expr.nested = tomlInlineRemovals(data, n.Value(), path, prefixes)
			}
		}
		expressions = append(expressions, expr)
	}
	if err := parser.Error(); err != nil {
		return nil, err
	}
	var removals []tomlRemoval
	for i, expr := range expressions {
		end := len(data)
		if i+1 < len(expressions) {
			end = expressions[i+1].start
		}
		if expr.remove {
			removals = append(removals, tomlRemoval{expr.start, end})
		} else {
			removals = append(removals, expr.nested...)
		}
	}
	sort.Slice(removals, func(i, j int) bool { return removals[i].start < removals[j].start })
	merged := removals[:0]
	for _, r := range removals {
		if len(merged) > 0 && r.start <= merged[len(merged)-1].end {
			if r.end > merged[len(merged)-1].end {
				merged[len(merged)-1].end = r.end
			}
			continue
		}
		merged = append(merged, r)
	}
	for i := range merged {
		j := merged[i].end
		for j < len(data) && (data[j] == ' ' || data[j] == '\t') {
			j++
		}
		if j < len(data) && data[j] == '}' {
			for merged[i].start > 0 && (data[merged[i].start-1] == ' ' || data[merged[i].start-1] == '\t') {
				merged[i].start--
			}
			if merged[i].start > 0 && data[merged[i].start-1] == ',' {
				merged[i].start--
			}
		}
	}
	for i := len(merged) - 1; i >= 0; i-- {
		r := merged[i]
		data = append(data[:r.start], data[r.end:]...)
	}
	var check map[string]interface{}
	if err := toml.Unmarshal(data, &check); err != nil {
		return nil, fmt.Errorf("validate surgically migrated TOML: %w", err)
	}
	return data, nil
}

func tomlNodeKey(n *unstable.Node) []string {
	var out []string
	it := n.Key()
	for it.Next() {
		out = append(out, string(it.Node().Data))
	}
	return out
}

func tomlNodeLineStart(data []byte, n *unstable.Node) int {
	offset := 0
	if n.Kind == unstable.Comment {
		offset = int(n.Raw.Offset)
	} else if it := n.Key(); it.Next() {
		offset = int(it.Node().Raw.Offset)
	}
	if i := bytes.LastIndexByte(data[:offset], '\n'); i >= 0 {
		return i + 1
	}
	return 0
}

func pathHasPrefix(path []string, prefixes []string) bool {
	joined := strings.Join(path, ".")
	for _, prefix := range prefixes {
		if joined == prefix || strings.HasPrefix(joined, prefix+".") {
			return true
		}
	}
	return false
}

func tomlInlineRemovals(data []byte, table *unstable.Node, base []string, prefixes []string) []tomlRemoval {
	var out []tomlRemoval
	it := table.Children()
	for it.Next() {
		entry := it.Node()
		path := append(append([]string{}, base...), tomlNodeKey(entry)...)
		if pathHasPrefix(path, prefixes) {
			keys := entry.Key()
			keys.Next()
			start, end := int(keys.Node().Raw.Offset), tomlValueEnd(data, entry.Value())
			for end < len(data) && (data[end] == ' ' || data[end] == '\t') {
				end++
			}
			if end < len(data) && data[end] == ',' {
				end++
				for end < len(data) && (data[end] == ' ' || data[end] == '\t') {
					end++
				}
			} else {
				for start > 0 && (data[start-1] == ' ' || data[start-1] == '\t') {
					start--
				}
				if start > 0 && data[start-1] == ',' {
					start--
				}
			}
			out = append(out, tomlRemoval{start, end})
		} else if entry.Value().Kind == unstable.InlineTable {
			out = append(out, tomlInlineRemovals(data, entry.Value(), path, prefixes)...)
		}
	}
	return out
}

func tomlValueEnd(data []byte, value *unstable.Node) int {
	start := int(value.Raw.Offset)
	if value.Kind != unstable.InlineTable && value.Kind != unstable.Array {
		return start + int(value.Raw.Length)
	}
	open, close := byte('{'), byte('}')
	if value.Kind == unstable.Array {
		open, close = '[', ']'
	}
	depth, quote, escaped := 0, byte(0), false
	for i := start; i < len(data); i++ {
		c := data[i]
		if quote != 0 {
			if quote == '"' && c == '\\' && !escaped {
				escaped = true
				continue
			}
			if c == quote && !escaped {
				quote = 0
			}
			escaped = false
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == open {
			depth++
		}
		if c == close {
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(data)
}

const cardDeprecationComment = "DEPRECATED: ecosystem.notebooks is retained only as migration history; roots.toml/notebooks.toml are authoritative."

func annotateDeprecatedCardTOML(data []byte) ([]byte, error) {
	if bytes.Contains(data, []byte(cardDeprecationComment)) {
		return data, nil
	}
	var parser unstable.Parser
	parser.Reset(data)
	var tablePath []string
	for parser.NextExpression() {
		n := parser.Expression()
		var path []string
		switch n.Kind {
		case unstable.Table, unstable.ArrayTable:
			tablePath = tomlNodeKey(n)
			path = tablePath
		case unstable.KeyValue:
			path = append(append([]string{}, tablePath...), tomlNodeKey(n)...)
			if len(path) == 1 && path[0] == "ecosystem" && n.Value().Kind == unstable.InlineTable && tomlInlineContainsKey(n.Value(), "notebooks") {
				path = []string{"ecosystem", "notebooks"}
			}
		}
		if len(path) >= 2 && path[0] == "ecosystem" && path[1] == "notebooks" {
			at := tomlNodeLineStart(data, n)
			comment := []byte("# " + cardDeprecationComment + "\n")
			out := append([]byte{}, data[:at]...)
			out = append(out, comment...)
			out = append(out, data[at:]...)
			return out, nil
		}
	}
	if err := parser.Error(); err != nil {
		return nil, fmt.Errorf("locate ecosystem.notebooks in TOML card: %w", err)
	}
	return nil, fmt.Errorf("ecosystem card supplies a default notebook but its ecosystem.notebooks declaration could not be annotated")
}

func tomlInlineContainsKey(table *unstable.Node, want string) bool {
	it := table.Children()
	for it.Next() {
		entry := it.Node()
		key := tomlNodeKey(entry)
		if len(key) > 0 && key[0] == want {
			return true
		}
	}
	return false
}

func annotateDeprecatedCardYAML(data []byte) ([]byte, error) {
	if bytes.Contains(data, []byte(cardDeprecationComment)) {
		return data, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("locate ecosystem.notebooks in YAML card: %w", err)
	}
	var notebooks *yaml.Node
	if len(doc.Content) > 0 && doc.Content[0].Kind == yaml.MappingNode {
		root := doc.Content[0]
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value != "ecosystem" || root.Content[i+1].Kind != yaml.MappingNode {
				continue
			}
			ecosystem := root.Content[i+1]
			for j := 0; j+1 < len(ecosystem.Content); j += 2 {
				if ecosystem.Content[j].Value == "notebooks" {
					notebooks = ecosystem.Content[j]
					break
				}
			}
		}
	}
	if notebooks == nil || notebooks.Line <= 0 {
		return nil, fmt.Errorf("ecosystem card supplies a default notebook but its ecosystem.notebooks declaration could not be annotated")
	}
	lines := strings.SplitAfter(string(data), "\n")
	line := notebooks.Line - 1
	if line >= len(lines) {
		return nil, fmt.Errorf("ecosystem.notebooks source location is outside the YAML card")
	}
	plain := strings.TrimSuffix(lines[line], "\n")
	indent := len(plain) - len(strings.TrimLeft(plain, " \t"))
	lines[line] = strings.Repeat(" ", indent) + "# " + cardDeprecationComment + "\n" + lines[line]
	return []byte(strings.Join(lines, "")), nil
}

func removeYAMLTopKeys(data []byte, keys map[string]bool) ([]byte, error) {
	// Validate first, then remove only the selected top-level blocks from the
	// original bytes. This keeps comments, quoting, ordering, and unrelated
	// formatting exactly as the operator wrote them.
	var check yaml.Node
	if err := yaml.Unmarshal(data, &check); err != nil {
		return nil, err
	}
	lines := strings.SplitAfter(string(data), "\n")
	out := make([]string, 0, len(lines))
	skip := false
	for _, line := range lines {
		plain := strings.TrimSuffix(line, "\n")
		trimmed := strings.TrimSpace(plain)
		topLevel := plain == strings.TrimLeft(plain, " \t") && trimmed != "" && !strings.HasPrefix(trimmed, "#")
		if topLevel {
			name, _, hasColon := strings.Cut(trimmed, ":")
			name = strings.Trim(strings.TrimSpace(name), "\"'")
			skip = hasColon && keys[name]
		}
		if !skip {
			out = append(out, line)
		}
	}
	return []byte(strings.Join(out, "")), nil
}
