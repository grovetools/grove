# Satellite infra target contract

`grove satellite up/down` provisions VMs through a terraform module. The
module normally comes from this package's embedded `targets/<target>/terraform`
tree (extracted per-satellite to `~/.local/state/grove/satellites/<name>/terraform`),
but `--tf-dir` swaps in a bring-your-own module directory, used as-is with no
extraction. **Any module honoring this contract works via `--tf-dir`** — the
CLI only speaks terraform variables and outputs; nothing below is
GCP-specific except where noted.

## Variables the CLI passes

`grove satellite up` runs `terraform apply` with these `-var` flags, and
persists them to `terraform.tfvars` in the module dir so `terraform destroy`
(from `grove satellite down`) resolves them non-interactively:

| Variable | Required in module | Passed | Notes |
|---|---|---|---|
| `project_id` | yes (no default) | always | Cloud project/account id. GCP-flavored name kept for now; a future non-GCP target must still accept the variable (it may ignore it). |
| `ssh_user` | yes (no default) | always | Login user the module must authorize with the operator's SSH public key. |
| `allowed_ssh_cidr` | yes (no default) | always | CIDR allowed to reach tcp/22. The embedded gcp module refuses `0.0.0.0/0` by validation; BYO modules should too. |
| `vm_name` | yes (default ok) | always | Instance name; also the per-satellite isolation key. `down` passes it again at destroy. |
| `zone` | yes (default ok) | when set | Placement zone/region override. |
| `service_account_email` | yes (default `""`) | when set | Attached identity for keyless cloud auth (GCP: ADC via metadata server). `""` must mean "attach nothing". |

Variables the embedded gcp module additionally declares, with defaults the
CLI never sets (tune them via `terraform.tfvars` edits or a BYO module):
`machine_type`, `disk_size_gb`, `image_family`, `image_project`,
`ssh_pubkey_file` (default `~/.ssh/id_ed25519.pub`), `service_account_scopes`
(default `["cloud-platform"]`), `zig_version`.

A module missing `variables.tf` is rejected before terraform runs.

## Outputs the CLI reads

| Output | Required | Used for |
|---|---|---|
| `external_ip` | **yes** | The only output `up` hard-requires: `terraform output -raw external_ip` yields the address that is host-key-scanned, bootstrapped over SSH, and written to the registry as `ssh_addr` (`<ip>:22`). |
| `ssh_command` | no | Convenience/informational. |
| `vm_name` | no | Informational. |
| `zone` | no | Informational. |

Output names are deliberately env-provider friendly: the daemon terraform env
provider upper-cases non-sensitive string outputs into `.env.local`
(`EXTERNAL_IP`, `SSH_COMMAND`).

## Image / host assumptions

The post-apply bootstrap (`bootstrap/satellite-bootstrap.sh`, embedded and
always taken from the grove binary — never from the module dir) runs over SSH
and assumes:

- a Debian-ish distro: `apt-get` present (the embedded gcp module's startup
  script installs build tools, gh, Go, zig via apt/tarballs; gcloud via snap
  shim or Google's apt repo),
- systemd, including `systemd --user` with lingering enable-able
  (`loginctl enable-linger`) — groved runs as a user unit, grove-syncd as a
  system unit,
- SSH public-key auth for `ssh_user`, reachable at `external_ip:22` from the
  operator's CIDR, with sudo,
- a startup/cloud-init mechanism that eventually touches
  `/var/lib/grove-satellite/startup-done` (the bootstrap polls this sentinel;
  the embedded module renders `startup.sh.tpl` into GCE instance metadata),
- outbound internet (clones, toolchain downloads, installers).

## State layout and lifecycle

- Default module dir: `~/.local/state/grove/satellites/<name>/terraform`.
  `up`/`down` re-extract the embedded module files there on every run (they
  version with the binary) but never touch `terraform.tfstate*`,
  `terraform.tfvars`, or `.terraform*` in that directory.
- `--tf-dir <dir>`: no extraction, no migration — the directory is yours,
  including its state. The embedded bootstrap script is still used.
- `templates/sync.toml.laptop` is reference material for a manual laptop-side
  sync setup; `grove satellite up` generates the laptop sync config itself.
