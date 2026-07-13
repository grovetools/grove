# grove satellite — operator runbook

A "grove satellite" is a real GCP VM running the full grove stack, the
notebook hydrated onto it via grove-syncd, and the VM driven from the laptop.
Spec and decision record: `plans/grove-satellite-poc/.artifacts/spec-grove-satellite-poc-20260710.md`
in the grovetools notebook.

This runbook began life as `cloud/poc/grove-satellite/README.md`. The
infrastructure assets it describes are now **embedded in the grove binary**
(`grove/cmd/satelliteassets/`) and extracted per satellite to
`~/.local/state/grove/satellites/<name>/terraform/` — each satellite gets its
own terraform state dir, and the bootstrap script can never version-skew from
the CLI. The preferred interface is `grove satellite up/down/status/list`;
the manual terraform/bootstrap steps below remain valid against the extracted
directory.

**Everything here creates billable GCP resources when applied.** Nothing runs
by itself — `grove satellite up` / `terraform apply` is always an explicit,
human-approved step.

## Layout (embedded in the grove binary)

| Path | What |
|---|---|
| `grove/cmd/satelliteassets/targets/gcp/terraform/` | One GCE VM (Ubuntu 24.04, `e2-standard-4`, 50 GB) + an SSH-only firewall pinned to your laptop's IP. Startup script installs Go (latest stable), zig (pinned), gh, gcloud (only if the image doesn't ship it), build tools. Optionally attaches a service account (`service_account_email`). |
| `grove/cmd/satelliteassets/bootstrap/satellite-bootstrap.sh` | Idempotent post-boot driver (laptop → VM over SSH): gh auth, clone superrepo + submodules, `grove build`, grove-syncd under systemd, token mint, groved under systemd --user with `pull=true` sync. Opt-in extras: Claude Code install + auth (`--claude`, `--claude-token-stdin`), dotfiles (`--dotfiles-repo`). |
| `grove/cmd/satelliteassets/templates/sync.toml.laptop` | Laptop-side push-only sync config, reference for manual setup (see activation ladder below — `grove satellite up` writes the laptop sync config itself). |
| `grove/cmd/satelliteassets/CONTRACT.md` | The target contract: variables/outputs a terraform module must honor, image assumptions, and the `--tf-dir` bring-your-own-module escape hatch. |

Extracted working copies (module + your satellite's `terraform.tfstate` /
`terraform.tfvars`): `~/.local/state/grove/satellites/<name>/terraform/`.
Module files are re-extracted (overwritten) on every `up`/`down`; the state
and var files are never touched.

## Secrets convention: stdin, never argv

Nothing secret goes in argv, terraform vars, or instance metadata (all three
are observable — `ps`, state files, the metadata server). Every token the
bootstrap needs is piped on **stdin**:

- one token flag (`--gh-token-stdin` *or* `--claude-token-stdin`): stdin is
  that raw token, e.g. `gh auth token | satellite-bootstrap.sh ...`
- both flags: stdin is order-free `KEY=VALUE` lines (blank lines and
  `#`-comments ignored):

  ```
  GH_TOKEN=<github token>
  CLAUDE_CODE_OAUTH_TOKEN=<token from `claude setup-token`>
  ```

On the VM the tokens land only in mode-0600 files under `$HOME`
(gh's own store; `~/.config/environment.d/claude.conf` +
`~/.config/grove-satellite/claude-env.sh`).

`grove satellite up` drives the same framing from the
`[satellites.<name>.provision]` config block (`gh_token_cmd`,
`claude_token_cmd`, ...) — token commands run locally before terraform, and
the tokens ride the bootstrap's stdin.

## GCP auth on the VM: attached service account, zero keys

Pass `-var service_account_email=<sa>@<project>.iam.gserviceaccount.com` at
apply time (or `service_account_email` in `[satellites.<name>.provision]`)
and the VM authenticates to GCP through the **metadata server**
(Application Default Credentials): `gcloud` and all client libraries pick it
up automatically, and **no credential files ever exist on disk** — nothing to
mint, copy, rotate, or leak. Access control lives in the SA's IAM roles;
scopes default to `cloud-platform` (`service_account_scopes`), which defers
entirely to IAM. Leave the email empty (default) for no GCP access at all.
The startup script guarantees `gcloud` is present AND on PATH: Ubuntu GCE
images usually ship it as a snap in `/snap/bin` (not on the default PATH), so
the script adds `/snap/bin` to the login PATH and symlinks the snap shim into
`/usr/local/bin/gcloud` for non-login shells; only when neither form exists
does it install `google-cloud-cli` from Google's apt repo.

