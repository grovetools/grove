package cmd

// Per-repo cross-target capability for `--prebuilt` satellite deploys.
//
// The ecosystem cross-compile contract (GROVE_TARGET_* scoped to the final
// build) is honored by every repo's Makefile for every target — with
// exceptions, recorded here. A repo whose Makefile REFUSES the VM's arch used
// to be discovered the expensive way: the cross-build fails mid-deploy, the
// repo is dropped from the ship set, and the drop turns into a fatal deployErr
// — for `up`, after the VM is already billable. This table lets both `up` and
// `upgrade` know the limit up front: unshippable repos are excluded before the
// build wave, and a required one aborts while the provision is still free.

import (
	"fmt"
	"strings"

	orch "github.com/grovetools/grove/pkg/orchestrator"
)

// prebuiltRepoLimit records what a repo can do in a prebuilt deploy. The
// source of truth for Targets is the repo's own Makefile, which declares them
// in crossTargetsMakeVar; a test mirrors the two and fails on drift.
type prebuiltRepoLimit struct {
	// Targets, when non-empty, is the EXHAUSTIVE set of cross targets the
	// repo's Makefile accepts, in "<goos>/<goarch>" form. Absent from the
	// table (or an empty Targets) means "honors the contract for every
	// target", which is the norm.
	Targets []string
	// Optional marks a repo the satellite runs without: none of its binaries
	// are in satellite-bootstrap.sh's required set (grove, groved, flow, nb,
	// treemux, tuimux) or the grove-syncd unit. Excluding or dropping such a
	// repo degrades the satellite loudly instead of failing the deploy.
	Optional bool
}

// prebuiltRepoLimits is the exception table: repos that cannot cross-build to
// every target, and/or that a satellite can run without.
var prebuiltRepoLimits = map[string]prebuiltRepoLimit{
	// grove-agent is a bun --compile binary, so agent/Makefile enumerates the
	// targets it can produce rather than honoring the Go contract for all of
	// them — its CROSS_TARGETS variable, which the mirror test below parses.
	// Optional because bootstrap's required binaries are grove/groved/flow/nb/
	// treemux/tuimux plus the grove-syncd unit: a satellite that loses
	// grove-agent still syncs, federates and runs jobs.
	"agent": {Targets: []string{"linux/arm64", "linux/amd64"}, Optional: true},
}

// crossTargetsMakeVar is the Makefile variable a repo declares its supported
// cross targets in — the source of truth prebuiltRepoLimits mirrors.
const crossTargetsMakeVar = "CROSS_TARGETS"

// prebuiltRepoSupportsTarget reports whether repo's Makefile can cross-build
// for target. Repos absent from the exception table support everything.
func prebuiltRepoSupportsTarget(repo string, target orch.Target) bool {
	limit, ok := prebuiltRepoLimits[repo]
	if !ok || len(limit.Targets) == 0 {
		return true
	}
	for _, t := range limit.Targets {
		if t == target.String() {
			return true
		}
	}
	return false
}

// prebuiltRepoOptional reports whether a satellite is still functional without
// repo's binaries. Anything not in the table is required.
func prebuiltRepoOptional(repo string) bool {
	return prebuiltRepoLimits[repo].Optional
}

// splitPrebuiltReposForTarget partitions repos into the ones that can be
// cross-built for target and the ones whose Makefile refuses it, preserving
// order. Callers decide what an unsupported REQUIRED repo means (fatal for
// `up`'s pre-terraform validation, a drop mid-deploy).
func splitPrebuiltReposForTarget(repos []string, target orch.Target) (supported, unsupported []string) {
	for _, r := range repos {
		if prebuiltRepoSupportsTarget(r, target) {
			supported = append(supported, r)
		} else {
			unsupported = append(unsupported, r)
		}
	}
	return supported, unsupported
}

// partitionOptionalRepos splits repos into the ones a satellite needs and the
// ones it merely wants — the difference between a failed deploy and a loud
// warning.
func partitionOptionalRepos(repos []string) (required, optional []string) {
	for _, r := range repos {
		if prebuiltRepoOptional(r) {
			optional = append(optional, r)
		} else {
			required = append(required, r)
		}
	}
	return required, optional
}

// unsupportedPrebuiltReposMessage renders the notice for repos excluded from a
// ship set because their Makefile cannot target the VM's arch. "" for none, so
// callers can print (or error with) it unconditionally.
func unsupportedPrebuiltReposMessage(repos []string, target orch.Target) string {
	if len(repos) == 0 {
		return ""
	}
	return fmt.Sprintf("%s cannot cross-build for %s (their Makefile supports only %s)",
		strings.Join(repos, ", "), target, prebuiltRepoTargetsSummary(repos))
}

// prebuiltRepoTargetsSummary renders the targets the given repos do support,
// for the exclusion message ("linux/arm64").
func prebuiltRepoTargetsSummary(repos []string) string {
	var all []string
	seen := map[string]bool{}
	for _, r := range repos {
		for _, t := range prebuiltRepoLimits[r].Targets {
			if !seen[t] {
				seen[t] = true
				all = append(all, t)
			}
		}
	}
	if len(all) == 0 {
		return "other targets"
	}
	return strings.Join(all, ", ")
}
