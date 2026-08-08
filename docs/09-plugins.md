# Plugin Distribution (`grove plugin`)

> **Hand-maintained, exempt from docgen.** Every other numbered file in this
> directory is generated from a prompt in the notebook's `workspaces/grove/docgen/`
> and re-derived from current code on each `docgen generate`. This one is not:
> it describes a distribution format with third-party authors on the other end,
> and a format that is re-narrated from code each release is a format that
> changes without anyone deciding to. Edit it by hand, and do not add a docgen
> section pointing at it.
>
> The design source is the notebook concept `grovetools:grove-extensibility-docs`
> (`grove--09-plugins.md`). The panel-author view is the "Ship it:
> `grove-plugin.toml`" section of treemux's `docs/writing-a-panel.md`; the
> protocol a panel speaks is `docs/panel-protocol-v1.md` in the same repo; the
> tool-author view is `writing-a-tool.md` beside this file. All are cited
> **by name**, because section numbers there have moved once already
> and a stale `§10` is a reference that looks fine and points at nothing.

`grove plugin install` is how a plugin gets from a git repository onto a
machine. A plugin is one of **two kinds**: a treemux sidecar **panel**, or a
CLI **tool** dispatched as a grove subcommand. The panel was the first kind
and is the running example throughout this file; a tool travels the same
pipeline end to end — clone, pin, consent, build, managed binary, lockfile,
trust record — and differs only at the two ends: its manifest declares
`[tool]` instead of `[panel]`, and what appears is not a rail item but a
`grove <verb>` subcommand. The differences are collected in "Tools: the same
pipeline, minus the panel wiring" below; the tool author's guide is
`writing-a-tool.md` beside this file. The target UX is one line:

```console
$ grove plugin install github.com/user/grove-panel-foo@v1.2.0
```

→ clone at that ref → resolve it to an exact commit → show the user what it
declares and ask → build → binary into grove's managed bin dir → manifest
fragment into `~/.config/grove/plugins/foo.toml` → the rail item appears in
treemux on its next config reload. No restart, no recompile, no hand-edited
config.

---

## The pipeline, and what it reuses

Almost all of this already existed. What `grove plugin` adds is a manifest
format, a lockfile, one consent prompt, and the CLI that drives the stages in
order.

| Stage | Mechanism | Where it comes from |
| --- | --- | --- |
| **Declare** | `~/.config/grove/plugins/*.toml` drop-in glob, merged into the global config layer | `core/config/config.go` — already shipped |
| **Clone** | `git clone` / `git fetch` into `DataDir()/plugins/src/<slug>` | `grove/pkg/plugin/source.go` — new, but the same shape as `pkg/sdk`'s source installs |
| **Build** | the build-job runner `grove build` uses | `grove/pkg/build` — reused as-is |
| **Install** | `versions/<name>/<commit>/bin/<binary>` plus a symlink in `BinDir()` | `grove/pkg/sdk`'s managed-binary convention |
| **Consent** | MAC'd v2 trust store, keyed by config file + digest | `core/pkg/exectrust` — the store `grove config trust` writes |
| **Appear** | `config_reload` hot-reload and rail reconcile | treemux — already shipped |

Two seams were added rather than reused, and both are genuinely new surface:
the **manifest schema** (`grove-plugin.toml`) and the **lockfile**.

### Where the code lives

The package is split by **direction**, not by file, because the two ends of the
pipeline are in different modules:

| | Package |
| --- | --- |
| **Read** — manifest, lockfile, locations, approval check | `core/pkg/plugin` |
| **Write** — clone, build, install, declare, record, remove | `grove/pkg/plugin` |

The read side is in `core` so treemux can answer questions about panels it is
already running — which commit is installed, what the manifest declared, whether
the fragment still matches the approval — without importing grove or shelling
out. Reaching that state through a grove import would mean paying for the whole
install pipeline in order to read four files. `grove/pkg/plugin` aliases the read
side, so existing grove call sites are unchanged.

Nothing in the read half runs a program, clones a repository or writes to the
lockfile. The one thing it writes is the exec-trust record, which is the same
MAC'd store `grove config trust` uses rather than a second trust store of its
own.