## Optional bootstrap add-ons (all default OFF)

- `--claude` — install Claude Code via the official installer
  (`curl -fsSL https://claude.ai/install.sh | bash`; URL is a variable at the
  top of the bootstrap script). Install only, no auth.
- `--claude-token-stdin` — implies `--claude`; additionally reads a
  long-lived token (mint on the laptop with `claude setup-token`) from stdin
  and writes it as `CLAUDE_CODE_OAUTH_TOKEN` to (a)
  `~/.config/environment.d/claude.conf` so systemd --user units — groved and
  the agents it spawns — see it, and (b)
  `~/.config/grove-satellite/claude-env.sh` sourced from `~/.profile` /
  `~/.bashrc` for interactive shells. Both 0600. The live user manager gets
  the var immediately (`import-environment` + groved try-restart).
- `--dotfiles-repo <url>` — clone your dotfiles repo to a staging dir
  (`~/.local/share/grove-satellite/dotfiles`) and run its `install.sh`
  (which is expected to platform-detect GCP via the metadata server and run
  its `gcp/setup-linux.sh`). Private repos work through the gh credential
  helper set up in step 2 — the token never appears in argv or on-disk URLs.
  **Best-effort**: failures print a warning and never sink the bootstrap.

All add-ons are idempotent; re-running with the same flags is safe.

## Cost (approximate, us-east1)

- `e2-standard-4`: ~$0.13/hr ≈ $3.2/day ≈ $98/mo if left running
- 50 GB pd-balanced boot disk: ~$5/mo prorated
- An afternoon of play: well under $1. **Teardown when done** (below).

## M1 — provision + bootstrap

The one-command path (terraform apply + host-key pin + bootstrap + registry +
laptop sync setup):

```bash
grove satellite up mysat --project <PROJECT> --ssh-user $USER
# infra inputs persist to [satellites.mysat.infra]; later runs need no flags
```

Manual runbook (same module, extracted per satellite):

```bash
cd ~/.local/state/grove/satellites/<name>/terraform
terraform init

terraform apply \
  -var project_id=<PROJECT> \
  -var ssh_user=$USER \
  -var allowed_ssh_cidr="$(curl -fsS ifconfig.me)/32"
# optional GCP access from the VM (ADC via metadata server, no keys):
#   -var service_account_email=<SA>@<PROJECT>.iam.gserviceaccount.com

VM_IP=$(terraform output -raw external_ip)

# GitHub token scoped to repo read is enough; piped, never in argv:
gh auth token | ../bootstrap/satellite-bootstrap.sh "$USER@$VM_IP" --gh-token-stdin
```

(`../bootstrap/satellite-bootstrap.sh` is the embedded script `grove
satellite up` extracts to `~/.local/state/grove/satellites/<name>/bootstrap/`.)

Full 0→1 example — everything in one bootstrap run (gh + Claude Code +
dotfiles). Mint the Claude token once on the laptop with `claude
setup-token` (interactive, opens a browser):

```bash
CLAUDE_TOKEN=$(claude setup-token)   # or paste from its output

printf 'GH_TOKEN=%s\nCLAUDE_CODE_OAUTH_TOKEN=%s\n' \
    "$(gh auth token)" "$CLAUDE_TOKEN" \
  | ../bootstrap/satellite-bootstrap.sh "$USER@$VM_IP" \
      --gh-token-stdin --claude-token-stdin \
      --dotfiles-repo https://github.com/<you>/dotfiles.git
```

First boot takes a few minutes (toolchains), the bootstrap ~15–30 min
(ecosystem clone + full `grove build` incl. the zig libghostty build).
Re-running the bootstrap is safe; it skips completed steps.

Acceptance: script ends with `groved active`; `ssh $USER@$VM_IP 'bash -lc "grove build --json"'` green.

## M2 — light up sync (activation ladder)

The laptop is **push-only** (its notebook is never written by sync); only the
VM materializes. Back up the notebook first regardless:
`tar -czf /tmp/grovetools-notebook-backup-$(date +%Y%m%d).tar.gz -C ~/notebooks grovetools`

`grove satellite up` automates the laptop half: it verifies the sync token the
bootstrap fetched, writes/merges the push-only `~/.config/grove/sync.toml`,
and records `sync_local_port` in the registry so the laptop daemon forwards
note-sync over its pinned SSH connection — no manual tunnel. The manual
ladder, for standalone bootstrap runs:

