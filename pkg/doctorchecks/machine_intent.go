package doctorchecks

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/doctor"
	"github.com/grovetools/core/pkg/workspace"

	"github.com/sirupsen/logrus"
)

func init() {
	doctor.Register(&machineIntentCheck{})
}

// machineIntentCheck reconciles specific recorded roots against what is
// actually on disk, in both directions:
//
//	declared but missing    a subscription with nothing at its path — the
//	                        input to `grove ecosystem materialize`.
//	present but undeclared  an ecosystem grove discovers but that no
//	                        subscription names, so another machine restoring
//	                        these dotfiles would not get it.
//
// REPORT-ONLY, by design. Neither direction is unambiguously wrong: a missing
// subscription may be a machine you have not materialized yet, and an
// undeclared ecosystem may be a scratch clone you never want to travel. The
// check names them; the operator decides.
type machineIntentCheck struct{}

func (c *machineIntentCheck) ID() string   { return "machine_intent" }
func (c *machineIntentCheck) Name() string { return "recorded code roots match what is on disk" }

func (c *machineIntentCheck) Run(ctx context.Context, opts doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}

	table, err := coderoot.Load()
	if err != nil {
		res.Status = doctor.StatusFail
		res.Message = "recorded roots/notebooks are unreadable"
		res.Error = compactError(err)
		res.Resolution = "fix roots.toml and notebooks.toml (or run `grove migrate` for legacy config)"
		return res
	}

	states := config.ReconcileCodeRoots(table)
	if len(states) == 0 {
		res.Status = doctor.StatusOK
		res.Message = "no specific code roots declared; nothing to reconcile"
		return res
	}

	var missing []string
	declared := map[string]bool{}
	for _, st := range states {
		declared[canonical(st.Path)] = true
		if !st.Enabled {
			continue
		}
		switch st.State {
		case "declared-missing":
			missing = append(missing, fmt.Sprintf("%s (declared at %s, nothing there)", st.Name, st.Path))
		case "unmanifested":
			missing = append(missing, fmt.Sprintf("%s (%s exists but carries no grove manifest)", st.Name, st.Path))
		}
	}

	undeclared := undeclaredEcosystems(declared)

	switch {
	case len(missing) > 0 && len(undeclared) > 0:
		res.Status = doctor.StatusWarn
		res.Message = fmt.Sprintf("%d declared-but-missing, %d present-but-undeclared", len(missing), len(undeclared))
		res.Error = strings.Join(append(missing, undeclared...), "; ")
		res.Resolution = "materialize or drop the missing roots; declare undeclared ecosystems in roots.toml"
	case len(missing) > 0:
		res.Status = doctor.StatusWarn
		res.Message = fmt.Sprintf("%d subscription(s) declared but missing on this machine", len(missing))
		res.Error = strings.Join(missing, "; ")
		res.Resolution = "materialize each one, or remove the entry from roots.toml if this machine should not have it"
	case len(undeclared) > 0:
		res.Status = doctor.StatusWarn
		res.Message = fmt.Sprintf("%d discovered ecosystem(s) are not declared in roots.toml", len(undeclared))
		res.Error = strings.Join(undeclared, "; ")
		res.Resolution = "add them under [roots.*] so a restored machine gets them too"
	default:
		res.Status = doctor.StatusOK
		res.Message = fmt.Sprintf("%d subscription(s) present, none undeclared", len(states))
	}
	return res
}

func (c *machineIntentCheck) AutoFix(ctx context.Context) error {
	// Deliberately not fixable: which direction to reconcile is intent, and
	// materializing an ecosystem clones from the network.
	return fmt.Errorf("%w: run `grove machine status` and decide per entry (`grove migrate` imports legacy config)", doctor.ErrNotFixable)
}

// undeclaredEcosystems lists discovered ecosystems whose path no subscription
// names. Discovery is the same walk every grove surface uses, so this answers
// "grove can see it, but a restored machine would not".
func undeclaredEcosystems(declared map[string]bool) []string {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	result, err := workspace.NewDiscoveryService(logger).DiscoverAll()
	if err != nil || result == nil {
		return nil
	}
	var out []string
	for _, eco := range result.Ecosystems {
		if declared[canonical(eco.Path)] {
			continue
		}
		out = append(out, fmt.Sprintf("%s at %s is discoverable but undeclared", eco.Name, eco.Path))
	}
	sort.Strings(out)
	return out
}

// canonical normalizes a path for comparison the way isEcosystemDiscoverable
// does in grove/cmd: absolute, with symlinks resolved (macOS /var →
// /private/var would otherwise make every comparison miss).
func canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}
