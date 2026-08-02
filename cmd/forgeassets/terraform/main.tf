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
#   1. TLS or tunnel-only for the service ports — every service terminates TLS
#      (modules/syncd renders the material; self-signed is pinned by
#      fingerprint, ACME is DNS-01 only).
#   2. No 0.0.0.0/0 ingress ANYWHERE. Every rule is tag-scoped to this instance
#      and sourced from the operator CIDR, optionally plus Google's IAP range
#      for SSH. The variable validations refuse the open internet outright.
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
  # Google's IAP TCP-forwarding source range. Reaching :22 through it needs an
  # IAM grant as well, so adding it here widens nothing on its own — it just
  # means `gcloud compute ssh --tunnel-through-iap` keeps working when the
  # laptop's public IP rotates.
  iap_ssh_cidr = "35.235.240.0/20"

  ssh_source_ranges     = var.enable_iap_ssh ? [var.allowed_cidr, local.iap_ssh_cidr] : [var.allowed_cidr]
  forgejo_source_ranges = concat([var.allowed_cidr], var.forgejo_extra_cidrs)
  syncd_source_ranges   = concat([var.allowed_cidr], var.syncd_extra_cidrs)

  # A domain-less instance has no name to put in a certificate and no zone to
  # answer a DNS-01 challenge in, so ACME is not on the table; degrade rather
  # than render a plan that cannot succeed.
  tls_mode = var.domain == "" ? "self-signed" : var.tls_mode

  service_account_email = var.service_account_email != "" ? var.service_account_email : one(google_service_account.forge[*].email)
}

# ---- identity -------------------------------------------------------------

resource "google_service_account" "forge" {
  count = var.service_account_email == "" ? 1 : 0

  account_id   = var.vm_name
  display_name = "grove forge services VM"
  description  = "Dedicated identity for ${var.vm_name}. Granted NO IAM roles and attached with NO OAuth scopes: the forge needs no GCP API access, so a compromised VM has no project-wide read."
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
  name        = "${var.vm_name}-allow-forgejo"
  network     = var.network
  description = "Forgejo HTTP. Operator CIDR only unless forgejo_extra_cidrs opts into wider access; tunnel through IAP for anything else."

  allow {
    protocol = "tcp"
    ports    = [tostring(var.forgejo_http_port)]
  }

  source_ranges = local.forgejo_source_ranges
  target_tags   = [var.vm_name]
}

resource "google_compute_firewall" "syncd" {
  count = var.syncd_enabled ? 1 : 0

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
      # ephemeral external IP
    }
  }

  service_account {
    email  = local.service_account_email
    scopes = var.service_account_scopes
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
