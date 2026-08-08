# Write a grove tool

A **tool plugin** is a CLI program that `grove plugin install` puts on a
machine and that grove then dispatches to by name, git-subcommand-style:
install a plugin whose manifest provides `forge up`, and `grove forge up`
runs it. It is the second plugin kind. The first — the treemux sidecar panel —
has its own guide, "Write your first grove panel" in treemux's docs; this page
is that guide's sibling for tools.

It lives here in grove's docs rather than beside its sibling because grove
owns the whole tool surface: the installer, the consent screen and the
`grove <verb>` dispatch are all grove's, and treemux never learns a tool
exists.

The machinery a tool shares with panels — the pipeline, the lockfile, the
trust store — is documented once, in `09-plugins.md` in this directory. This
page covers what a tool author needs, and repeats the shared parts only where
the tool view differs. **Sections are cited by name here**, as they are in
`09-plugins.md` and the panel guide: section numbers have moved before, and a
stale number is a broken reference nobody notices.

---

## What a tool is, and is not

A tool is an **ordinary CLI binary**. It is built from a pinned commit,
installed into grove's managed bin dir, and run when the user types its verb —
with the user's full authority, their environment, their credentials, their
network. Between invocations it does not exist as a process.

Three things it is not, each worth being clear-eyed about before you write
one:

- **Not a panel.** No pane, no PTY, no socket, no control-plane protocol, no
  rail item, no hotkey claims. Nothing about a tool appears in treemux. If
  your program wants a live pane, write a panel; the same repository cannot be
  both (see "One manifest, two kinds" below).
