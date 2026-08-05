# Forge infra contract

`grove forge up/plan/down` provisions ONE services VM through a terraform
module. The module normally comes from this package's embedded `terraform/`
tree (extracted to `~/.local/state/grove/forge/terraform`), but `--tf-dir` swaps
in a bring-your-own module directory, used as-is with no extraction. **Any
module honoring this contract works via `--tf-dir`** — the CLI only speaks
terraform variables and outputs.

This is the sibling of `grove/cmd/satelliteassets/CONTRACT.md`, and the
differences are the point:

| | satellite | forge |
|---|---|---|
| lifecycle | cattle — `down` is routine | **pet** — `down` needs `--force` AND the typed name |
| state | registry is the truth | the boot disk is the truth (durable refs + SQLite) |
| identity | ephemeral | stable; a backup/restore drill gates production use |
| count | many, named | one |

## Variables the CLI passes

`grove forge up`/`plan` runs terraform with these `-var` flags and persists them
to `terraform.tfvars` in the module dir so `terraform destroy` (from
`grove forge down`) resolves them non-interactively.

| Variable | Required in module | Source |
|---|---|---|
| `project_id` | yes (no default) | `[forge.infra] project` |
| `ssh_user` | yes (no default) | `[forge.infra] ssh_user` |
| `allowed_cidr` | yes (no default) | `[forge.infra] cidr` — **must refuse `0.0.0.0/0` by validation** |
| `vm_name` | yes (default ok) | `[forge.infra] vm_name`, default `grove-forge` |
| `zone` | yes (default ok) | `[forge.infra] zone` |
| `machine_type` | yes (default ok) | `[forge.infra] machine_type`, default `e2-medium` |
| `disk_size_gb` | yes (default ok) | `[forge.infra] disk_size_gb` |
| `image_family` / `image_project` | yes (default ok) | `[forge.infra]`, default `debian-12` / `debian-cloud` |
| `ssh_pubkey_file` | yes (default ok) | `[forge.infra] ssh_pubkey_file` |
| `service_account_email` | yes (default `""`) | `[forge.infra] service_account_email`; `""` **must** mean "create a dedicated identity", not "attach the project default" |
| `enable_iap_ssh` | yes (default ok) | `[forge.infra] enable_iap_ssh` |
| `domain` | yes (default `""`) | `[forge.services] domain` |
| `tls_mode` | yes (default `self-signed`) | `[forge.services] tls_mode` |
| `acme_email`, `acme_dns_provider` | yes (default `""`) | `[forge.services]` |
| `forgejo_version` | yes (no default) | `[forge.services.forgejo] version` |
| `forgejo_sha256` | yes (no default) | `[forge.services.forgejo] sha256` |
| `forgejo_http_port`, `forgejo_site_name` | yes (default ok) | `[forge.services.forgejo]` |
| `syncd_enabled`, `syncd_port` | yes (default ok) | `[forge.services.syncd]` |

Variables the embedded module additionally declares, which the CLI never sets:
`network`, `service_account_scopes` (default `[]` — the no-scope posture),
`backup_enabled` (default `false`), `backup_bucket`, `backup_location`,
`backup_retention_days`, `backup_nearline_days`, `backup_noncurrent_days`,
`forgejo_extra_cidrs`, `syncd_extra_cidrs`.

## Outputs the CLI reads

| Output | Required | Used for |
|---|---|---|
| `external_ip` | **yes** | The address `up` host-key-scans, ships binaries to, and records. |
| `forge_url` | no | Printed by `status`; the value that belongs in `[forge] url`. Domain-less deployments are IAP-tunnel-only, so this is `http://localhost:<port>`. |
| `forgejo_tunnel_command` | no | Printed by `up`/`status` when non-empty: the IAP tunnel that makes `forge_url` answer on a domain-less deployment. |
| `syncd_addr` | no | Printed by `status`; the endpoint whose TLS fingerprint is pinned. |
| `backup_bucket` | no | The bucket `grove forge backup`/`restore` read and write; empty when backups are off. |
| `tls_mode` | no | Printed by `status`. |
| `service_account_email` | no | Printed by `status` (audit: is this the dedicated one?). |
| `firewall_rules` | no | Printed by `status` — the whole exposed surface in one place. |
| `ssh_command`, `vm_name`, `zone` | no | Informational. |

Output names are env-provider friendly for the same reason the satellite
module's are: the daemon terraform env provider upper-cases non-sensitive
string outputs into `.env.local`.

## Security invariants a BYO module must also honor

These are not preferences; the trial's hardening ledger is where each came from.

1. **No `0.0.0.0/0` ingress on any port.** Every rule is tag-scoped to the
   instance and sourced from the operator CIDR (optionally plus Google's IAP
   range `35.235.240.0/20` for SSH). The embedded module enforces this with
   variable validations, including on the "extra CIDRs" escape hatches.
   Forgejo's plain-HTTP port goes further: it admits ONLY the IAP range —
   cleartext never crosses the open wire, and the operator reaches it through
   `forgejo_tunnel_command`. VM-to-VM consumers (CI runners) use the VPC's
   internal address, which needs no rule from this module.
2. **A dedicated identity with no scopes.** `service_account_email = ""` creates
   a service account with no IAM roles and attaches it with
   `service_account_scopes = []`. The default compute SA can read every GCS
   bucket in the project; the forge needs no GCP API access at all.

   `backup_enabled = true` is the ONE deliberate widening, and it widens by
   exactly two things: the OAuth scope `devstorage.read_write` (not
   `cloud-platform` — the metadata server can mint a token that speaks GCS and
   nothing else) and one `roles/storage.objectAdmin` binding on the backup
   bucket alone (not a project-wide role). Scope is the coarse cap, IAM says
   which bucket; neither alone would be enough. A bring-your-own module that
   grants project-level storage roles does not meet this contract.
3. **TLS on every non-loopback listener.** `grove-syncd` refuses a non-loopback
   bind without `--tls-cert/--tls-key` (sync/cmd/serve.go), and the unit this
   module writes never passes `--insecure`.
4. **ACME is DNS-01 only.** HTTP-01 would require opening `:80` to the world,
   which invariant 1 forbids. With no domain (or no DNS provider) the mode is
   self-signed, and the certificate is pinned by the fingerprint
   `grove forge status` reads off the wire.
5. **Pinned, checksummed downloads.** Forgejo is installed by version + SHA-256.
   `forgejo_sha256` is validated as 64 hex characters before terraform runs.

## Image / host assumptions

- Debian-ish with `apt-get`, systemd, `openssl`, `curl`, `sqlite3`.
- A startup/cloud-init mechanism; the embedded module renders `startup.sh.tpl`
  into GCE instance metadata and the script marks completion by touching
  `/var/lib/grove-forge/startup-done`.
- Outbound internet for the Forgejo release download (and lego, under ACME).
- **No VM-side toolchain.** Unlike a satellite, this host installs no Go, no
  zig, no `gh` and no gcloud: `grove-syncd` arrives cross-built from the laptop
  (`grove forge up --prebuilt`, the satellite `--prebuilt` precedent), and the
  unit's `ConditionPathExists` holds it until the binary lands.

## State layout and lifecycle

- Default module dir: `~/.local/state/grove/forge/terraform` (singular — there
  is one forge). `up`/`plan`/`down` re-extract the embedded module files there
  on every run so they version with the binary, and never touch
  `terraform.tfstate*`, `terraform.tfvars`, or `.terraform*`.
- `--tf-dir <dir>`: no extraction — the directory is yours, including its state.
