# The grove forge SERVICES VM: Forgejo + grove-syncd, colocated.
#
# This is the pet, not the cattle. `grove satellite` provisions disposable build
# hosts whose truth lives in the registry; this module provisions ONE host that
# accumulates durable git refs and therefore has an identity, a backup story,
# and a destroy path deliberately harder to walk than `satellite down`.
#
# Security posture encoded here, item by item, from the trial's hardening ledger
# (plan hosted-git-and-prs, inbox note 20260802-hosted-git-prs-deferred-infra):
#
#   1. TLS or tunnel-only for the service ports — grove-syncd terminates TLS
#      (modules/syncd renders the material; self-signed is pinned by
#      fingerprint, ACME is DNS-01 only), and Forgejo's plain-HTTP :3000 is
#      reachable ONLY through Google's IAP TCP-forwarding range: the operator
#      rides an authenticated, encrypted IAP tunnel, never the open wire.
#   2. No 0.0.0.0/0 ingress ANYWHERE. Every rule is tag-scoped to this instance
#      and sourced from the operator CIDR (SSH, syncd) or the IAP range
#      (SSH, Forgejo). The variable validations refuse the open internet
#      outright.
#   3. A dedicated service account with no IAM roles, attached with no OAuth
#      scopes — closing the trial's "the default compute SA can read every
#      bucket in the project" finding.
#   4. Narrow tokens: not a terraform concern, but nothing in this module ever
#      receives one. The forge API token lives on the laptop, is resolved by the
#      daemon, and never reaches the VM.

terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
  }
}

provider "google" {
  project = var.project_id
}

data "google_compute_image" "os" {
  family  = var.image_family
  project = var.image_project
}

locals {
  # Google's IAP TCP-forwarding source range. Reaching a port through it needs
  # an IAM grant as well, so admitting it here widens nothing on its own — it
  # just means `gcloud compute start-iap-tunnel` (and `ssh
  # --tunnel-through-iap`) keeps working when the laptop's public IP rotates.
  iap_cidr = "35.235.240.0/20"

  # A zone's region is its zone minus the trailing "-<letter>".
  region = join("-", slice(split("-", var.zone), 0, 2))

  ssh_source_ranges = (
    var.ssh_ingress == "iap" ? [local.iap_cidr] :
    var.ssh_ingress == "cidr" ? [var.allowed_cidr] :
    var.enable_iap_ssh ? [var.allowed_cidr, local.iap_cidr] : [var.allowed_cidr]
  )
  # Forgejo speaks plain HTTP, so its port never opens to the operator CIDR:
  # admin passwords, tokens and git auth would cross the internet in cleartext.
  # IAP-only means every laptop byte transits Google's encrypted, IAM-gated
  # frontend; VM-to-VM traffic (CI runners) rides the VPC's internal range and
  # needs no rule here at all.
  forgejo_source_ranges = concat([local.iap_cidr], var.forgejo_extra_cidrs)
  syncd_source_ranges   = concat([var.allowed_cidr], var.syncd_extra_cidrs)

  # A domain-less instance has no name to put in a certificate and no zone to
  # answer a DNS-01 challenge in, so ACME is not on the table; degrade rather
  # than render a plan that cannot succeed.
  tls_mode = var.domain == "" ? "self-signed" : var.tls_mode

  service_account_email = var.service_account_email != "" ? var.service_account_email : one(google_service_account.forge[*].email)

  # The no-scope default (item 3 above) is absolute until backups are turned
  # on, and then it widens by exactly one entry. devstorage.read_write, not
  # cloud-platform: the metadata server can mint a token that speaks GCS and
  # nothing else, and backup.tf's single bucket-scoped IAM binding decides
  # which bucket that token may touch. Scope is the coarse cap; IAM is the
  # authority. Neither alone would be enough.
  service_account_scopes = var.backup_enabled ? distinct(concat(var.service_account_scopes, [
    "https://www.googleapis.com/auth/devstorage.read_write",
  ])) : var.service_account_scopes
}

# ---- identity -------------------------------------------------------------

resource "google_service_account" "forge" {
  count = var.service_account_email == "" ? 1 : 0

  account_id   = var.vm_name
  display_name = "grove forge services VM"
  description  = "Dedicated identity for ${var.vm_name}. Granted NO IAM roles and attached with NO OAuth scopes: the forge needs no GCP API access, so a compromised VM has no project-wide read."
}

# ---- address ---------------------------------------------------------------

# A RESERVED address, not an ephemeral one — and this is a correctness
# requirement, not a convenience.
#
# With tls_mode = "self-signed" the certificate generated at first boot carries
# the external IP as its only SAN, and clients pin it ([sync] ca_cert). An
# ephemeral address is released on every instance STOP, so the instance comes
# back on a different IP and every pinned client fails verification — not with
# "cannot connect", which is diagnosable, but with a certificate error against a
# service that is up and healthy.
#
# That was theoretical while nothing ever stopped the forge. It stopped being
# theoretical the moment `allow_stopping_for_update` made a stop part of a
# routine converge, and it was never really theoretical at all: GCE host
# maintenance can stop an instance without anyone asking it to. Reserving the
# address costs the same ~$3.65/mo the in-use ephemeral one already cost.
resource "google_compute_address" "forge" {
  name         = "${var.vm_name}-ip"
  project      = var.project_id
  region       = local.region
  address_type = "EXTERNAL"
  description  = "Stable external address for the grove forge. Self-signed TLS pins this IP as its SAN, so it must survive a stop."
}

