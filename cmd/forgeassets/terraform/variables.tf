# Terraform inputs for the grove forge SERVICES VM.
#
# `grove forge up`/`plan` passes the required ones with -var and persists all of
# them to terraform.tfvars so `grove forge down` can destroy without prompting.
# Defaults live here for everything that has a sane one; the CLI resolves them
# from [forge.infra]/[forge.services] (core/config/forge.go), which is where an
# operator should be editing.

variable "project_id" {
  description = "GCP project to create the forge VM in"
  type        = string
}

variable "zone" {
  description = "GCP zone for the VM"
  type        = string
  default     = "us-east1-b"
}

variable "vm_name" {
  description = "Instance name — also the network tag and the prefix of every firewall rule"
  type        = string
  default     = "grove-forge"
}

variable "machine_type" {
  description = "GCE machine type. e2-medium, not the trial's e2-small: two services (Forgejo + grove-syncd) on 2 GB was tight."
  type        = string
  default     = "e2-medium"
}

variable "disk_size_gb" {
  description = "Boot disk size in GB. This disk holds every git ref the forge ever accepts plus its SQLite database — the pet's durable state."
  type        = number
  default     = 50
}

variable "image_family" {
  description = "OS image family"
  type        = string
  default     = "debian-12"
}

variable "image_project" {
  description = "Project owning the OS image"
  type        = string
  default     = "debian-cloud"
}

variable "network" {
  description = "VPC network the VM and its firewall rules live in"
  type        = string
  default     = "default"
}

variable "ssh_user" {
  description = "Username provisioned via instance metadata ssh-keys"
  type        = string
}

variable "ssh_pubkey_file" {
  description = "Path to the SSH public key granted access"
  type        = string
  default     = "~/.ssh/id_ed25519.pub"
}

variable "allowed_cidr" {
  description = "Operator CIDR allowed to reach tcp/22 and the (TLS) syncd port — your laptop's public IP as a /32. Forgejo's plain-HTTP port is NOT opened to this CIDR: it is IAP-tunnel-only. Deliberately no default: never apply this open."
  type        = string

  validation {
    condition     = var.allowed_cidr != "0.0.0.0/0" && var.allowed_cidr != "::/0"
    error_message = "Refusing the open internet — restrict access to your operator address (e.g. 203.0.113.7/32)."
  }
}

variable "enable_iap_ssh" {
  description = "Also allow Google's IAP TCP-forwarding range (35.235.240.0/20) to tcp/22, so SSH survives a laptop IP rotation. Precedent: the trial's forge-trial-allow-iap-ssh rule."
  type        = bool
  default     = true
}

variable "forgejo_extra_cidrs" {
  description = "Additional CIDRs allowed to reach the Forgejo HTTP port, on top of Google's IAP range (the only default source). Empty by default: the forge is IAP-tunnel-only until the owner opts into wider access. Forgejo is plain HTTP, so anything added here sees cleartext in transit. 0.0.0.0/0 is refused."
  type        = list(string)
  default     = []

  validation {
    condition     = length([for c in var.forgejo_extra_cidrs : c if c == "0.0.0.0/0" || c == "::/0"]) == 0
    error_message = "Refusing 0.0.0.0/0 on the forge port — no rule in this module may open a port to the internet."
  }
}

variable "syncd_extra_cidrs" {
  description = "Additional CIDRs allowed to reach the grove-syncd port (other grove nodes replicating the notebook). 0.0.0.0/0 is refused."
  type        = list(string)
  default     = []

  validation {
    condition     = length([for c in var.syncd_extra_cidrs : c if c == "0.0.0.0/0" || c == "::/0"]) == 0
    error_message = "Refusing 0.0.0.0/0 on the syncd port — no rule in this module may open a port to the internet."
  }
}

variable "service_account_email" {
  description = "EXISTING service account to attach. Empty (the default) is recommended: the module then creates a dedicated one with no IAM roles, attached with no OAuth scopes. The trial ran on the default compute SA, which can read every GCS bucket in the project if the VM is compromised."
  type        = string
  default     = ""
}

variable "service_account_scopes" {
  description = "OAuth scopes for the attached service account. Empty is the whole point: the forge needs no GCP API access at all (its binary comes from Codeberg, grove-syncd's from the laptop over SSH). Adding scopes here is a deliberate widening — ACME DNS-01 against Cloud DNS, for example, needs ndev.clouddns.readwrite."
  type        = list(string)
  default     = []
}

# ---- services -------------------------------------------------------------

variable "domain" {
  description = "DNS name the services are reached at. Empty means the external IP is the only address, which forces self-signed TLS."
  type        = string
  default     = ""
}

variable "tls_mode" {
  description = "self-signed (default) or acme. ACME uses the DNS-01 challenge ONLY: HTTP-01 would require opening :80 to the world, which this module refuses for every port. Self-signed certificates are pinned by the fingerprint `grove forge status` reads off the wire."
  type        = string
  default     = "self-signed"

  validation {
    condition     = contains(["self-signed", "acme"], var.tls_mode)
    error_message = "tls_mode must be \"self-signed\" or \"acme\"."
  }
}

variable "acme_email" {
  description = "ACME account contact address (tls_mode = acme)"
  type        = string
  default     = ""
}

variable "acme_dns_provider" {
  description = "lego DNS-01 provider code, e.g. cloudflare or gcloud (tls_mode = acme)"
  type        = string
  default     = ""
}

variable "forgejo_version" {
  description = "Forgejo release to install, e.g. 16.0.2. No default: an unpinned forge is one upstream release away from a surprise."
  type        = string
}

variable "forgejo_sha256" {
  description = "SHA-256 of the Forgejo release binary. No default: a pinned version with an unverified download is not a pin."
  type        = string

  validation {
    condition     = can(regex("^[0-9a-fA-F]{64}$", var.forgejo_sha256))
    error_message = "forgejo_sha256 must be 64 hex characters."
  }
}

variable "forgejo_http_port" {
  description = "Port Forgejo listens on"
  type        = number
  default     = 3000
}

variable "forgejo_site_name" {
  description = "Forgejo APP_NAME"
  type        = string
  default     = "grove forge"
}

variable "syncd_enabled" {
  description = "Colocate grove-syncd on the forge VM (same TLS and token discipline, one box to administer and back up)"
  type        = bool
  default     = true
}

variable "syncd_port" {
  description = "grove-syncd TLS bind port"
  type        = number
  default     = 8788
}
