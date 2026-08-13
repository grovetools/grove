package doctorchecks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/doctor"
	"github.com/grovetools/core/pkg/notespace"
)

func init() { doctor.Register(&notespaceIdentityCheck{}) }

type notespaceIdentityCheck struct{}

func (*notespaceIdentityCheck) ID() string { return "notespace_identity" }
func (*notespaceIdentityCheck) Name() string {
	return "notespace layout, identity, primary and registry bindings"
}

func (c *notespaceIdentityCheck) Run(ctx context.Context, opts doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}
	table, err := coderoot.Load()
	if err != nil {
		res.Status = doctor.StatusFail
		res.Message = "recorded notebook topology is invalid"
		res.Error = compactError(err)
		return res
	}
	machine, err := config.LoadMachineConfig()
	if err != nil {
		res.Status = doctor.StatusFail
		res.Message = "machine identity config is invalid"
		res.Error = compactError(err)
		return res
	}
	if machine == nil {
		machine = &config.MachineConfig{}
	}
	var failures, warnings []string
	idRoots := map[string][]string{}
	subjectRoots := map[string][]string{}
	ids := map[string]bool{}
	localModes := 0
	for _, name := range table.SortedNotebookNames() {
		root := table.NotebookRoot(name)
		if info, err := os.Stat(filepath.Join(root, ".notebook")); err == nil && info.IsDir() {
			localModes++
			warnings = append(warnings, fmt.Sprintf("notebook %q is local .notebook mode and intentionally unsynced", name))
			continue
		}
		if info, err := os.Stat(filepath.Join(root, "workspaces")); err == nil && info.IsDir() {
			failures = append(failures, fmt.Sprintf("notebook %q retains legacy workspaces/; run `grove migrate --step 2`", name))
		}
		nbStamp, err := notespace.LoadNotebook(root)
		if err != nil {
			failures = append(failures, err.Error())
		} else if nbStamp == nil {
			failures = append(failures, fmt.Sprintf("notebook %q has no .notebook.toml", name))
		}
		dir := filepath.Join(root, "notespaces")
		entries, err := os.ReadDir(dir)
		if err != nil {
			failures = append(failures, fmt.Sprintf("notebook %q notespaces: %v", name, err))
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			spaceRoot := filepath.Join(dir, entry.Name())
			stamp, err := notespace.LoadNotespace(spaceRoot)
			if err != nil {
				failures = append(failures, err.Error())
				continue
			}
			if stamp == nil {
				failures = append(failures, fmt.Sprintf("orphan notespace directory without stamp: %s", spaceRoot))
				continue
			}
			ids[stamp.ID] = true
			idRoots[stamp.ID] = append(idRoots[stamp.ID], spaceRoot)
			subjectRoots[stamp.Subject] = append(subjectRoots[stamp.Subject], spaceRoot)
		}
	}
	for id, roots := range idRoots {
		if len(roots) != 1 {
			sort.Strings(roots)
			failures = append(failures, fmt.Sprintf("notespace id %s has %d physical roots: %s", id, len(roots), strings.Join(roots, ", ")))
		}
	}
	for subj, roots := range subjectRoots {
		primary := machine.Primaries[subj]
		if primary == "" {
			failures = append(failures, fmt.Sprintf("subject %s has local notes but no [primaries] entry", subj))
			continue
		}
		if !ids[primary] {
			failures = append(failures, fmt.Sprintf("subject %s primary %s has no local stamp", subj, primary))
		}
		count := 0
		for _, root := range roots {
			stamp, _ := notespace.LoadNotespace(root)
			if stamp != nil && stamp.ID == primary {
				count++
			}
		}
		if count != 1 {
			failures = append(failures, fmt.Sprintf("subject %s primary %s resolves to %d local roots", subj, primary, count))
		}
	}
	for path, subj := range machine.Subjects {
		if filepath.Clean(path) != path || !filepath.IsAbs(path) {
			failures = append(failures, "noncanonical [subjects] path: "+path)
		} else if info, err := os.Stat(path); err != nil || !info.IsDir() {
			failures = append(failures, "[subjects] path is absent: "+path)
		}
		if len(subjectRoots[subj]) == 0 {
			warnings = append(warnings, fmt.Sprintf("local subject %s has no notes on this machine", subj))
		}
	}
	configEntries, _ := os.ReadDir(filepath.Dir(config.MachineConfigPath()))
	for _, entry := range configEntries {
		if entry.IsDir() {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(filepath.Dir(config.MachineConfigPath()), entry.Name()))
		if readErr != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "template") && strings.Contains(line, "workspaces/") {
				failures = append(failures, fmt.Sprintf("%s pins old workspaces/ layout in template: %s", entry.Name(), strings.TrimSpace(line)))
			}
		}
	}
	if machine.Sync.Registry != nil {
		if !ids[machine.Sync.Registry.NotespaceID] {
			failures = append(failures, fmt.Sprintf("[sync.registry] id %s has no local binding", machine.Sync.Registry.NotespaceID))
		}
		if table.NotebookRoot(machine.Sync.Registry.Notebook) == "" {
			failures = append(failures, fmt.Sprintf("[sync.registry] notebook %q is not recorded", machine.Sync.Registry.Notebook))
		}
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		res.Status = doctor.StatusFail
		res.Message = fmt.Sprintf("%d notespace identity/layout invariant(s) failed", len(failures))
		res.Error = strings.Join(failures, "; ")
		res.Resolution = "run `grove migrate --step 2 --dry-run` with explicit roots; do not hand-edit ids. A duplicate id is repaired by designating the losing root: `grove doctor --fix --remint <notespace-root>`."
		return res
	}
	if len(warnings) > 0 {
		sort.Strings(warnings)
		res.Status = doctor.StatusWarn
		res.Message = fmt.Sprintf("identity topology is consistent with %d advisory item(s)", len(warnings))
		res.Error = strings.Join(warnings, "; ")
		return res
	}
	res.Status = doctor.StatusOK
	res.Message = fmt.Sprintf("%d unique notespace id(s), primaries and registry bindings are consistent (%d local-mode notebooks)", len(ids), localModes)
	return res
}

func (*notespaceIdentityCheck) AutoFix(context.Context) error {
	return fmt.Errorf("%w: only an explicitly selected duplicate-id losing root may be reminted", doctor.ErrNotFixable)
}