# ---- network ---------------------------------------------------------------

resource "google_compute_firewall" "ssh" {
  name        = "${var.vm_name}-allow-ssh"
  network     = var.network
  description = "Operator SSH to the grove forge${var.enable_iap_ssh ? ", plus Google IAP TCP forwarding" : ""}"

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  source_ranges = local.ssh_source_ranges
  target_tags   = [var.vm_name]
}

resource "google_compute_firewall" "forgejo" {
  count = var.forgejo_ingress_enabled ? 1 : 0

  name        = "${var.vm_name}-allow-forgejo"
  network     = var.network
  description = "Forgejo HTTP, IAP TCP-forwarding range only — plain HTTP never crosses the open wire. Operator access is an IAP tunnel (see the forge_url output); forgejo_extra_cidrs opts into direct reach."

  allow {
    protocol = "tcp"
    ports    = [tostring(var.forgejo_http_port)]
  }

  source_ranges = local.forgejo_source_ranges
  target_tags   = [var.vm_name]
}

resource "google_compute_firewall" "syncd" {
  count = var.syncd_enabled && var.syncd_ingress_enabled ? 1 : 0

  name        = "${var.vm_name}-allow-syncd"
  network     = var.network
  description = "grove-syncd (TLS). Operator CIDR plus any other grove nodes replicating the notebook."

  allow {
    protocol = "tcp"
    ports    = [tostring(var.syncd_port)]
  }

  source_ranges = local.syncd_source_ranges
  target_tags   = [var.vm_name]
}

# ---- services --------------------------------------------------------------

module "forgejo" {
  source = "./modules/forgejo"

  release   = var.forgejo_version
  sha256    = var.forgejo_sha256
  http_port = var.forgejo_http_port
  site_name = var.forgejo_site_name
  domain    = var.domain
  tls_mode  = local.tls_mode
}

module "syncd" {
  source = "./modules/syncd"
  count  = var.syncd_enabled ? 1 : 0

  port              = var.syncd_port
  domain            = var.domain
  tls_mode          = local.tls_mode
  acme_email        = var.acme_email
  acme_dns_provider = var.acme_dns_provider
}

# ---- machine ---------------------------------------------------------------

resource "google_compute_instance" "forge" {
  name         = var.vm_name
  machine_type = var.machine_type
  zone         = var.zone
  tags         = [var.vm_name]

  # GCE refuses to change a running instance's OAuth scopes (and its machine
  # type) — the API requires a stop first. Without this, turning backups on
  # fails the apply with "Changing the service account scopes ... requires
  # stopping the instance", and the forge's scopes could never be converged at
  # all: the only path left would be destroy-and-recreate, which for a PET is
  # the accident this whole module is shaped to prevent.
  #
  # This permits a stop/start, not a replacement. The boot disk — which is the
  # forge's entire value, every ref and the PR database — survives a stop
  # untouched. Terraform stops the instance only for changes GCE cannot make
  # live, and destruction stays gated where it always was: `grove forge down`,
  # behind --force plus the instance name typed back.
  allow_stopping_for_update = true

  # A rolling image family selects the OS only when this pet is first created.
  # OS replacement or upgrades require an explicit, separately designed data
  # migration; they must never happen incidentally because the family advanced.
  # Keep this path exact: disk size/type and every other VM field must converge.
  lifecycle {
    ignore_changes = [
      boot_disk[0].initialize_params[0].image,
    ]
  }

  # The pet's disk is its state. Deleting the instance without deleting this
  # disk is the difference between a rebuild and a data loss, so `grove forge
  # down` double-confirms and the disk is called out in its prompt.
  boot_disk {
    initialize_params {
      image = data.google_compute_image.os.self_link
      size  = var.disk_size_gb
      type  = "pd-balanced"
    }
  }

  network_interface {
    network = var.network

    access_config {
      nat_ip = google_compute_address.forge.address
    }
  }

  service_account {
    email  = local.service_account_email
    scopes = local.service_account_scopes
  }

  metadata = {
    ssh-keys = "${var.ssh_user}:${file(pathexpand(var.ssh_pubkey_file))}"
    startup-script = templatefile("${path.module}/startup.sh.tpl", {
      ssh_user      = var.ssh_user
      tls_mode      = local.tls_mode
      domain        = var.domain
      forgejo_setup = module.forgejo.setup_script
      syncd_setup   = var.syncd_enabled ? one(module.syncd[*].setup_script) : ""
    })
  }
}
