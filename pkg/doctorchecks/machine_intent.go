package doctorchecks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/doctor"
	"github.com/grovetools/core/pkg/workspace"

	"github.com/sirupsen/logrus"
)

func init() {
	doctor.Register(&machineIntentCheck{})
}

// machineIntentCheck reconciles declared intent (machine.toml) against what is
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
func (c *machineIntentCheck) Name() string { return "machine.toml matches what is on disk" }

func (c *machineIntentCheck) Run(ctx context.Context, opts doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}

	machineCfg, err := config.LoadMachineConfig()
	if err != nil {
		res.Status = doctor.StatusFail
		res.Message = "machine.toml is unreadable, so this machine has no declared intent"
		res.Error = compactError(err)
		res.Resolution = fmt.Sprintf("fix %s (or move it aside); `grove machine status` shows the same error", config.MachineConfigPath())
		return res
	}

	states := config.ReconcileMachineEcosystems(machineCfg)
	if machineCfg == nil || len(states) == 0 {
		res.Status = doctor.StatusOK
		res.Message = "no ecosystem subscriptions declared; nothing to reconcile"
		if legacy := legacyGrovesCount(); legacy > 0 {
			res.Message = fmt.Sprintf("no ecosystem subscriptions declared, but %d legacy [groves.*] entr%s exist",
				legacy, plural(legacy, "y", "ies"))
			res.Resolution = "run `grove machine migrate` to declare them as machine.toml subscriptions"
		}
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
		case config.MachineEcosystemDeclaredMissing:
			missing = append(missing, fmt.Sprintf("%s (declared at %s, nothing there)", st.Name, st.Path))
		case config.MachineEcosystemUnmanifested:
			missing = append(missing, fmt.Sprintf("%s (%s exists but carries no grove manifest)", st.Name, st.Path))
		}
	}

	undeclared := undeclaredEcosystems(declared)

	switch {
	case len(missing) > 0 && len(undeclared) > 0:
		res.Status = doctor.StatusWarn
		res.Message = fmt.Sprintf("%d declared-but-missing, %d present-but-undeclared", len(missing), len(undeclared))
		res.Error = strings.Join(append(missing, undeclared...), "; ")
		res.Resolution = "materialize or drop the missing subscriptions; declare the undeclared ecosystems under [machine.ecosystems.*] so they travel with your dotfiles"
	case len(missing) > 0:
		res.Status = doctor.StatusWarn
		res.Message = fmt.Sprintf("%d subscription(s) declared but missing on this machine", len(missing))
		res.Error = strings.Join(missing, "; ")
		res.Resolution = "materialize each one, or remove the subscription from machine.toml if this machine should not have it"
	case len(undeclared) > 0:
		res.Status = doctor.StatusWarn
		res.Message = fmt.Sprintf("%d discovered ecosystem(s) are not declared in machine.toml", len(undeclared))
		res.Error = strings.Join(undeclared, "; ")
		res.Resolution = "add them under [machine.ecosystems.*] (or `grove machine migrate`) so a restored machine gets them too"
	default:
		res.Status = doctor.StatusOK
		res.Message = fmt.Sprintf("%d subscription(s) present, none undeclared", len(states))
	}
	return res
}

func (c *machineIntentCheck) AutoFix(ctx context.Context) error {
	// Deliberately not fixable: which direction to reconcile is intent, and
	// materializing an ecosystem clones from the network.
	return fmt.Errorf("%w: run `grove machine status` and decide per entry (`grove machine migrate` imports legacy groves)", doctor.ErrNotFixable)
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

// legacyGrovesCount counts [groves.*] entries still declared anywhere in the
// cascade — the migration backlog.
func legacyGrovesCount() int {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	layered, err := config.LoadLayered(cwd)
	if err != nil {
		return 0
	}
	seen := map[string]bool{}
	count := func(cfg *config.Config) {
		if cfg == nil {
			return
		}
		for name := range cfg.Groves {
			seen[name] = true
		}
	}
	count(layered.Global)
	for _, frag := range layered.GlobalFragments {
		count(frag.Config)
	}
	if layered.GlobalOverride != nil {
		count(layered.GlobalOverride.Config)
	}
	return len(seen)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
