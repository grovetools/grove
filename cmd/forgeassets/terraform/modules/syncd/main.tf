# The grove-syncd service, colocated with Forgejo.
#
# Unlike Forgejo, the BINARY does not come from the internet: grove-syncd is
# built from the ecosystem, so `grove forge up --prebuilt` cross-compiles it on
# the laptop and ships it over the pinned SSH connection — the satellite
# `up --prebuilt` precedent, and the only honest option for a binary with no
# public release. This module installs everything AROUND the binary (group,
# dirs, TLS material wiring, systemd unit) and leaves the unit enabled but not
# started until the binary lands.
#
# TLS is not optional. grove-syncd refuses a non-loopback bind without
# --tls-cert/--tls-key unless --insecure is passed explicitly
# (sync/cmd/serve.go checkTLSPosture), and nothing here passes --insecure: a
# forge worth backing up is worth not shipping its tokens in clear text (the
# trial's #1 outstanding hardening item).
#
# DRIFT NOTE: the unit below is derived from sync/systemd/grove-syncd.service
# rather than shipped verbatim, because that unit deliberately binds loopback
# and assumes a reverse proxy in front. The hardening directives are kept in
# lockstep; the ExecStart is the intentional difference.

locals {
  # A domain-less instance has no name to put in a certificate and no zone to
  # answer a DNS-01 challenge in, so ACME cannot apply.
  effective_tls_mode = var.domain == "" ? "self-signed" : var.tls_mode

  unit = templatefile("${path.module}/grove-syncd.service.tpl", {
    port      = var.port
    tls_group = var.tls_group
  })

  setup_script = templatefile("${path.module}/install.sh.tpl", {
    port              = var.port
    domain            = var.domain
    tls_mode          = local.effective_tls_mode
    tls_group         = var.tls_group
    acme_email        = var.acme_email
    acme_dns_provider = var.acme_dns_provider
    # Space-separated: acme.defaults is a shell env file and the renew script
    # word-splits this deliberately, one --dns.resolvers per entry.
    acme_dns_resolvers = join(" ", var.acme_dns_resolvers)
    unit               = local.unit
  })
}
