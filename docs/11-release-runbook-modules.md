# Release Runbook: Making the Modules `go get`-able

This is a runbook for the release coordinator. It covers the one-time work that
`grove release` cannot do for you — repository visibility and first tags — plus
the follow-up edits that turn the commit pseudo-versions currently pinned in
`flow`, `agentlogs` and `daemon` into real tags.

Nothing here has been done. The module-graph hygiene work fixed everything that
could be fixed without pushing to a remote or changing a GitHub setting; what
remains is exactly this list.

Status as of 2026-07-29 (`grove-extensiblity` branch).

---

## 0. Where things actually stand

**What is `go get`-able today.** Verified from a clean module with `GOWORK=off`
and the public proxy:

```console
$ go get github.com/grovetools/core@v0.6.3    # ok
$ go get github.com/grovetools/flow@v0.6.3    # ok
$ go get github.com/grovetools/grove@v0.6.3   # ok
```

This works by luck of timing: at `v0.6.3`, `core` did not yet require `tuimux`
and `flow` did not yet require `eval`, `grove-openrouter` or `memory`. Those
dependencies are all newer than the last release.

**What breaks at the next release.** `main` now has:

| Module | New dependency since its last tag | Problem |
| --- | --- | --- |
| `core` | `tuimux` | tuimux repo is **private** |
| `flow` | `eval`, `grove-openrouter`, `memory` | all three repos are **private**; eval and grove-openrouter are also **untagged** |
| `agentlogs` | `eval` | private, untagged |
| `daemon` | `tuimux`, `memory` | private; `daemon` itself has **no tags** |
| `skills`, `compositor`, `treemux` | `tuimux` | private |

So the moment `core v0.6.4` is cut, `go get github.com/grovetools/core` starts
failing for anyone outside the org. §1 is the fix.

**Repository visibility** (`gh repo view`):

- Private: `agent`, `cloud`, `eval`, `git-viewer`, `grove-openrouter`,
  `memory`, `sync`, `treemux`, `tuimux`
- Public: everything else

**Untagged repos**: `cloud`, `daemon`, `eval`, `git-viewer`,
`grove-openrouter`, `sync`, `treemux`.

**Pseudo-versions currently pinned in the tree** (all resolve to real,
pushed `origin/main` commits — they are not placeholders):

| Module | Requires | Pinned at |
| --- | --- | --- |
| `flow` | `eval` | `v0.0.0-20260724205137-f2ccb7b82b04` |
| `flow` | `grove-openrouter` | `v0.0.0-20260718141608-3bd491b0b10a` |
| `agentlogs` | `eval` | `v0.0.0-20260724205137-f2ccb7b82b04` |
| `daemon` | `skills` | `v0.6.1-0.20260727192925-24c2196cb16e` |
| `flow` | `core` | `v0.6.4-0.20260521140340-5660efd35db0` (pre-existing) |

## 1. Decide visibility — this gates everything else

`go get` cannot see a private repo, and neither can `proxy.golang.org` or
`sum.golang.org`. Tagging a private module changes nothing for a third party.

The minimum set to make the *published* modules consumable is **`tuimux`,
`eval`, `grove-openrouter`, `memory`**. Add **`treemux`** if third parties are
meant to build custom TUIs (the tier-4 "grove distro" story), since
`treemux/pkg/{keymap,keyspec}` are contract packages in
[`10-api-stability.md`](10-api-stability.md).

**Writing a sidecar panel no longer needs treemux at all.** The `embed/v1`
bindings and the panel runtime now live in `core/panelkit`, so a Go panel
depends on `core` and nothing else of grove's. That was the point of moving
them: treemux is untagged and private, and a panel author could satisfy the
import only with a `replace` into a local checkout. `tuimux` going public is
still on the critical path, because `core`'s own `go.mod` requires it.

```console
$ gh repo edit grovetools/tuimux --visibility public --accept-visibility-change-consequences
$ gh repo edit grovetools/eval --visibility public --accept-visibility-change-consequences
$ gh repo edit grovetools/grove-openrouter --visibility public --accept-visibility-change-consequences
$ gh repo edit grovetools/memory --visibility public --accept-visibility-change-consequences
$ gh repo edit grovetools/treemux --visibility public --accept-visibility-change-consequences
```

Before flipping any of these, audit the history for secrets — going public
exposes every commit, not just the tip.

**If they stay private**, the modules are consumable only by people with repo
access, and every consumer needs:

```console
$ go env -w GOPRIVATE=github.com/grovetools/*
$ git config --global url."ssh://git@github.com/".insteadOf "https://github.com/"
```

Say so in the READMEs if that is the answer, and skip §2's public-proxy checks.

`compositor`'s default branch is `treemux-phase4`, not `main`. Tag resolution
does not care, but `@latest` on an untagged module and anyone browsing the repo
does. Worth fixing while you are in the settings.

## 2. Tag `eval` and `grove-openrouter`, drop the pseudo-versions

These two have never been released. `flow` and `agentlogs` pin them by commit;
the goal is a tag.

Preferred path — let the orchestrator do it, since it handles dependency order
and the downstream `go get` bumps (see [`04-ecosystems.md`](04-ecosystems.md)):

```console
$ grove release plan          # includes eval and grove-openrouter
$ grove release tui           # review bumps + changelogs, approve
$ grove release apply
```

Manual path, if you are releasing only these two:

