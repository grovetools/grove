# Output names are env-provider friendly for the same reason the satellite
# module's are: the daemon terraform env provider upper-cases non-sensitive
# string outputs into .env.local (EXTERNAL_IP, SSH_COMMAND, FORGE_URL).
#
# `grove forge status` reads these through `terraform output -json` and does not
# contact the VM to render them. The one thing it CANNOT read from here is the
# TLS fingerprint of a self-signed certificate — that is generated on the VM at
# first boot, so status dials the syncd port and computes it off the wire.

output "external_ip" {
  description = "Ephemeral external IP of the forge VM"
  value       = google_compute_instance.forge.network_interface[0].access_config[0].nat_ip
}

output "ssh_command" {
  description = "SSH command to reach the forge"
  value       = "ssh ${var.ssh_user}@${google_compute_instance.forge.network_interface[0].access_config[0].nat_ip}"
}

output "vm_name" {
  description = "Instance name"
  value       = google_compute_instance.forge.name
}

output "zone" {
  description = "Instance zone"
  value       = var.zone
}

output "forge_url" {
  description = "Base URL of the Forgejo instance — the value that belongs in [forge] url"
  value       = var.domain != "" ? "https://${var.domain}" : "http://${google_compute_instance.forge.network_interface[0].access_config[0].nat_ip}:${var.forgejo_http_port}"
}

output "syncd_addr" {
  description = "host:port of the colocated grove-syncd, or empty when it is not deployed"
  value       = var.syncd_enabled ? format("%s:%d", var.domain != "" ? var.domain : google_compute_instance.forge.network_interface[0].access_config[0].nat_ip, var.syncd_port) : ""
}

output "tls_mode" {
  description = "Effective TLS strategy: self-signed (pin the fingerprint from `grove forge status`) or acme"
  value       = local.tls_mode
}

output "service_account_email" {
  description = "Identity attached to the VM. When the module created it, it carries no IAM roles and the instance attaches it with no OAuth scopes."
  value       = local.service_account_email
}

output "firewall_rules" {
  description = "Every ingress rule this module owns, so an audit can see the whole exposed surface in one place"
  value = concat(
    [
      "${google_compute_firewall.ssh.name}: tcp/22 from ${join(",", local.ssh_source_ranges)}",
      "${google_compute_firewall.forgejo.name}: tcp/${var.forgejo_http_port} from ${join(",", local.forgejo_source_ranges)}",
    ],
    var.syncd_enabled ? ["${var.vm_name}-allow-syncd: tcp/${var.syncd_port} from ${join(",", local.syncd_source_ranges)}"] : [],
  )
}
