# Plugin Distribution (`grove plugin`)

`grove plugin install` is how a treemux sidecar panel gets from a git
repository onto a machine. The target UX is one line:

```console
$ grove plugin install github.com/user/grove-panel-foo@v1.2.0
```

→ clone at that ref → resolve it to an exact commit → show the user what it
declares and ask → build → binary into grove's managed bin dir → manifest
fragment into `~/.config/grove/plugins/foo.toml` → the rail item appears in
treemux on its next config reload. No restart, no recompile, no hand-edited
config.

This document is the design. The plugin-author view is the
"Ship it: `grove-plugin.toml`" section of
[treemux `docs/writing-a-panel.md`](https://github.com/grovetools/treemux/blob/main/docs/writing-a-panel.md)
— cited by name, because section numbers there have moved once already and a
stale `§10` is a reference that looks fine and points at nothing. The protocol a
panel speaks is `docs/panel-protocol-v1.md` in the same repo.

---

## 1. The pipeline, and what it reuses

Almost all of this already existed. What `grove plugin` adds is a manifest
format, a lockfile, one consent prompt, and the CLI that drives the stages in
order.

| Stage | Mechanism | Where it comes from |
| --- | --- | --- |
| **Declare** | `~/.config/grove/plugins/*.toml` drop-in glob, merged into the global config layer | `core/config/config.go` — already shipped |
| **Clone** | `git clone` / `git fetch` into `DataDir()/plugins/src/<slug>` | `grove/pkg/plugin/source.go` — new, but the same shape as `pkg/sdk`'s source installs |
| **Build** | the build-job runner `grove build` uses | `grove/pkg/build` — reused as-is |
| **Install** | `versions/<commit>/bin/<binary>` plus a symlink in `BinDir()` | `grove/pkg/sdk`'s managed-binary convention |
| **Consent** | MAC'd v2 trust store, keyed by config file + digest | `core/pkg/exectrust` — the store `grove config trust` writes |
| **Appear** | `config_reload` hot-reload and rail reconcile | treemux — already shipped |

Two seams were added rather than reused, and both are genuinely new surface:
the **manifest schema** (`grove-plugin.toml`, §2) and the **lockfile** (§3).

## 2. `grove-plugin.toml`

The manifest lives at the root of the plugin repository. It is the only thing
the installer reads out of the repo before the user has approved anything.

```toml
schema_version = 1

[plugin]
name        = "hello"          # ^[a-z0-9][a-z0-9-]{0,63}$ — filename, TOML key, rail id
description = "…"              # required: it is shown on the consent screen
homepage    = "https://…"      # optional

[build]
command = ["go", "build", "-o", "bin/grove-panel-hello", "."]   # optional argv
binary  = "bin/grove-panel-hello"                               # required, relative

[panel]
icon             = "H"
protocol         = "embed/v1"  # "" (plain PTY panel) or "embed/v1"
protocol_timeout = "2s"
args             = []
env              = ["KEY=VALUE"]
restart          = true

[[panel.keys]]
key         = "ctrl+f"
description = "jump to the notebook"
```

Validation (`grove/pkg/plugin/manifest.go`) is deliberately strict about the
things a consent screen depends on:

- `schema_version` must be exactly 1. A higher one is refused rather than
  guessed at.
- `name` has to be safe as a filename, a TOML bare key and a panel id at once.
- `description` is required, because a screen that says "approve this" with no
  statement of what it is is not consent.
- `build.command` is **argv, never a shell string**, so the prompt can show
  exactly what runs. It is optional: a panel that ships an interpreted program
  (see `examples/grove-panel-sh`) needs no toolchain, and that is the
  no-build-required path release assets would otherwise be needed for.
- `build.binary` must stay inside the checkout.
- Every displayed string is rejected if it carries control characters — the
  user's decision depends on reading it, so a value that can move the cursor is
  not a value.
- Unknown keys are a **warning, not an error**: they are listed on the consent
  screen and ignored. A manifest written for a later grove still installs on an
  earlier one, which is what keeps one plugin repo installable by two grove
  versions.

`panel.keys` is a *declaration*, not a grant. The host filters key claims at
handshake time (`welcome.rejected_keys`); the manifest carries them so the user
reads what the panel intends to take over before approving it.

## 3. The lockfile

`~/.config/grove/plugins/plugins.lock.json`, one entry per installed plugin:
the source, the requested ref, **the exact commit**, the manifest digest, the
consent digest, and the paths that were written.

Nothing floats. A ref is resolved to a commit once, at install time; the
installed binary is copied out of that build; and rebuilds work from the
recorded commit. `grove plugin update` is the only thing that moves a pin, and
it re-resolves the *recorded* ref — so a plugin pinned to `v1.2.0` does not
move when upstream's `main` does, and moving it to `v1.3.0` is an explicit
`--ref`.

The file is JSON on purpose. `core/config` globs
`~/.config/grove/plugins/*.toml` and merges every match as configuration; a
lockfile named `.toml` in that directory would be parsed as a grove config
file. (`TestLockfileIsNotGlobbedAsAConfigFragment` pins that.)

## 4. Install-time trust is the consent moment

An installed plugin is a process treemux spawns as you, every time it starts.
The decision to allow that is made once, on a screen showing:

- the source and the exact commit,
- the build command that will run in the checkout,
- the command treemux will run at every start, with its environment,
- the control-plane protocol it gets, and the hotkeys it declares,
- the two paths that will be written.

and it happens **before anything is built, written or trusted**. Declining
leaves the machine as it was found: no fragment, no lockfile, no binary, no
trust record, and not even the checkout if this run created it
(`TestInstallDeclinedWritesNothing`).

The approval goes into `core/pkg/exectrust` — the same MAC'd v2 store
`grove config trust` writes, keyed by the fragment path, with a digest over the
consent facts. This is reuse, not analogy: an installed plugin shows up in
`grove config trust --list` alongside every other exec-bearing config decision,
and there is no second trust store to audit. (Growing one was the specific
mistake the provenance work avoided when `[claude]` was made to reuse
`pkg/exectrust`.)

Because the digest covers the pinned commit and the manifest bytes, **an
approval covers that pin and nothing else**. `grove plugin update` recomputes
it, finds it different, and re-prompts with a diff of what changed since you
approved — the build command, the run command, the environment, the protocol,
the key claims.

### What this does and does not enforce

The fragment lands in the **global** config layer, which is the user's own
layer — `core/config`'s exec-provenance gate does not police it, and should
not: it exists to stop *cloned repositories* from introducing exec-bearing
config. So the enforcement point for plugins is the installer itself, which
refuses to build or write without approval, plus `grove plugin list`, which
reports a fragment whose approval no longer matches (`edited`) rather than
silently repairing it.

That is the honest boundary: `grove plugin` decides what gets installed, not
what a fragment already on disk is allowed to do. A user who hand-writes a
`[tui.plugins]` entry into their own config has always been able to run
whatever they like, and that is not this feature's business.

## 5. On disk

```
~/.config/grove/plugins/<name>.toml          the [tui.plugins.<name>] fragment
~/.config/grove/plugins/plugins.lock.json    the pins
~/.local/share/grove/plugins/src/<slug>/     the checkout, detached at the pin
~/.local/share/grove/plugins/versions/<name>/<commit>/bin/<binary>
~/.local/share/grove/bin/<binary>            symlink → the pinned build
~/.local/state/grove/exec-trust.json         the approval
```

The fragment's `command` is the **absolute** path of the bin dir entry, not a
bare name: whether a panel starts should not depend on whether grove's bin dir
happens to be on the PATH of whatever started treemux.

A plugin binary that would land on top of an existing bin dir entry grove did
not install for that plugin is refused, not overwritten
(`TestInstallRefusesToStompAnUnrelatedBinary`). A panel called `grove` does not
get to replace `grove`.

## 6. Commands

| Command | What it does |
| --- | --- |
| `grove plugin install <source>[@ref]` | clone, pin, ask, build, install, declare |
| `grove plugin list` | every pin, its commit, and whether it is intact |
| `grove plugin update [name…]` | re-resolve the recorded ref; diff; ask; move the pin |
| `grove plugin remove <name>` | fragment, binary, versions, checkout, pin, approval |

`--yes` approves non-interactively (it still prints the screen). `--json` is
available on all four. A source may be `host/owner/repo`, any git URL, or a
path to a local repository — the last one is how you test your own panel before
pushing it anywhere.

## 7. Deliberately not in v1

- **Prebuilt release assets.** Downloading a release binary instead of building
  from source is the obvious next step — `pi install git:…` is the in-house
  precedent — and it is what makes plugins installable on a machine with no Go
  toolchain. v1 covers the no-toolchain case from the other end (an optional
  build step, so interpreted panels install as-is) and defers the rest, because
  release assets bring a verification question with them: an asset is not
  reproducible from the commit the lockfile pins, so the pin would have to cover
  a *checksum* of the downloaded artifact as well. That is a real design, not a
  missing `if`. **v2.**
- **A curated registry or gallery.** GitHub-URL-as-identity is enough to ship
  the mechanism, and shipping the mechanism first is what tells us whether
  curation is even wanted. Curation is a people problem; nothing here blocks it.
- **npm-style dependency resolution.** A plugin is one repository and one
  binary. Plugins that depend on plugins is a different product.
- **Repo-shipped panels.** `[tui.plugins]` is a global-layer-only key, so a
  cloned repository still cannot introduce a panel — the installer writes to the
  global layer, so distribution does not need that restriction relaxed.
  Relaxing it (a repo shipping a panel that appears when you open that repo) is
  strictly more powerful and strictly more dangerous, and the provenance gate
  would be load-bearing rather than belt-and-braces the day it lands.