- **Not an extension API.** grove hands your binary the rest of the argv and
  reads back an exit code. There is no capability system, no sandbox, and no
  host to negotiate with — which means the consent screen at install time is
  the *entire* boundary between the user and what your tool can do. That is
  why the consent copy for tools is franker than for panels ("What installing
  a tool means" below).
- **Not settings-managed.** `[panel.settings]` and `grove plugin set` are
  panel affordances — a table delivered over a control plane a tool does not
  have. A tool reads its own config the way any CLI does, from wherever its
  own conventions say.

## One manifest, two kinds

`grove-plugin.toml` declares **exactly one** of `[panel]` or `[tool]`, and the
section present is what decides the kind. There is no `kind` field and no
schema bump: a manifest that says what it is by carrying the tables for it
needs no second declaration to disagree with the first, and `schema_version`
stays 1 because nothing an existing manifest says has changed meaning.

The cost of that choice is carried by older groves, and you should know what
it looks like. A grove predating tools treats `tool.*` as unknown keys —
**a warning, not an error** (see "`grove-plugin.toml`" in `09-plugins.md` for
why that policy exists) — and would install your manifest as a panel: your CLI
wired into a pane it was never meant to draw in. The tell is on the consent
screen, where `tool.*` is listed under unknown keys; a user who sees that
should decline, and it is worth saying so in your README, because the consent
screen is the one place they are guaranteed to be reading.

## The manifest

The running example is the forge recipe — a tool that provisions and tends
cloud VMs from the grove CLI, installed command name `forge`:

```toml
schema_version = 1

[plugin]
name        = "forge-gcp"    # ^[a-z0-9][a-z0-9-]{0,63}$ — filename, lockfile key
description = "Provision and tend GCP satellite VMs from the grove CLI"
homepage    = "https://github.com/you/grove-plugin-forge-gcp"   # optional

[build]
command = ["go", "build", "-o", "bin/forge", "."]   # argv, run in the checkout
binary  = "bin/forge"                               # required, stays inside the checkout

[tool]
binary   = "forge"           # the installed command name; optional — defaults
                             # to the basename of build.binary, so this line
                             # is redundant here and shown for the annotation
provides = [                 # required: the phrases the tool answers to
  "forge up",
  "forge status",
  "forge down",
]
```

Everything above `[tool]` is the shared manifest surface, held to the same
validation the panel kind is — name shape, required description, argv-only
build command, binary confined to the checkout, control characters refused in
anything a consent screen will print. The rules and their reasons are in
"`grove-plugin.toml`" in `09-plugins.md`; none of them bend for tools.

### `binary`

The bare command name the binary is installed under in grove's managed bin
dir. It defaults to the basename of `build.binary`, which is usually what you
want — declare it only when the artifact your build produces is not named what
the user should type. It is a *name*, never a path: where the binary lands is
the installer's decision, not the manifest's.

Note that `plugin.name` and `tool.binary` are different facts and are allowed
to differ, as they do above: the plugin is `forge-gcp` — the name in
`grove plugin list`, the lockfile and the removal command — while the thing
dispatched to is `forge`. One plugin, one binary; if you find yourself wanting
two binaries, you have two plugins.

### `provides`, and what a verb is

`provides` is **required** and is the list of CLI phrases your tool answers
to — written as phrases (`"forge up"`, not `"up"`) because they are read by a
human on the consent screen, and "forge up" tells that reader what they are
approving where a bare token would not.

The **first token of each phrase is the dispatch verb**, and the verb is what
the mechanism acts on: `grove forge …` resolves to your installed binary and
hands it the rest of the argv. The phrases past the first token are honest
disclosure, not routing — your binary owns its own subcommand parsing, and a
phrase you forgot to list still works. List the phrases anyway, and keep them
current: they are what the user consented to, and an update that changes them
re-opens the prompt with a diff.

A single tool usually claims a single verb, as forge does. Distinct first
tokens claim distinct verbs, and every claimed verb is checked at install
time (next section).

## Build expectations

The build contract is the panel one, unchanged, and the reasons transfer
whole:

- **`build.command` is argv, never a shell string.** The consent screen shows
  it verbatim, and `sh -c "…"` would hide what runs behind a shell.
- **`build.command` is optional.** Omit it and `build.binary` must already
  exist in the checkout — which is how an interpreted tool, written in bash or
  Python, installs on a machine with no toolchain and no build step at all.
- The build runs in a **managed checkout** at the pinned commit, not in your
  working tree. A `go.work` or `replace` that your own `go build` sees does
  not exist there; a tool that needs one is developed under `--dev` ("The
  lifecycle, from your side" below) and released only once its dependencies
  are published.

## Dispatch, and the three collisions

`grove <verb> …` resolves through grove's record of installed tools — the
lockfile pin, not the config layer — to the binary the pin names, and execs it
with the remaining arguments. Resolution is by verb, so it survives renames of
nothing: the verb your manifest provided is the verb the user types.

The verb namespace is shared, and every collision is handled at **install
time, loudly**, because a dispatch surface that resolves ambiguously is worse
than an install that fails:

- **Two tools claiming the same verb**: the second install is refused, naming
  both plugins. There is no priority order and no shadowing between tools —
  the user uninstalls one or the tools pick different verbs.
- **A verb that names a grove built-in command**: refused. grove's own
  commands are not up for claiming.
- **A verb that names a registered ecosystem tool** (the compiled-in roster
  grove already delegates to): refused, same reason.

The one collision install-time checking cannot prevent is a *future* grove
release adding a built-in over a verb you already claimed — that is a degraded
state, not an error, and it is covered under "Degraded states" below.

When a verb resolves but the binary is missing — deleted, or its build failed
during an update — the user gets a friendly error naming the plugin that
provides the verb and suggesting `grove plugin update <name> --force`, rather
than a bare "command not found" that points at nothing.

## What installing a tool means

The pipeline is the panel pipeline — clone at a ref, resolve to an exact
commit, consent, build, binary into the managed bin dir, pin in the lockfile,
approval into the exec-trust store. What a tool install **skips** is the panel
wiring: no `[tui.plugins]` pane entry is written, and nothing appears in
treemux. The per-plugin file under `~/.config/grove/plugins/` is still
written, because it is the trust anchor the approval is keyed by, the unit
`remove` deletes, and the thing a user can move aside to disable your tool by
hand — it just carries no pane.

The consent screen is where the kinds genuinely part company. A panel's worst
case is bounded by what a pane can do: it claims a hotkey, draws in a
rectangle, runs as a process the user watches. A tool's worst case is not
bounded by anything grove controls. The first consumer of this kind makes the
point concretely: `forge acme install-credentials` reads a GCP
service-account key from disk and ships it over SSH to a VM. That is the
*intended* behavior of a well-behaved tool — and it is why the tool consent
screen uses franker copy than the panel one. Structurally it shows:

- the source and the exact commit,
- the build command that will run in the checkout,
- the command name that will be installed, and the phrases it provides,
- the paths that will be written,

and it says what those facts mean in the honest register: **"This plugin can
read your cloud credentials and change your infrastructure."** Not because
every tool does, but because nothing stops one that wants to, and a consent
screen that undersells that is not consent. Write your `description` and your
`provides` phrases for the reader of that screen, and do not resent the frank
copy around them: a user who installs your tool having read it is a user whose
trust you actually have.

Everything `09-plugins.md` says under "Install-time trust is the consent
moment" holds for tools: declining leaves the machine untouched, the approval
is bound to the pinned commit and the manifest bytes, dev and pinned approvals
are not interchangeable, and an update that changes anything the user was
shown re-asks with a diff.

## The lifecycle, from your side

The commands are the same five (see "Commands" in `09-plugins.md`), and
`install`, `update`, `remove` and `outdated` behave identically for tools.
The author-facing differences:

- **Tag your releases.** An install pins whatever the ref resolves to and
  never floats; a repo with no tags leaves your users pinned to a commit hash
  they cannot reason about. This is the panel advice verbatim, and it matters
  more here — a user auditing "which forge do I have" is asking a security
  question.
- **The dev loop is shorter than a panel's.** `grove plugin install --dev
  <dir>` builds your working tree in place, uncommitted work included, and
  then the loop is: edit, `grove plugin update <name> --yes`, run
  `grove <verb>` again. There is no pane to restart — every invocation execs
  the binary as it is *now* — so the panel guide's third step does not exist.
  The one residue of it: an invocation already running when you rebuild keeps
  the old binary until it exits, as any exec'd process would.
- **`update` re-asks when it should.** The approval digest covers the build
  command, the installed name and the `provides` list, so a release that
  changes any of them re-opens the prompt with a diff. Growing `provides` is
  the common case — a new verb-claiming phrase is exactly the kind of change a
  user should see — and a new *verb* is additionally checked for collisions at
  that moment, like a fresh install.
- **`remove` is complete.** The binary, the versions, the checkout, the pin,
  the fragment and the approval all go; the verb stops resolving with the
  same friendly error a missing binary gets, until nothing provides it at
  all.

## Degraded states

A tool has no runtime for treemux to watch, so its health is answered by
`grove plugin list`, the same place a panel's is, with the same policy: each
fault names the command that repairs it. Structurally, the states specific to
tools:

| State | Means | Repair |
| --- | --- | --- |
| binary missing | the bin-dir entry is gone, or the last build failed | `grove plugin update <name> --force` — rebuild from the recorded commit |
| verb shadowed | a later grove release added a built-in with your verb's name; **the built-in wins** | rename the verb in a release; users update to it |

The shadowing rule is deliberate and worth internalizing as an author: grove's
own command surface grows, install-time checking can only see the built-ins
that exist when the check runs, and a plugin must never be able to sit in
front of a grove command — that direction would let an installed tool
intercept grove itself. The state is surfaced rather than silent, so the user
learns their verb went dark from `list`, not from wondering. Choosing a verb
distinctive enough not to be a plausible future grove subcommand (`forge`,
not `deploy`) is the cheap insurance.

## Out of scope

Three things a tool plugin deliberately is not, so you do not design toward
them:

- **Bundled skills.** Skills have their own sync surface today, and the
  installer does nothing with a `skills/` directory in a tool repo — no sync,
  no consent line, no removal. A tool that wants to ship skills ships them
  the way skills ship.
- **Provider plugins.** Model providers (the `grove-anthropic` /
  `grove-gemini` shape) are compiled into the ecosystem, not installed into
  it. `[tool]` does not open that door.
- **Daemon and service kinds** — infra pollers, notify channels, hook
  handlers: anything that runs unattended or is called *by* grove rather than
  by the user. These are deferred, not declined, and the blockers are named in
  the extensibility tier ladder
  (`concepts/grove-extensibility/tier-ladder.md`): the section "services: name
  the convention" proposes the per-kind naming (`grove-<kind>-<name>`) such
  services would install under, and "the security work" names what they are
  waiting on — extension identity and capability manifests, which anything
  that touches groved needs and which a user-invoked CLI, whose every run is
  the user's own act, does not.

The line all three draw is the same one: a tool runs **when the user types its
verb, as the user, and not otherwise**. That is the shape this kind covers,
and the consent story above is honest precisely because it is the only shape
it covers.

## Where things live

| | |
| --- | --- |
| Distribution design — pipeline, lockfile, trust | `09-plugins.md`, this directory |
| Manifest schema, lockfile, approval check | `core/pkg/plugin` |
| Installer — clone, build, install, record, remove | `grove/pkg/plugin` |
| Dispatch — `grove <verb>` resolution | grove's root command (`grove/cmd`) |
| The sibling guide for panels | treemux `docs/writing-a-panel.md` |