### Tools: the same pipeline, minus the panel wiring

Every stage above runs for a tool install except the panel half of the last
two. The per-plugin file under `~/.config/grove/plugins/` is still written —
it is the trust anchor the approval is keyed by, the unit `remove` deletes,
and the file a user can move aside to disable the plugin by hand — but it
carries **no `[tui.plugins]` pane entry**, and the "Appear" stage does not
happen: nothing shows up in treemux, because there is nothing to show.

What a tool gets instead is **dispatch**: `grove <verb> …` resolves,
git-subcommand-style, through the lockfile pin to the installed binary, and
hands it the remaining arguments. The verb namespace is guarded at install
time — a verb already claimed by another installed tool, by a grove built-in
command, or by a registered ecosystem tool refuses the install loudly rather
than resolving ambiguously later. A verb whose binary has gone missing
produces a friendly error naming the plugin that provides it and the
`grove plugin update <name> --force` that repairs it.

The consent screen is also where the kinds part company hardest: a panel
claims a hotkey and draws in a rectangle; a tool runs with the user's real
credentials against whatever they reach, so its consent copy is franker —
what it runs, what it can reach. Both the dispatch rules and the consent
framing are the tool author's business, and both are covered in
`writing-a-tool.md`.

## `grove-plugin.toml`

The manifest lives at the root of the plugin repository. It is the only thing
the installer reads out of the repo before the user has approved anything.

A manifest declares **exactly one** of `[panel]` or `[tool]`, and the section
present is what decides the kind — there is no `kind` field and no schema
bump, because a manifest that says what it is by carrying the tables for it
needs no second declaration to disagree with the first. The example below is
a panel; the `[tool]` counterpart is the "`[tool]`" subsection further down.

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
label            = "Hello panel"
icon             = "H"         # a core theme icon NAME, or the plugin's own glyph
protocol         = "embed/v1"  # "" (plain PTY panel) or "embed/v1"
protocol_timeout = "2s"
args             = []
env              = ["KEY=VALUE"]
restart          = true

[panel.settings]               # the panel's own defaults
work_minutes = 25
palette      = "auto"
palette_hex  = ""

[[panel.setting_options]]      # optional: a setting with a closed vocabulary
setting        = "palette"
description    = "which colors the panel draws in"
options        = ["auto", "host", "custom"]
custom_option  = "custom"      # choosing it hands over to...
custom_setting = "palette_hex" # ...this setting (or use allow_custom instead)

[[panel.keys]]                 # host chords it intends to claim
key         = "ctrl+f"
description = "jump to the notebook"

[panel.views.full]             # the panel's own named layouts
description = "clock, history and help"
drawer      = false

[panel.views.compact]
description = "one line: state and time remaining"
drawer      = true

[panel.digest]                 # it publishes a one-row projection of itself
description = "the current state and how long is left of it"

