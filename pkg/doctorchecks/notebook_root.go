package doctorchecks

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/doctor"
	"golang.org/x/sys/unix"
)

func init() {
	doctor.Register(&notebookRootCheck{})
}

// notebookRootCheck resolves configured notebook roots from the merged config
// and verifies each one exists and is writable. It skips gracefully when no
// notebooks (or no root_dir) are configured, and never creates anything.
type notebookRootCheck struct{}

func (c *notebookRootCheck) ID() string   { return "notebook_root_writable" }
func (c *notebookRootCheck) Name() string { return "notebook root directories writable" }

func (c *notebookRootCheck) Run(ctx context.Context, opts doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}

	startDir, err := os.Getwd()
	if err != nil {
		res.Status = doctor.StatusWarn
		res.Message = "could not determine working directory; skipping notebook root check"
		res.Error = err.Error()
		return res
	}

	layered, err := config.LoadLayered(startDir)
	if err != nil || layered == nil || layered.Final == nil {
		res.Status = doctor.StatusWarn
		res.Message = "could not load merged config; skipping notebook root check"
		if err != nil {
			res.Error = compactError(err)
		}
		return res
	}

	cfg := layered.Final
	if cfg.Notebooks == nil || len(cfg.Notebooks.Definitions) == 0 {
		res.Status = doctor.StatusOK
		res.Message = "no notebooks configured; skipping notebook root check"
		return res
	}

	names := make([]string, 0, len(cfg.Notebooks.Definitions))
	for name := range cfg.Notebooks.Definitions {
		names = append(names, name)
	}
	sort.Strings(names)

	var problems []string
	rootsChecked := 0
	for _, name := range names {
		nb := cfg.Notebooks.Definitions[name]
		if nb == nil || strings.TrimSpace(nb.RootDir) == "" {
			continue // distributed-mode notebook without a central root
		}
		rootsChecked++
		root := expandUserPath(nb.RootDir)

		info, err := os.Stat(root)
		if err != nil {
			problems = append(problems, fmt.Sprintf("notebook %q root %s: %v", name, root, err))
			continue
		}
		if !info.IsDir() {
			problems = append(problems, fmt.Sprintf("notebook %q root %s is not a directory", name, root))
			continue
		}
		if err := unix.Access(root, unix.W_OK); err != nil {
			problems = append(problems, fmt.Sprintf("notebook %q root %s is not writable: %v", name, root, err))
		}
	}

	if rootsChecked == 0 {
		res.Status = doctor.StatusOK
		res.Message = "notebooks configured without a root_dir; skipping notebook root check"
		return res
	}

	if len(problems) > 0 {
		res.Status = doctor.StatusFail
		res.Message = fmt.Sprintf("%d of %d notebook root(s) missing or not writable", len(problems), rootsChecked)
		res.Error = strings.Join(problems, "; ")
		res.Resolution = "create the directory or fix its permissions (doctor will not create it for you)"
		return res
	}

	res.Status = doctor.StatusOK
	res.Message = fmt.Sprintf("%d notebook root(s) present and writable", rootsChecked)
	return res
}

func (c *notebookRootCheck) AutoFix(ctx context.Context) error {
	return fmt.Errorf("%w: create the notebook root directory manually", doctor.ErrNotFixable)
}
