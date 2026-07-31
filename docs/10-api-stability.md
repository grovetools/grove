# API Stability

Grove is a set of ~26 Go modules that mostly talk to each other. Almost every
package in them is exported, because they were split out of one binary and Go
has no way to say "exported for my siblings, not for you". That is not a
promise. This document says which surfaces *are* a promise, what "stable" means
before 1.0, and how a break is supposed to reach you.

Read it if you are writing a treemux panel, a daemon API client, a sidecar
service, or a custom build of the TUI. If you are only *using* grove, the
short version is: the CLI and `grove.toml` will not break under you without a
changelog entry.

---

## 1. The contract surfaces

These are the surfaces third parties are invited to build against. Everything
here gets a changelog entry when it changes and is covered by `make apidiff`
(§4) where a Go API diff makes sense.

| Surface | Where | What it is for |
| --- | --- | --- |
| **CLI** | `grove`, `flow`, `nb`, `tend`, … — reference in [`07-cli-reference.md`](07-cli-reference.md) | Scripting grove from a shell |
| **`grove.toml` schema** | `core/config` (Go types), `core/schema/grove.embedded.schema.json` (generated), reference in [`05-configuration.md`](05-configuration.md) | Configuring a workspace; the deepest extension point grove has |
| **Daemon HTTP API** | 81 routes served by `daemon/internal/daemon/server`; the Go client is `core/pkg/daemon.Client` (94 interface methods, `LocalClient` + `RemoteClient`) | Sidecar services and out-of-process automation. `daemon` is 100% `internal/` **on purpose** — the wire API is the contract, not its Go packages. See [`daemon/docs/reacting-to-grove-events.md`](https://github.com/grovetools/daemon/blob/main/docs/reacting-to-grove-events.md) |
| **Panel protocol `embed/v1`** | `core/panelkit/panelproto` (`Version = "embed/v1"`), spec in [`treemux/docs/panel-protocol-v1.md`](https://github.com/grovetools/treemux/blob/main/docs/panel-protocol-v1.md) | Out-of-process sidecar panels. Versioned on the wire — a breaking change means `embed/v2`, not a silent edit |
| **Plugin manifest** | `~/.config/grove/plugins/*.toml`, described in [`09-plugins.md`](09-plugins.md) | What `grove plugin install` writes and treemux reads |

And these Go packages:

| Package | Module | For |
| --- | --- | --- |
| `github.com/grovetools/tuimux` (root) | tuimux | `Panel`, `PanelBuilder`, `Session`, the persisted-layout types. The interface a native panel implements |
| `github.com/grovetools/tuimux/embed` | tuimux | In-process embed host messages |
| `github.com/grovetools/tuimux/panels` | tuimux | Built-in panel implementations reusable by a custom build |
| `github.com/grovetools/tuimux/bindings` | tuimux | Key-binding declarations |
| `github.com/grovetools/core/config` | core | Reading and validating `grove.toml` |
| `github.com/grovetools/core/pkg/daemon` | core | The typed daemon client |
| `github.com/grovetools/core/tui/components/pager` | core | `pager.Page` — the drawer-page interface |
| `github.com/grovetools/core/tui/hostedkeys` | core | The hosted-key claim/grant shapes shared by hosts and panels |
| `github.com/grovetools/core/panelkit` (and `/window`, `/table`, `/tree`, `/layout`) | core | The panel SDK's widgets |
| `github.com/grovetools/core/panelkit/panelproto` | core | Go bindings for `embed/v1` |
| `github.com/grovetools/core/panelkit/sidecar` | core | The out-of-process panel runtime — `Run`, `Connect`, `Client` |
| `github.com/grovetools/treemux/pkg/keymap` | treemux | Key resolution and hosted-claim arbitration |
| `github.com/grovetools/treemux/pkg/keyspec` | treemux | `PanelSpec` / `CanonicalPanels` — the rail-panel registry |
| `github.com/grovetools/treemux/pkg/panelproto` | treemux | **Deprecated**: a frozen alias of `core/panelkit/panelproto`, removed one release after treemux's first tag |

**A Go panel imports one grove module.** The whole SDK — widgets, `embed/v1`
bindings, sidecar runtime — is in `core`, and none of it imports treemux: a
sidecar reaches its host over a socket, not over an import. The protocol and
runtime were written in treemux and moved here because treemux is untagged, so
a third-party panel could only name it with a `replace` pointing at a local
checkout. Nothing about the wire contract changed; `embed/v1` is still
`embed/v1`.

`tuimux/api/types` is contract by proxy: the root package aliases those types,
and the aliases are what you should import.

## 2. Everything else is internal-in-spirit

If a package is not in §1, treat it as private even though the compiler will
happily let you import it. Concretely:

- **`core` has no `internal/` directory at all** — ~85 package directories, all
  importable, and four of them are contract. `core/pkg/workspace`, `core/git`,
  `core/pkg/sessions`, `core/tui/components/*` (other than `pager`) and the
  rest exist for grove's own binaries and change whenever those need them.
- **`treemux/internal/**` is correctly fenced.** The panel-facing pieces that
  live there today (`WrapPanelCmd`, `KittyKeyMsg`, `HostedKeyClaimer`, the
  `PanelType` convention) are *intended* to be promoted to `treemux/pkg/panel`;
  until they are, a native rail panel means a fork. That promotion is the open
  work, not a reason to import `internal/`.
- **`daemon/internal/**` is correctly fenced** and will stay that way. Use the
  HTTP API.
- Anything named `testutil`, `tests/`, `tools/`, `cmd/` is not API.

Nothing enforces this. It is a statement of intent so that, when a non-contract
package changes shape in a patch release, you know that was allowed.

## 3. The pre-1.0 policy, honestly

Every module is `v0.x`. Under semver, `v0.x` gives you nothing, and grove is
using that latitude:

- **Minor versions may break any surface, contract or not.** `v0.6.x → v0.7.0`
  is allowed to remove a CLI flag, rename a config key, change a daemon route,
  or change a Go signature.
- **What contract surfaces buy you is notice, not immunity**: a breaking change
  to anything in §1 gets an entry in that module's `CHANGELOG.md` describing
  what broke and what to do about it. A breaking change to a non-contract
  package gets nothing.
- **Patch versions** (`v0.6.3 → v0.6.4`) aim not to break contract surfaces.
  Aim, not guarantee.
- **The wire protocols are the sturdiest thing here.** `embed/v1` is version-
  negotiated, so a sidecar panel built today keeps working; incompatible
  protocol changes ship as a new version string with both supported for a
  transition.
- **1.0 is not scheduled.** When it happens, §1 is the list that gets semver
  guarantees and §2 is the list that gets moved into `internal/` or promoted
  deliberately.

Two facts worth stating plainly rather than discovering:

1. The contract packages have **already drifted incompatibly** since their last
   tags — `make apidiff` (§4) currently reports breaks in `core/config`,
   `core/pkg/daemon` and `tuimux` against `v0.6.3`/`v0.0.1`. Pin an exact
   version and read the changelog before upgrading.
2. Several modules in the graph are **private or untagged**, so `go get` of the
   published modules does not work for everyone yet. See
   [`11-release-runbook-modules.md`](11-release-runbook-modules.md) for what is
   missing and who fixes it.

## 4. Checking for breaks: `make apidiff`

From the `grove` repo in a grove workspace:

```console
$ make apidiff              # every contract module
$ make apidiff ARGS=core    # just one
```

It checks out each module's most recent tag into a throwaway git worktree,
snapshots the exported API of that module's contract packages with
[`apidiff`](https://pkg.go.dev/golang.org/x/exp/cmd/apidiff), and diffs the
working tree against the snapshot. Output is per package: `no changes`,
`compatible changes only`, `INCOMPATIBLE` with the specific symbols, or `new
since <tag>` for packages that did not exist at the tag. It exits non-zero if
anything is incompatible (`ALLOW_INCOMPATIBLE=1` to report and pass anyway).
Modules with no tags are skipped with a note.

Install the tool once:

```console
$ go install golang.org/x/exp/cmd/apidiff@latest
```

The contract package set lives in `CONTRACT` at the top of
`grove/scripts/apidiff.sh`. Keep it and §1 of this document in sync.

**It is deliberately not part of `make check`.** Pre-1.0 breaks are allowed;
the gate's job is to make sure you *know* you made one so you can changelog it.
Run it before tagging a release — the release runbook says so — and when you
change something in §1.

CI cannot run it today: every repo's `.github/workflows/ci.yml` is disabled
(`on: push: branches: [none]`) and configured for a stale module path
(`GOPRIVATE=github.com/your-github-user/*`). When CI is revived, `make apidiff` is the
one line to add.

## 5. Changing a contract surface

1. Run `make apidiff` and read what it says you broke.
2. If the break is avoidable, avoid it — add a new symbol, keep the old one,
   deprecate it in a doc comment.
3. If it is not, write the `CHANGELOG.md` entry in the owning module: what
   broke, the old shape, the new shape, and the migration.
4. For `embed/v1` and the daemon HTTP API, prefer additive changes; a genuine
   break to `embed/v1` means introducing `embed/v2` alongside it.
5. If you are adding a package that third parties should build against, add it
   to §1 *and* to `CONTRACT` in `grove/scripts/apidiff.sh`.