[panel.notebook]               # it writes into the user's notebook
subtree     = "hn/clippings"
description = "stories you clip from the feed"
```

Validation (`core/pkg/plugin/manifest.go`) is deliberately strict about the
things a consent screen depends on:

- `schema_version` must be exactly 1. A higher one is refused rather than
  guessed at.
- `name` has to be safe as a filename, a TOML bare key and a panel id at once.
- `description` is required, because a screen that says "approve this" with no
  statement of what it is is not consent. So are `views.<name>.description`,
  `digest.description` and `notebook.description`, for the same reason: "declares
  a view" and "publishes a digest" give a reader nothing to decide on.
- `build.command` is **argv, never a shell string**, so the prompt can show
  exactly what runs. It is optional: a panel that ships an interpreted program
  (see treemux's `examples/grove-panel-sh`) needs no toolchain, and that is the
  no-build-required path release assets would otherwise be needed for.
- A `[[panel.setting_options]]` entry must name a setting `[panel.settings]`
  declares, and that setting's default must be one of the values it offers
  (unless `allow_custom` says the list is only a suggestion). Both are author
  errors that are otherwise invisible: options hung on a mistyped setting path
  silently do nothing, and a default outside its own list means a UI offering
  that list has no entry to show as current.
- `build.binary` must stay inside the checkout. `notebook.subtree` is held to the
  same path rules — relative, no `..` escapes, printable — not because grove ever
  walks it (it never does) but because it is rendered on the consent screen, and
  a path that escapes reads as a claim about directories the notebook does not
  contain.
- Every displayed string is rejected if it carries control characters — the
  user's decision depends on reading it, so a value that can move the cursor is
  not a value.
- Unknown keys are a **warning, not an error**: they are listed on the consent
  screen and ignored. A manifest written for a later grove still installs on an
  earlier one, which is what keeps one plugin repo installable by two grove
  versions.

### Declarations, not grants

`keys`, `views`, `setting_options`, `digest` and `notebook` are all
**declarations**. grove copies each into the installed fragment, prints each on
the consent screen, and binds each into the approval digest — and enforces none
of them:

| Table | What the host does with it |
| --- | --- |
| `[[panel.keys]]` | compares it against the claims the panel's handshake actually makes; a disagreement is logged and surfaced, never refused. It is also the only hosted key reference reachable without a running panel, which is what lets `treemux keys` describe a configured panel. |
| `[panel.views.<n>]` | reads exactly one field, `drawer`, and never a name. A drawer pane naming no view gets the first view declared `true`; a view declared `false` mounted in a drawer warns and mounts anyway. The names are an open set only the panel can define. |
| `[[panel.setting_options]]` | offers the list where a config UI would otherwise draw a text box — treemux's plugin editor cycles it, and opens text entry for the `allow_custom` slot or the setting `custom_option` hands over to. Nothing is refused: `grove plugin set` still writes a value outside the list, and the panel remains the only party that decides what an unfamiliar one means. |
| `[panel.digest]` | nothing. The host draws the live digest frame and never reads this. It exists so the question "does this panel publish a digest" has an answer *before* anyone has opened the panel — which is exactly when it is asked, reading a roster or writing a drawer page. |
| `[panel.notebook]` | nothing. No path is resolved, created or fenced. A process the user approved writes wherever its own authority reaches, and pretending otherwise would dress a disclosure up as a sandbox. |

`[panel.digest]`'s absence means "declares none", **never** "publishes none":
every fragment written before the field existed lacks it, as does every
hand-written `[tui.plugins]` entry. Read it in the affirmative only.

### `[tool]`

A tool manifest replaces `[panel]` with `[tool]` and carries none of the
panel tables — no settings, keys, views, digest or notebook, because there is
no host to declare them to:

```toml
[tool]
binary   = "forge"           # installed command name; optional — defaults to
                             # the basename of build.binary
