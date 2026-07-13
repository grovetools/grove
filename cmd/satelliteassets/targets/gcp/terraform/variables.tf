variable "project_id" {
  description = "GCP project to create the satellite VM in"
  type        = string
}

variable "zone" {
  description = "GCP zone for the VM"
  type        = string
  default     = "us-east1-b"
}

variable "vm_name" {
  description = "Instance name (also used for the firewall rule and network tag)"
  type        = string
  default     = "grove-satellite"
}

variable "machine_type" {
  description = "GCE machine type (e2-standard-4 builds the 26-repo ecosystem comfortably; e2-standard-2 halves cost)"
  type        = string
  default     = "e2-standard-4"
}

variable "disk_size_gb" {
  description = "Boot disk size in GB"
  type        = number
  default     = 50
}

variable "image_family" {
  description = "OS image family"
  type        = string
  default     = "ubuntu-2404-lts-amd64"
}

variable "image_project" {
  description = "Project owning the OS image"
  type        = string
  default     = "ubuntu-os-cloud"
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

variable "allowed_ssh_cidr" {
  description = "CIDR allowed to reach :22 — set to your laptop's public IP, e.g. 203.0.113.7/32. Deliberately no default: never apply this open."
  type        = string

  validation {
    condition     = var.allowed_ssh_cidr != "0.0.0.0/0"
    error_message = "Refusing 0.0.0.0/0 — restrict SSH to your laptop's public IP (/32)."
  }
}

variable "service_account_email" {
  description = "Service account to attach to the VM. Auth then flows through the GCE metadata server (ADC) — gcloud and client libraries pick it up automatically, zero key files on disk. Empty string (default) attaches no service account."
  type        = string
  default     = ""
}

variable "service_account_scopes" {
  description = "OAuth access scopes for the attached service account. cloud-platform defers all real access control to the SA's IAM roles (recommended); narrow scopes only add a second, coarser gate."
  type        = list(string)
  default     = ["cloud-platform"]
}

variable "zig_version" {
  description = "Zig toolchain version (compositor/treemux libghostty build; match the laptop's zig)"
  type        = string
  default     = "0.15.2"
}