```bash
# 1. tunnel (keep running)
ssh -N -L 8788:127.0.0.1:8788 $USER@$VM_IP &

# 2. laptop config — rung 1 syncs only the small `cloud` workspace
#    (template: grove/cmd/satelliteassets/templates/sync.toml.laptop)
cp <template> ~/.config/grove/sync.toml
# token was already written by the bootstrap to ~/.config/grove/sync.token

# 3. restart laptop groved so it registers the sync handler
groved upgrade   # or: restart your daemon however you normally do

# 4. rung 1: edit a note in the cloud workspace, watch it appear on the VM
nb quick "satellite rung-1 $(date +%s)" # from a cloud-workspace cwd
ssh $USER@$VM_IP 'find ~/notebooks/grovetools/workspaces/cloud -newer /var/lib/grove-satellite/startup-done -name "*.md"'

# 5. rung 2: uncomment the grovetools workspace in ~/.config/grove/sync.toml,
#    restart groved again, watch the real plans/concepts hydrate VM-side.
```

Acceptance: laptop edit visible on VM in seconds; conflict test (edit both
sides) produces a conflict artifact, not a silent overwrite; a planted fake
secret is quarantined pre-push; laptop tree byte-identical throughout.

## M3 — drive the VM from a local pane

```bash
ssh -t $USER@$VM_IP 'bash -lc treemux'
```

Run it inside a local treemux pane — the VM's treemux (hydrated workspace
tree included) renders byte-transparently.

> Note (discovered 2026-07-10): groved's embedded SSH server (`[daemon.ssh]`,
> port 2222) is enabled on the VM but its session spawn path is stale — it
> execs a `groveterm` binary with `--follower/--workspace` flags that nothing
> in-tree builds or parses anymore (`daemon/internal/daemon/ssh/server.go:124`,
> `resolveCommandArgs`). Plain sshd + treemux is the PoC path; fixing the wish
> target is a follow-up gap.

## M4 — run a flow job on the VM, driven from the laptop

Inside the M3 session (or plain ssh), in the hydrated workspace:

```bash
cd ~/code/grovetools
flow plan init satellite-demo
flow plan add --title "hello-satellite" --type shell -p 'echo "hello from the satellite: $(hostname)" && date'
flow plan run --background
flow plan status --json
```

The job's plan files land in the VM's notebook tree → sync pushes them back
→ they appear in the laptop notebook (reverse direction of M2). That closes
the loop: VM exists → notebook hydrated → agent driven from laptop → results
visible at home. (For a real claude agent job, bootstrap with
`--claude-token-stdin` — Claude Code gets installed and
`CLAUDE_CODE_OAUTH_TOKEN` reaches groved's children via
`~/.config/environment.d/claude.conf`. Alternatively export a low-limit
`ANTHROPIC_API_KEY` yourself.)

## Teardown

```bash
grove satellite down <name>
```

(`down` runs terraform destroy in the satellite's state dir, removes the
provisioning state entry, and reminds you to restart the daemon. Manual
equivalent: `cd ~/.local/state/grove/satellites/<name>/terraform && terraform
destroy` — the `terraform.tfvars` written by `up` supplies
project_id/ssh_user/allowed_ssh_cidr.)

Disk, IP, and firewall are all terraform-managed — destroy removes
everything. Sync state on the laptop lives in ~/.local/share/grove/sync/
(rebuildable; delete to reset). The laptop sync config
(~/.config/grove/sync.toml) and token (~/.config/grove/sync.token) are kept —
remove them manually if unwanted.

## Legacy state migration (pre-embed provisions)

Satellites provisioned before the assets moved into the binary kept their
tfstate in the shared worktree dir `cloud/poc/grove-satellite/terraform/`.
The first `grove satellite up/down <name>` run from inside that ecosystem
(any subdirectory) detects a tfstate there whose `terraform.tfvars` names
`<name>` as `vm_name`, copies `terraform.tfstate*` + `terraform.tfvars` into
`~/.local/state/grove/satellites/<name>/terraform/`, and prints a notice. The
originals remain as a backup only — do not run terraform against the legacy
directory afterwards.

## Notebook restore (if sync ever misbehaves)

```bash
tar -xzf /tmp/grovetools-notebook-backup-<date>.tar.gz -C ~/notebooks
```
