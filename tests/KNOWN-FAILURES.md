# Known test failures — the baseline a phase gate diffs against

`TMPDIR=/tmp go test ./...` in this repo is not green. The failure below is
declared, with provenance, so a per-phase gate can **diff against this list**
instead of re-deriving it by hand. Anything not listed here is a regression.

This is the grove half of W0.3 / audit **B3** — the `KNOWN-FAILURES.md` contract
`daemon/tests/KNOWN-FAILURES.md` established and `nb/tests/KNOWN-FAILURES.md`
carries for tend. It landed in nb only, so the Phase 4 gate's "diffed against
each repo's declared baseline" had nothing to diff against here; the Phase 4
final holistic review (F3) established the provenance below, and this file is
where it stops being re-derived.

## The contract

- A failing test may be listed here **only** with: its full name, the commit or
  condition that makes it fail, the date, and why it is not the current phase's.
- An entry is a debt, not a waiver. Nothing here is expected to stay.
- **An unlisted failure fails the gate.** This file cannot hide a new failure —
  it can only pre-declare an old one, in writing, with its cause.
- A listed test that starts PASSING is also a signal: move it to *Resolved* with
  the fixing commit rather than deleting the entry. The record of what was once
  broken is the part a future reviewer needs.

## Open

### `tests/e2e/cmd` — `TestBuildCommand/ExcludePattern`

```
build_test.go:97:
  Error: "Projects that would run 'build':\n\n  1. grove (<this worktree>/grove)\n\n\nTotal: 1 projects in 1 wave(s)\n\n"
         does not contain "grove-"
```

| | |
|---|---|
| **Failing when** | the ecosystem discovered from the test's *ambient working directory* holds no project whose name starts `grove-`. True inside a plan worktree that carries `grove` but not the `grove-*` repos as build projects; false in a full checkout. |
| **Cause** | environment, not code. The subtest shells out to the real `grove build --dry-run --exclude grove-core,grove-proxy` **without** `inScratchProject` (its two neighbours, `VerboseMode` and `ParallelExecution`, do take one) and then asserts `Contains(output, "grove-")` — i.e. it asserts a fact about whatever ecosystem the cwd resolves to. |
| **Not P4** | proven twice this run: (1) a test binary compiled from grove `main` `90f9369` and run **from this worktree's `grove/tests/e2e/cmd`** fails byte-identically; (2) the same source run from a `git archive main` export **passes**. The verdict is a function of where the test is invoked, and a `main` binary and an `ec8fd09` binary print identical `build --dry-run` output from the same cwd. |
| **Owner** | the grove e2e harness. This is exactly V1's "config tests never read ambient state" rule, violated in grove the way audit **B2** documents it in core. `defer inScratchProject(t, ...)()` with a seeded `grove-*` project, as the sibling subtests already do, makes the assertion mean what it says. |

## How the provenance above was established

No `git worktree add`, no `git stash`, nothing written under any repo's `.git`
(the B8 rule, and job 147's technique):

```sh
mkdir -p /tmp/p4close-base/grove
git -C grove archive main | tar -x -C /tmp/p4close-base/grove
# a scratch go.work in /tmp/p4close-base whose `use` block points at ./grove,
# ./core and this worktree's other 25 module dirs

# (1) the exported main source, run from the export: PASSES
cd /tmp/p4close-base && TMPDIR=/tmp go test ./grove/tests/e2e/cmd/ -run TestBuildCommand

# (2) the same main source, compiled there and run from the WORKTREE cwd: FAILS
cd /tmp/p4close-base && TMPDIR=/tmp go test -c -o /tmp/grove-main-cmd.test ./grove/tests/e2e/cmd/
cd <worktree>/grove/tests/e2e/cmd && TMPDIR=/tmp /tmp/grove-main-cmd.test -test.run TestBuildCommand
```

The comparison base is grove `main` **`90f9369`** (2026-08-12, *fix(migrate):
close machine-C second-attempt findings F9-F11*).

## Resolved

*(nothing yet)*