provides = [                 # required: the CLI phrases the tool answers to
  "forge up",
  "forge status",
  "forge down",
]
```

`binary` is a bare name, never a path — where the binary lands is the
installer's decision. `provides` is required, and it is written as *phrases*
because they are read by a human on the consent screen: "forge up" tells that
reader what they are approving where a bare token would not. The **first
token of each phrase is the dispatch verb**, checked for collisions at
install time ("Tools: the same pipeline, minus the panel wiring" above).

The unknown-key policy has a consequence here worth naming: a grove that
predates tools sees `tool.*` as unknown keys — warned about, not refused —
and would install such a manifest as a panel. The warning on the consent
screen is the tell, and declining there is the right move; `writing-a-tool.md`
tells authors to say so in their README.

## The lockfile

`~/.config/grove/plugins/plugins.lock.json`, one entry per installed plugin:

| Field | |
| --- | --- |
| `spec`, `url`, `ref` | what the user asked for and where it resolved to |
| `commit` | **the exact commit**, resolved once at install time |
| `dev` | a development install: built in place, unpinned |
| `manifest_digest`, `consent_digest` | what the approval is over |
| `consent` | the **whole `ConsentFacts` snapshot** — name, description, source, commit, build argv, run argv, env, protocol, icon, label, keys, views, notebook, digest description, settings keys |
| `source_dir`, `version_binary`, `binary`, `fragment` | the paths that were written |
| `installed_at` | when |

Nothing floats. A ref is resolved to a commit once, at install time; the
installed binary is copied out of that build; and rebuilds work from the
recorded commit. `grove plugin update` is the only thing that moves a pin, and
it re-resolves the *recorded* ref — so a plugin pinned to `v1.2.0` does not
move when upstream's `main` does, and moving it to `v1.3.0` is an explicit
`--ref`.

The consent snapshot is carried in full rather than reduced to its digest
because a reader comparing *declared* against *observed* — the keys a panel said
it would claim against the keys it claims at runtime — needs the declaration, and
this is the only place it is recorded after the install screen has scrolled away.
treemux's Plugins panel is that reader.

What the pin **cannot** say is which version the installed binary was actually
built from; that is recoverable by reading the bin-dir symlink back to
`versions/<name>/<commit|dev>/bin/…` (`Pin.BuiltCommit`), and `list --json`
exposes it as `built_commit`.

The file is JSON on purpose. `core/config` globs
`~/.config/grove/plugins/*.toml` and merges every match as configuration; a
lockfile named `.toml` in that directory would be parsed as a grove config
file. (`TestLockfileIsNotGlobbedAsAConfigFragment` pins that.)

## Install-time trust is the consent moment

An installed plugin is a process treemux spawns as you, every time it starts.
The decision to allow that is made once, on a screen showing:

- the source and the exact commit,
- the build command that will run in the checkout,
- the command treemux will run at every start, with its environment,
- the control-plane protocol it gets, and the hotkeys it declares,
- the views it can draw and which of them it means for a drawer,
- whether it publishes a digest, and what that digest says,
- the notebook subtree it writes into, if any,
- its settings and their default values,
- the two paths that will be written.

and it happens **before anything is built, written or trusted**. Declining
leaves the machine as it was found: no fragment, no lockfile, no binary, no
trust record, and not even the checkout if this run created it
(`TestInstallDeclinedWritesNothing`).

The approval goes into `core/pkg/exectrust` — the same MAC'd v2 store
`grove config trust` writes, keyed by the fragment path, with a digest over the
consent facts. This is reuse, not analogy: an installed plugin shows up in
`grove config trust --list` alongside every other exec-bearing config decision,
and there is no second trust store to audit.

Because the digest covers the pinned commit and the manifest bytes, **an
approval covers that pin and nothing else**. `grove plugin update` recomputes
it, finds it different, and re-prompts with a diff of what changed since you
approved — the build command, the run command, the environment, the protocol,
the key claims, the views, the digest, the notebook subtree, a retuned setting
default.

**Dev and pinned approvals are not interchangeable.** `dev` is part of the
digest, so approving a development build of a panel is not approving a pinned
one of the same name.

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

## On disk

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

## Commands

| Command | What it does |
| --- | --- |
| `grove plugin install <source>[@ref]` | clone, pin, ask, build, install, declare |
| `grove plugin install --dev <dir>` | build a local directory **in place**, unpinned |
| `grove plugin list` | every pin, its commit, and whether it is intact |
| `grove plugin outdated [name…]` | read-only: has each pinned ref moved upstream? |
| `grove plugin set <name> <key=value>…` | change an installed panel's settings |
| `grove plugin update [name…]` | re-resolve the recorded ref; diff; ask; move the pin |
| `grove plugin remove <name>` | fragment, binary, versions, checkout, pin, approval |

`--json` is available on all of them. A source may be `host/owner/repo`, any git
URL, or a path to a local repository.

### `install --dev` and the development loop

```console
$ grove plugin install --dev ~/Code/grove-plugins/grove-panel-foo
```

`--dev` installs a panel you are *writing*. It builds the directory **in place**
instead of cloning it, so the build sees whatever workspace you develop in — a
`go.work`, a `replace`, an unpublished sibling module — exactly as your own
`go build` does. A normal install builds in a managed checkout where none of that
applies, which is why a panel depending on an unreleased module can be built by
hand but not installed.

Nothing is pinned. The approval covers a **directory**, not a commit, and
`grove plugin update <name>` rebuilds whatever is in it at the time — uncommitted
work included. Removing a dev install never deletes your source.

The loop:

1. Edit.
2. `grove plugin update <name> --yes` — rebuild from the working tree. `--yes` is
   defensible here and only here: the manifest you are approving is one you wrote
   seconds ago.
3. **Restart the pane.** A plugin PTY spawns on first focus and then keeps
   running, so a pane opened before the rebuild goes on serving the old binary
   with nothing on screen to say so.

Steps 2 and 3 are what treemux's Plugins panel Dev page makes observable, link by
link, so "I rebuilt it and nothing changed" has an answer on screen. Note that for
a dev build a matching HEAD proves nothing — the build consumed uncommitted state
— so the honest comparison is of *times*, and a dirty tree is a question no check
can close.

### `outdated`

Asks each panel's remote whether the ref it is pinned to names a different commit
now. Read-only in the way that matters: it asks with `git ls-remote` and touches
no checkout, no binary and no pin. Nothing is fetched, nothing is built, nothing
moves — so it is safe to run while the panels are running.

```
NAME  PINNED        LATEST        STATE
hn    274ca8258f11  9f0c1a2b3d4e  outdated
```

| State | Means |
| --- | --- |
| `current` | the ref still names the pinned commit |
| `outdated` | the ref names something else now |
| `unreachable` | the remote could not be asked — offline, private, renamed |
| `dev` | a development install: built from a working tree, nothing pinned |

A remote that cannot be reached is **reported, not raised**. One unreachable
plugin must not fail the check for the rest, so the exit status is zero unless
the command itself was used wrongly.

`--json` rows carry `name`, `state`, `url`, `ref`, `dev`, `pinned`, `latest`, and
`reason` when there is one.

### `set`

```console
$ grove plugin set breaktimer work_minutes=30
$ grove plugin set hn feed.limit=50 feed.refresh=10m
```

Settings are the panel's own options — the `[panel.settings]` table its manifest
declares, handed to it over the control plane and re-delivered live when the
config reloads. Name them with the dotted paths the install screen showed.

**Why a command rather than a line in your own config:** `[tui.plugins]` merges
one *entry* at a time, so a later layer setting one option replaces the whole
panel entry, `command` and all, instead of adding to it. The installed fragment
is the only place these can live — and it is a file grove owns, whose contents
the install approval is bound to. Editing it by hand leaves the recorded consent
describing something else, which `list` reports as `edited` and the next `update`
silently reverts. So grove makes the edit and re-records the approval against
what it wrote.

Values are read as the type the panel declared: `25` stays a number, `true` stays
a boolean, `"2s"` is checked as a duration. A name the panel does not declare is
refused unless `--new` says to add it anyway.

Nothing is prompted — these are your settings, in a layer you own — but the
change is printed as the same diff an update would show.

### `list --json`

The table shows `NAME`, `SOURCE`, `PINNED`, `PROTOCOL`, `STATE`, where state is
`ok`, `dev`, `no rail item`, `no binary` or `edited`, and each fault is followed
by the command that repairs it. `--json` carries considerably more, because the
readers are programs — treemux's Plugins panel foremost:

| Field | |
| --- | --- |
| `name`, `source`, `ref`, `commit`, `dev` | identity and pin |
| `binary`, `fragment` | the paths |
| `protocol` | the approved control-plane protocol |
| `approved`, `intact` | the trust verdict, and whether both paths still exist |
| `installed_at`, `source_dir`, `version_binary` | when and where |
| `manifest_digest`, `consent_digest` | what the approval is over |
| `consent` | the **whole approval snapshot** — what the user was shown |
| `built_commit` | what the installed binary was actually built from, read back through the bin-dir symlink. Empty when the entry is missing or is not one of grove's version links |

`built_commit` is the one field the pin cannot answer for itself, and it is the
difference between "this is pinned at X" and "the thing running was built from
X".

`dev` is reported last in the table's `STATE` column so a genuinely broken dev
install still shows what is broken. It is a mode, not a fault, so it never joins
the remedy list — but it is never `ok` either: the binary on disk came from a
directory that has very likely moved on since, and the `PINNED` column is
suppressed (`—`) because printing the HEAD recorded at install time would read as
a pin and is not one.

## Deliberately not in v1

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
