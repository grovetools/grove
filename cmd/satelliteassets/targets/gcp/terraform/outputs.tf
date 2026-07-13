# Output names are deliberately env-provider friendly: the daemon terraform
# provider's default mapping exports every non-sensitive string output
# UPPER_CASED into EnvResponse.EnvVars (daemon/internal/daemon/env/terraform.go),
# so these become EXTERNAL_IP / SSH_COMMAND in .env.local under an env profile.

output "external_ip" {
  description = "Ephemeral external IP of the satellite VM"
  value       = google_compute_instance.satellite.network_interface[0].access_config[0].nat_ip
}

output "ssh_command" {
  description = "SSH command to reach the satellite"
  value       = "ssh ${var.ssh_user}@${google_compute_instance.satellite.network_interface[0].access_config[0].nat_ip}"
}

output "vm_name" {
  description = "Instance name"
  value       = google_compute_instance.satellite.name
}

output "zone" {
  description = "Instance zone"
  value       = var.zone
}
