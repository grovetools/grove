output "setup_script" {
  description = "Shell fragment the parent injects into the VM startup script: download+verify, app.ini, systemd unit, enable."
  value       = local.setup_script
}

output "download_url" {
  description = "The exact pinned release URL this module installs (surfaced so an audit does not have to read the rendered script)"
  value       = local.download_url
}
