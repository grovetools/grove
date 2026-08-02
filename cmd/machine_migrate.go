package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/paths"
)

// migrateSource is one [groves.*] declaration found in the config cascade,
// carried with the file that declared it so the report can name a real path
// (which matters: on a dotfiles setup the config-dir entry is a symlink and
// the edit lands in the dotfiles repo).
type migrateSource struct {
	Name      string
	Grove     config.GroveSourceConfig
	File      string // path as the loader saw it
	Resolved  string // symlinks resolved — the file actually written
	Layer     string
	Ecosystem bool // classified as an ecosystem subscription rather than a bare root
	Reason    string
}

func newMachineMigrateCmd() *cobra.Command {
	var dryRun bool
	var annotate bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Move [groves.*] declarations into machine.toml",
		Long: `Rewrite legacy [groves.*] entries as machine.toml subscriptions.

Each grove becomes either an ecosystem subscription or a bare scan root:

  [machine.ecosystems.<name>]  the directory carries a grove manifest, so it is
                               an ecosystem this machine subscribes to — the
                               thing 'grove ecosystem materialize' can rebuild
                               on another host.
  [machine.roots.<name>]       a plain directory of repos. First-class, but
                               nothing can materialize it, so it is never
                               reported as "declared but missing".

This is SAFE TO RUN TWICE and changes no behavior on its own. machine.toml
compiles into the same cfg.Groves map the legacy entries produce, and
compilation fills only ABSENT keys — so while the old entries remain they keep
winning, byte for byte. Delete them when you are ready; the subscriptions take
over.

The dead ~/.config/grove/machines/ directory is imported too, if present.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMachineMigrate(cmd.OutOrStdout(), dryRun, annotate)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Report what would change without writing anything")
	cmd.Flags().BoolVar(&annotate, "annotate", true, "Add a deprecation comment above each migrated [groves.*] entry in its original file")
	return cmd
}

func runMachineMigrate(out io.Writer, dryRun, annotate bool) error {
	cfgPath := config.MachineConfigPath()
	if cfgPath == "" {
		return fmt.Errorf("cannot resolve the grove config directory")
	}

	sources, err := collectGroveSources()
	if err != nil {
		return err
	}
	legacy, err := collectLegacyMachinesGroves()
	if err != nil {
		return err
	}
	sources = append(sources, legacy...)

	if len(sources) == 0 {
		fmt.Fprintf(out, "No [groves.*] entries found; nothing to migrate.\n")
		return nil
	}

	// Existing subscriptions are never overwritten: migrate is a one-way
	// import, not a re-sync of hand-edited intent.
	existing, err := config.LoadMachineConfigFrom(cfgPath)
	if err != nil {
		return err
	}

	subs := config.MachineSubscriptions{
		Ecosystems: map[string]config.MachineEcosystem{},
		Roots:      map[string]config.MachineRoot{},
		Header: []string{
			"# This machine's intent: name, ecosystem subscriptions, bare scan roots.",
			"# Written by `grove machine migrate`. Dotfiles-portable on purpose — a",
			"# restored copy plus a freshly minted machine id is a NEW machine with the",
			"# SAME intent, which is the supported fast path.",
			"",
		},
	}
	var skipped []string
	for _, src := range sources {
		if existing != nil {
			if _, ok := existing.Machine.Ecosystems[src.Name]; ok {
				skipped = append(skipped, fmt.Sprintf("%s (already declared in machine.toml)", src.Name))
				continue
			}
			if _, ok := existing.Machine.Roots[src.Name]; ok {
				skipped = append(skipped, fmt.Sprintf("%s (already declared in machine.toml)", src.Name))
				continue
			}
		}
		if _, dup := subs.Ecosystems[src.Name]; dup {
			continue
		}
		if _, dup := subs.Roots[src.Name]; dup {
			continue
		}
		if src.Ecosystem {
			subs.Ecosystems[src.Name] = config.MachineEcosystem{
				Path:        src.Grove.Path,
				Notebook:    src.Grove.Notebook,
				Description: src.Grove.Description,
				Enabled:     disabledOnly(src.Grove.Enabled),
			}
		} else {
			subs.Roots[src.Name] = config.MachineRoot{
				Path:        src.Grove.Path,
				Notebook:    src.Grove.Notebook,
				Description: src.Grove.Description,
				Enabled:     disabledOnly(src.Grove.Enabled),
			}
		}
	}

	fmt.Fprintf(out, "Migrating %d grove declaration(s) into %s\n\n", len(subs.Ecosystems)+len(subs.Roots), cfgPath)
	for _, src := range sources {
		kind := "root      "
		if src.Ecosystem {
			kind = "ecosystem "
		}
		fmt.Fprintf(out, "  %s %-18s %-36s [%s: %s]\n", kind, src.Name, src.Grove.Path, src.Layer, displayPath(src))
		if src.Reason != "" {
			fmt.Fprintf(out, "              %s\n", src.Reason)
		}
	}
	for _, s := range skipped {
		fmt.Fprintf(out, "  skipped   %s\n", s)
	}

	if dryRun {
		fmt.Fprintf(out, "\n--dry-run: nothing was written.\n")
		return nil
	}

	changed, err := config.WriteMachineSubscriptions(cfgPath, subs)
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintf(out, "\n✓ wrote %s\n", cfgPath)
	} else {
		fmt.Fprintf(out, "\n· %s already up to date\n", cfgPath)
	}

	if annotate {
		annotated, err := annotateMigratedSources(out, sources)
		if err != nil {
			return err
		}
		if annotated == 0 {
			fmt.Fprintf(out, "· original [groves.*] entries already carry the deprecation note\n")
		}
	}

	fmt.Fprintf(out, "\nThe original [groves.*] entries still WIN (compilation fills absent keys only),\n")
	fmt.Fprintf(out, "so behavior is unchanged until you delete them. Verify with `grove machine status`.\n")
	return nil
}

// disabledOnly carries an explicit `enabled = false` through the migration and
// drops a redundant `enabled = true` — the default — so the new file states
// intent rather than echoing boilerplate.
func disabledOnly(enabled *bool) *bool {
	if enabled != nil && !*enabled {
		v := false
		return &v
	}
	return nil
}

func displayPath(src migrateSource) string {
	if src.Resolved != "" && src.Resolved != src.File {
		return fmt.Sprintf("%s → %s", src.File, src.Resolved)
	}
	return src.File
}

// collectGroveSources enumerates every [groves.*] declaration in the global
// layers, attributed to the file that declared it. Project-layer groves are
// deliberately out of scope: machine.toml is this MACHINE's intent, and a
// repo-local grove declaration belongs to the repo.
func collectGroveSources() ([]migrateSource, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	layered, err := config.LoadLayered(cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to load config layers: %w", err)
	}

	var out []migrateSource
	add := func(layer, path string, groves map[string]config.GroveSourceConfig) {
		for _, name := range sortedGroveNames(groves) {
			out = append(out, classifyGroveSource(name, groves[name], layer, path))
		}
	}

	if layered.Global != nil {
		add("global", layered.FilePaths[config.SourceGlobal], layered.Global.Groves)
	}
	for _, frag := range layered.GlobalFragments {
		if frag.Config != nil {
			add("fragment", frag.Path, frag.Config.Groves)
		}
	}
	if layered.GlobalOverride != nil && layered.GlobalOverride.Config != nil {
		add("override", layered.GlobalOverride.Path, layered.GlobalOverride.Config.Groves)
	}
	return out, nil
}

// collectLegacyMachinesGroves imports ~/.config/grove/machines/. The directory
// is never loaded by the config cascade (LoadFromWithLogger only warns about
// it), so whatever groves it declares are invisible today — importing them is
// the whole reason the warning names this command. Files there are full grove
// configs in either dialect; the incumbent on the author's machine is YAML.
func collectLegacyMachinesGroves() ([]migrateSource, error) {
	configDir := paths.ConfigDir()
	if configDir == "" {
		return nil, nil
	}
	dir := filepath.Join(configDir, config.LegacyMachinesDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil // absent is the normal case
	}

	var out []migrateSource
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		switch strings.ToLower(filepath.Ext(name)) {
		case ".yml", ".yaml", ".toml":
		default:
			continue
		}
		path := filepath.Join(dir, name)
		groves, err := grovesFromFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read legacy machine config %s: %w", path, err)
		}
		for _, groveName := range sortedGroveNames(groves) {
			src := classifyGroveSource(groveName, groves[groveName], "machines/", path)
			out = append(out, src)
		}
	}
	return out, nil
}

// grovesFromFile parses just the groves map out of a standalone config file,
// in either dialect. It decodes directly rather than going through
// config.Load: the bytes loaders compile machine.toml into whatever they
// parse, so a Load here would hand back this machine's OWN subscriptions and
// migrate would import them as if the legacy file had declared them.
func grovesFromFile(path string) (map[string]config.GroveSourceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg config.Config
	if strings.EqualFold(filepath.Ext(path), ".toml") {
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
	} else {
		// Config.UnmarshalYAML maps the legacy search_paths spelling onto
		// Groves for us.
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
	}
	return cfg.Groves, nil
}

// classifyGroveSource decides whether a grove is an ecosystem subscription or
// a bare scan root. The evidence is the directory itself: an ecosystem carries
// a grove manifest. An unreadable or absent path stays a root — the
// conservative choice, since a root is inert while an ecosystem subscription
// becomes materialize's input.
func classifyGroveSource(name string, grove config.GroveSourceConfig, layer, file string) migrateSource {
	src := migrateSource{Name: name, Grove: grove, File: file, Layer: layer}
	if resolved, err := filepath.EvalSymlinks(file); err == nil {
		src.Resolved = resolved
	}

	path := expandPath(grove.Path)
	info, err := os.Stat(path)
	switch {
	case err != nil:
		src.Reason = "path is not present on this machine; imported as a bare root"
	case !info.IsDir():
		src.Reason = "path is not a directory; imported as a bare root"
	case config.FindEcosystemManifest(path) != "":
		src.Ecosystem = true
	default:
		src.Reason = "no grove manifest at the path; imported as a bare root"
	}
	return src
}

func sortedGroveNames(groves map[string]config.GroveSourceConfig) []string {
	names := make([]string, 0, len(groves))
	for name := range groves {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// annotateMigratedSources writes a deprecation comment above each migrated
// [groves.*] table (TOML) or the groves: key (YAML) in its original file. The
// comment is the only edit — the entries stay live, because deleting a
// declaration the user still depends on is theirs to do, not ours.
func annotateMigratedSources(out io.Writer, sources []migrateSource) (int, error) {
	byFile := map[string][]migrateSource{}
	var order []string
	for _, src := range sources {
		target := src.File
		if src.Resolved != "" {
			target = src.Resolved
		}
		if _, seen := byFile[target]; !seen {
			order = append(order, target)
		}
		byFile[target] = append(byFile[target], src)
	}

	count := 0
	for _, file := range order {
		data, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(out, "! could not annotate %s: %v\n", file, err)
			continue
		}
		var updated string
		if strings.HasSuffix(file, ".toml") {
			updated = annotateTOMLGroves(string(data), byFile[file])
		} else {
			updated = annotateYAMLGroves(string(data))
		}
		if updated == string(data) {
			continue
		}
		info, err := os.Stat(file)
		mode := os.FileMode(0o644)
		if err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(file, []byte(updated), mode); err != nil {
			return count, fmt.Errorf("failed to annotate %s: %w", file, err)
		}
		count++
		fmt.Fprintf(out, "✓ annotated %s\n", file)
	}
	return count, nil
}

const groveDeprecationNote = "# DEPRECATED: migrated to machine.toml by `grove machine migrate`."

const groveDeprecationHint = "# Still authoritative while it exists (machine.toml fills absent keys only); delete when ready."

// annotateTOMLGroves inserts the deprecation note above each migrated
// [groves.<name>] header it has not already annotated.
func annotateTOMLGroves(content string, sources []migrateSource) string {
	wanted := map[string]bool{}
	for _, src := range sources {
		wanted["groves."+src.Name] = true
	}

	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines)+2*len(wanted))
	for i, line := range lines {
		key, ok := tomlHeaderKey(line)
		if ok && wanted[key] && !alreadyAnnotated(lines, i) {
			out = append(out, groveDeprecationNote, groveDeprecationHint)
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// annotateYAMLGroves inserts the note above the top-level `groves:` key. YAML
// groves are a single mapping, so the note lands once for the whole block.
func annotateYAMLGroves(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines)+2)
	for i, line := range lines {
		if isYAMLGrovesKey(line) && !alreadyAnnotated(lines, i) {
			out = append(out, groveDeprecationNote, groveDeprecationHint)
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func alreadyAnnotated(lines []string, i int) bool {
	for j := i - 1; j >= 0; j-- {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "#") {
			return false
		}
		if strings.Contains(trimmed, "grove machine migrate") {
			return true
		}
	}
	return false
}

// tomlHeaderKey extracts the dotted key of a TOML table header line, or
// reports false for any other line.
func tomlHeaderKey(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
		return "", false
	}
	key := strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
	key = strings.TrimSuffix(strings.TrimPrefix(key, "["), "]")
	key = strings.ReplaceAll(key, "\"", "")
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false
	}
	return key, true
}

func isYAMLGrovesKey(line string) bool {
	if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
		return false
	}
	name, _, ok := strings.Cut(line, ":")
	if !ok {
		return false
	}
	switch strings.TrimSpace(strings.ReplaceAll(name, "\"", "")) {
	case "groves", "search_paths":
		return true
	}
	return false
}
