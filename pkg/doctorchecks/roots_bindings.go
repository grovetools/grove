package doctorchecks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/doctor"
)

func init() {
	doctor.Register(&rootsBindingsCheck{})
}

// pathState keeps the two useful views of a recorded path together. Expanded
// is the path operators would reach; Canonical additionally resolves symlinks
// when the target exists and is used for comparisons across doctor checks.
type pathState struct {
	Expanded  string
	Canonical string
	Exists    bool
}

func inspectPath(path string) pathState {
	expanded := expandUserPath(path)
	if abs, err := filepath.Abs(expanded); err == nil {
		expanded = abs
	}
	canonicalPath := expanded
	if real, err := filepath.EvalSymlinks(expanded); err == nil {
		canonicalPath = real
	}
	info, err := os.Stat(expanded)
	return pathState{Expanded: expanded, Canonical: canonicalPath, Exists: err == nil && info.IsDir()}
}

// canonical is shared with machine_intent so both checks compare paths using
// exactly the same expansion, absolute-path, and symlink policy.
func canonical(path string) string { return inspectPath(path).Canonical }

type rootBinding struct {
	Name         string
	DeclaredPath string
	Path         pathState
	Kind         string
	Enabled      bool
	State        string
	Notebook     string
	NotebookRoot pathState
}

func inspectRootBindings(table coderoot.Table) []rootBinding {
	out := make([]rootBinding, 0, len(table.Roots))
	for _, name := range table.SortedRootNames() {
		root := table.Roots[name]
		enabled := root.Enabled == nil || *root.Enabled
		path := inspectPath(root.Path)
		state := "present"
		switch {
		case !path.Exists && !enabled:
			state = "explicitly-missing"
		case !path.Exists:
			state = "missing"
		case !enabled:
			state = "disabled-present"
		}
		kind := "specific"
		if root.Scan {
			kind = "scan"
		}
		notebook := table.RootNotebook(name)
		nb := table.Notebooks[notebook]
		out = append(out, rootBinding{
			Name: name, DeclaredPath: root.Path, Path: path, Kind: kind,
			Enabled: enabled, State: state, Notebook: notebook,
			NotebookRoot: inspectPath(nb.Root),
		})
	}
	return out
}

func formatRootBinding(binding rootBinding) string {
	return fmt.Sprintf("%s: declared=%q expanded=%q canonical=%q kind=%s enabled=%t state=%s notebook=%q notebook_root=%q notebook_canonical=%q",
		binding.Name, binding.DeclaredPath, binding.Path.Expanded, binding.Path.Canonical,
		binding.Kind, binding.Enabled, binding.State, binding.Notebook,
		binding.NotebookRoot.Expanded, binding.NotebookRoot.Canonical)
}

// notebookEmbeddedConfigs enumerates the files that can participate in the
// deprecated project-notebook config layer (config.SourceProjectNotebook).
// The loader checks one workspace directory at a time; doctor deliberately
// inventories every recorded notebook so stale fragments cannot hide outside
// the current working directory.
func notebookEmbeddedConfigs(table coderoot.Table) []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range table.SortedNotebookNames() {
		root := table.NotebookRoot(name)
		entries, err := os.ReadDir(filepath.Join(root, "workspaces"))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			for _, filename := range []string{"grove.toml", "grove.yml", "grove.yaml"} {
				path := filepath.Join(root, "workspaces", entry.Name(), filename)
				info, err := os.Stat(path)
				if err != nil || info.IsDir() || seen[path] {
					continue
				}
				seen[path] = true
				out = append(out, fmt.Sprintf("notebook %q: %s", name, path))
			}
		}
	}
	sort.Strings(out)
	return out
}

type rootsBindingsCheck struct{}

func (c *rootsBindingsCheck) ID() string   { return "roots_bindings" }
func (c *rootsBindingsCheck) Name() string { return "recorded roots resolve to notebooks and paths" }

func (c *rootsBindingsCheck) Run(ctx context.Context, opts doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}
	table, err := coderoot.Load()
	if err != nil {
		res.Status = doctor.StatusFail
		res.Message = "recorded roots/notebooks failed strict parsing or binding validation"
		res.Error = compactError(err)
		res.Resolution = "fix the named table in roots.toml or notebooks.toml; duplicate paths and unresolved notebook/default names are not allowed"
		return res
	}

	bindings := inspectRootBindings(table)
	inventory := make([]string, 0, len(bindings))
	var missing []string
	for _, binding := range bindings {
		inventory = append(inventory, formatRootBinding(binding))
		if binding.State == "missing" {
			missing = append(missing, binding.Name+" at "+binding.Path.Expanded)
		}
	}
	embedded := notebookEmbeddedConfigs(table)
	detail := ""
	if len(inventory) > 0 {
		detail = "; bindings: " + strings.Join(inventory, "; ")
	}

	switch {
	case len(missing) > 0:
		res.Status = doctor.StatusFail
		res.Message = fmt.Sprintf("%d of %d recorded root(s) are absent while enabled%s", len(missing), len(bindings), detail)
		res.Error = "missing enabled roots: " + strings.Join(missing, "; ")
		if len(embedded) > 0 {
			res.Error += "; deprecated project-notebook fragments: " + strings.Join(embedded, "; ")
		}
		res.Resolution = "create/materialize each root, or explicitly set enabled = false to record that it is intentionally missing"
	case len(embedded) > 0:
		res.Status = doctor.StatusWarn
		res.Message = fmt.Sprintf("%d recorded root binding(s) resolved%s; found %d deprecated project-notebook config fragment(s)", len(bindings), detail, len(embedded))
		res.Error = strings.Join(embedded, "; ")
		res.Resolution = "move settings out of notebook workspaces/*/grove.toml (or grove.yml/grove.yaml); that project-notebook layer is deprecated"
	default:
		res.Status = doctor.StatusOK
		if len(bindings) == 0 {
			res.Message = "no recorded roots; roots/notebooks files parsed and bound cleanly"
		} else {
			res.Message = fmt.Sprintf("%d recorded root binding(s) resolved%s", len(bindings), detail)
		}
	}
	return res
}

func (c *rootsBindingsCheck) AutoFix(ctx context.Context) error {
	return fmt.Errorf("%w: path presence and notebook routing require operator intent", doctor.ErrNotFixable)
}
