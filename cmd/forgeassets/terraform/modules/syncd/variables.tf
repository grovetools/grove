variable "port" {
  description = "TLS bind port for grove-syncd"
  type        = number
  default     = 8788
}

variable "domain" {
  description = "DNS name, empty when the external IP is the only address"
  type        = string
  default     = ""
}

variable "tls_mode" {
  description = "self-signed or acme"
  type        = string
  default     = "self-signed"
}

variable "acme_email" {
  description = "ACME account contact (tls_mode = acme)"
  type        = string
  default     = ""
}

variable "acme_dns_provider" {
  description = "lego DNS-01 provider code (tls_mode = acme)"
  type        = string
  default     = ""
}

variable "acme_dns_resolvers" {
  description = "Nameservers (host:port) lego polls for DNS-01 propagation; needed for a delegated subdomain"
  type        = list(string)
  default     = []
}

variable "tls_group" {
  description = "Group owning the TLS private key. The unit runs under DynamicUser (no fixed uid to chown to), so key access is granted by supplementary group instead."
  type        = string
  default     = "grove-tls"
}
