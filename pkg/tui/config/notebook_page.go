package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/theme"

	"github.com/grovetools/grove/pkg/setup"
)

// defaultNotebookName is the definition a fresh save creates when no default
// rule exists yet — the same name the grove setup wizard writes
// (cmd/setup.go's notebook step).
const defaultNotebookName = "personal"

// defaultNotebookRoot is the suggested root shown while nothing is
// configured, in the ~-abbreviated display dialect (the wizard's default,
// cmd/setup.go). Commit expands it.
const defaultNotebookRoot = "~/notebooks"

// notebooksConfig returns the merged [notebooks] section, or nil when absent.
func notebooksConfig(lc *config.LayeredConfig) *config.NotebooksConfig {
	if lc != nil && lc.Final != nil {
		return lc.Final.Notebooks
	}
	return nil
}

// defaultNotebookRule returns the definition name notebooks.rules.default
// points at, or "" when no default rule is configured.
func defaultNotebookRule(lc *config.LayeredConfig) string {
	if nb := notebooksConfig(lc); nb != nil && nb.Rules != nil {
		return nb.Rules.Default
	}
	return ""
}

// notebookRootDir returns a named definition's root_dir, or "".
func notebookRootDir(lc *config.LayeredConfig, name string) string {
	if nb := notebooksConfig(lc); nb != nil {
		if def, ok := nb.Definitions[name]; ok && def != nil {
			return def.RootDir
		}
	}
	return ""
}

// currentNotebookRoot is the root row's Read: the default notebook's
// root_dir (~-abbreviated, like every path the config pages display), or the
// suggested default when no default notebook is configured yet.
func currentNotebookRoot(lc *config.LayeredConfig) string {
	if name := defaultNotebookRule(lc); name != "" {
		if root := notebookRootDir(lc, name); root != "" {
			return setup.AbbreviatePath(root)
		}
	}
	return defaultNotebookRoot
}

// saveNotebookRoot is the root row's Save hook — the first curated setting
// writing outside [tui], and a plural, dynamically targeted write:
//
//	(a) notebooks.definitions.<name>.root_dir = <expanded path>, where
//	    <name> is the CURRENT default rule when one exists (edited in
//	    place — an existing setup is never re-pointed at a new definition),
//	    else "personal";
//	(b) notebooks.rules.default = "personal" — only when no rule existed;
//	(c) mkdir the root, strictly AFTER the config writes: a mkdir failure
//	    surfaces in the status line but can never leave the config
//	    half-written.
func saveNotebookRoot(tomlHandler *setup.TOMLHandler, yamlHandler *setup.YAMLHandler, lc *config.LayeredConfig, value interface{}) error {
	raw, _ := value.(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("notebook location cannot be empty")
	}
	root := setup.ExpandPath(raw)

	name := defaultNotebookRule(lc)
	newDefault := name == ""
	if newDefault {
		name = defaultNotebookName
	}
	if err := SaveGlobalSetting(tomlHandler, yamlHandler, lc, []string{"notebooks", "definitions", name, "root_dir"}, root); err != nil {
		return err
	}
	if newDefault {
		if err := SaveGlobalSetting(tomlHandler, yamlHandler, lc, []string{"notebooks", "rules", "default"}, name); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("saved, but could not create %s: %w", setup.AbbreviatePath(root), err)
	}
	return nil
}

// notebookTargetPreview names the write target honestly: which definition a
// save edits or creates (the no-clobber rule above), and that the directory
// is created.
func notebookTargetPreview(lc *config.LayeredConfig, width int) string {
	t := theme.DefaultTheme
	target := "creates notebook \"" + defaultNotebookName + "\" and makes it your default"
	if name := defaultNotebookRule(lc); name != "" {
		target = "updates your default notebook (\"" + name + "\") in place"
	}
	lines := []string{
		t.Muted.Render("Saving " + target + "; the directory is created if missing."),
		t.Muted.Render("~ expands to your home directory."),
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(strings.Join(lines, "\n"))
}

// NotebookSettings returns the Notebook page's setting descriptors: the
// default notebook's root directory — the one essential onboarding decision
// (spec 23): the notebook is where nb and flow keep notes, plans, and chat
// transcripts for every project — plus a link into the raw config tree for
// the long tail (definitions, rules, path templates). The root row persists
// via Setting.Save (multi-write + mkdir); see saveNotebookRoot for the exact
// no-clobber semantics.
func NotebookSettings() []Setting {
	return []Setting{
		{
			ID:          "notebook_root",
			Label:       "Notebook location",
			Description: "Directory where your notes, plans, and chat transcripts live — nb and flow keep everything under this root",
			Essential:   true,
			Control:     ControlText,
			Read:        currentNotebookRoot,
			PreviewFn:   notebookTargetPreview,
			Save:        saveNotebookRoot,
		},
		{
			ID:          "notebooks_data_link",
			Label:       "All notebook settings",
			Description: "Definitions, rules, and path templates in the raw config tree",
			Control:     ControlLink,
			Options:     []string{"data"},
		},
	}
}
