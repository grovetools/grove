# The Forgejo service.
#
# A module rather than inline resources because the two services on this box are
# independent units with independent lifecycles, and because a module boundary
# is where the "what does this service actually need" question gets answered
# once. It creates no cloud resources: it renders the shell that installs the
# service, and the parent injects that into the instance's startup script.
#
# Install shape, from the live trial (Forgejo v16.0.2 on GCP):
#   - single binary, pinned by version AND checksum,
#   - SQLite (no separate database server on a single-owner forge),
#   - headless install: INSTALL_LOCK = true means the web installer never runs,
#   - registration disabled: this forge has one owner and invited collaborators.

locals {
  binary_name  = "forgejo-${var.release}-linux-amd64"
  download_url = "${var.download_base}/v${var.release}/${local.binary_name}"

  # Forgejo's ROOT_URL has to be right at install time: it is baked into clone
  # URLs and webhook payloads. With no domain there is no name to use, so the
  # startup script substitutes the external IP at boot (FORGEJO_ROOT_URL below
  # is a shell expansion, not a terraform one).
  root_url = var.domain != "" ? "${var.tls_mode == "acme" ? "https" : "http"}://${var.domain}/" : ""

  app_ini = templatefile("${path.module}/app.ini.tpl", {
    site_name = var.site_name
    http_port = var.http_port
    user      = var.user
    domain    = var.domain
    root_url  = local.root_url
  })

  unit = templatefile("${path.module}/forgejo.service.tpl", {
    user = var.user
  })

  setup_script = templatefile("${path.module}/install.sh.tpl", {
    user         = var.user
    binary_name  = local.binary_name
    download_url = local.download_url
    sha256       = lower(var.sha256)
    http_port    = var.http_port
    app_ini      = local.app_ini
    unit         = local.unit
    root_url     = local.root_url
  })
}
