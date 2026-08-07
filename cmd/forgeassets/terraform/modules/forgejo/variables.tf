variable "release" {
  description = "Forgejo release version, e.g. 16.0.2 (no leading v)"
  type        = string
}

variable "sha256" {
  description = "SHA-256 of the linux-amd64 release binary"
  type        = string
}

variable "http_port" {
  description = "Port Forgejo listens on"
  type        = number
  default     = 3000
}

variable "site_name" {
  description = "Forgejo APP_NAME"
  type        = string
  default     = "grove forge"
}

variable "domain" {
  description = "DNS name, empty when the external IP is the only address"
  type        = string
  default     = ""
}

variable "tls_mode" {
  description = "self-signed or acme — decides whether ROOT_URL is http or https"
  type        = string
  default     = "self-signed"
}

variable "user" {
  description = "System user the service runs as"
  type        = string
  default     = "git"
}

variable "tls_group" {
  description = "Group that may read the shared TLS private key (matches the syncd module's tls_group). Only used when tls_mode = acme, where Forgejo terminates TLS itself."
  type        = string
  default     = "grove-tls"
}

variable "download_base" {
  description = "Release download base. Overridable so an air-gapped install can point at a mirror; the checksum is verified either way."
  type        = string
  default     = "https://codeberg.org/forgejo/forgejo/releases/download"
}
