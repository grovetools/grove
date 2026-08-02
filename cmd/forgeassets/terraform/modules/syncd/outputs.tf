output "setup_script" {
  description = "Shell fragment the parent injects into the VM startup script: TLS wiring, systemd unit, enable (the binary arrives separately, cross-built from the laptop)."
  value       = local.setup_script
}

output "tls_mode" {
  description = "The TLS mode actually in force — self-signed when no domain is configured, whatever was asked for otherwise"
  value       = local.effective_tls_mode
}