```console
# eval — currently at origin/main f2ccb7b
$ cd eval
$ make check && make build
$ git tag -a v0.6.0 -m "eval v0.6.0"
$ git push origin v0.6.0

# grove-openrouter — currently at origin/main 3bd491b
$ cd ../grove-openrouter
$ make check && make build
$ git tag -a v0.6.0 -m "grove-openrouter v0.6.0"
$ git push origin v0.6.0
```

Then move the consumers off the pseudo-versions:

```console
$ cd flow
$ go mod edit -require=github.com/grovetools/eval@v0.6.0
$ go mod edit -require=github.com/grovetools/grove-openrouter@v0.6.0

$ cd ../agentlogs
$ go mod edit -require=github.com/grovetools/eval@v0.6.0
```

Delete the "carries a commit pseudo-version rather than a tag" comment block at
the bottom of each `go.mod` once the requires point at tags.

`go.sum`: in-workspace builds do not need entries for workspace modules, so
`go.sum` will not have hashes for `eval` or `grove-openrouter`. Populate them
from outside the workspace once the tags are public:

```console
$ cd flow && GOWORK=off go mod tidy && GOWORK=off go build ./...
$ cd ../agentlogs && GOWORK=off go mod tidy && GOWORK=off go build ./...
```

Verify from a scratch module that the published graph resolves:

```console
$ cd $(mktemp -d) && go mod init probe
$ GOWORK=off go get github.com/grovetools/flow@latest
$ GOWORK=off go get github.com/grovetools/agentlogs@latest
```

## 3. First tags for `daemon` and `treemux`

Neither has ever been tagged, so `go get github.com/grovetools/daemon@latest`
resolves to a pseudo-version off `main` and `make apidiff` has no baseline to
diff against (it prints `SKIP (no tags)`).

- **`daemon`** is 100% `internal/` by design — its contract is the HTTP API, not
  its Go packages. Tagging it is about installability and reproducible builds,
  not about API. Note that `daemon`'s module graph was broken until recently: it
  required `skills v0.6.3`, a tag that does not exist. It now pins a commit
  pseudo-version; move it to a tag once `skills` cuts its next release.
- **`treemux`** *does* export contract packages (`pkg/keymap`, `pkg/keyspec`,
  and `pkg/panelproto` until the frozen alias is dropped). Tagging it
  establishes the apidiff baseline, so do it before promoting
  `treemux/internal/app` to `treemux/pkg/panel`.

```console
$ cd daemon  && make check && git tag -a v0.6.0 -m "daemon v0.6.0"  && git push origin v0.6.0
$ cd treemux && make check && git tag -a v0.6.0 -m "treemux v0.6.0" && git push origin v0.6.0
```

`cloud`, `git-viewer` and `sync` are also untagged. They are private leaf
binaries, so this is lower priority — tag them when they are meant to be
installable.

## 4. Fix the release workflows before relying on them

Every repo has `.github/workflows/{ci,release}.yml`, and both are stale:

1. **CI never runs.** `on: push: branches: [none]` in every repo. Re-enable it
   deliberately or delete it; a workflow that cannot run is worse than none.
2. **Wrong private path.** CI sets `GOPRIVATE=github.com/your-github-user/*`. The
   modules are `github.com/grovetools/*`. If the repos stay private (§1), this
   must be `github.com/grovetools/*` or dependency resolution fails.
3. **Go version skew.** Both workflows pin `go-version: '1.24.4'`, but `flow`,
   `grove`, `docgen`, `memory`, `notify`, `grove-anthropic`, `grove-gemini`,
   `grove-openrouter` and (now) `daemon`, `hooks`, `git-viewer` declare
   `go 1.25.0`. `release.yml` will fail on a tag push for those repos. Bump to
   `'1.25.0'` or `'stable'`.
4. **`release.yml` has no credentials step.** It runs `go mod download` in a
   bare checkout with no `GROVE_PAT` and no `GOPRIVATE`, so any module with a
   private dependency cannot release from CI as configured.

## 5. Verification checklist

After §1–§3, all of these should pass:

```console
# every workspace module still builds
$ for d in */; do (cd $d && [ -f go.mod ] && go build ./...); done

# no module requires a version that does not exist
$ for d in */; do grep -H 'grovetools/.* v0\.0\.0-00010101000000' $d/go.mod; done   # expect no output

# no replace directives into sibling worktrees
$ grep -rn '^replace github.com/grovetools' */go.mod                                # expect no output

# contract API diff has a baseline for every contract module
$ cd grove && make apidiff                                                          # no more "SKIP (no tags)"

# a stranger can actually get the modules
$ cd $(mktemp -d) && go mod init probe && GOWORK=off GOFLAGS= GOPRIVATE= \
    go get github.com/grovetools/core@latest github.com/grovetools/flow@latest
```

## 6. What was already done

Landed on `grove-extensiblity`; no remote was touched.

| Repo | Change |
| --- | --- |
| `flow` | `eval` + `grove-openrouter` pinned to real commit pseudo-versions; both `replace` directives removed |
| `agentlogs` | same for `eval` |
| `skills` | inert `replace github.com/grovetools/skills => ../skills` removed |
| `daemon` | `skills v0.6.3` (nonexistent tag) → commit pseudo-version; `go 1.24.4` → `1.25.0` |
| `hooks`, `git-viewer` | `go 1.24.4` → `1.25.0`, matching the `flow` they depend on |
| `grove` | `docs/10-api-stability.md`, this runbook, `scripts/apidiff.sh`, `make apidiff` |
