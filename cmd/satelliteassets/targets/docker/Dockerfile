# grove satellite docker image — the container `grove satellite up --target
# docker` runs. Built from the CLI's embedded copy of this build context (the
# image tag is a content hash of these files, so editing them makes the next
# `up` rebuild naturally). Override with [satellites.<name>.infra] image=...
# to bring your own image: it must run sshd on :22 and accept the injected
# authorized key for a non-root login user.
#
# Deterministic on purpose: a pinned slim base, a single apt layer, nothing
# that moves independently of this file. The satellite is exec-kind only
# (sshd + grove binaries; no groved/syncd services), so the guest needs just:
#   - openssh-server: the endpoint itself.
#   - openssh-sftp-server: modern OpenSSH clients' scp speaks SFTP; without
#     the subsystem binary every laptop-side `scp` (prebuilt ship, repo
#     mirror) fails. --no-install-recommends would otherwise drop it.
#   - bash: the generated remote scripts run via `bash -s`.
#   - git: the repo mirror / worktree verbs (bundle fetch + checkout).
#   - curl + ca-certificates: small provisioning helpers and anything the
#     shipped tools fetch over TLS.
#   - sudo: the shared prebuilt ship set includes the sync repo, whose
#     grove-syncd binary is system-installed via `sudo cp` to /usr/local/bin
#     by the generated install script (grove/cmd/satellite_upgrade.go,
#     buildSatellitePrebuiltInstallScript). The tart guest relies on its
#     image's passwordless sudo for the same step; this image mirrors that.
#   - tar/gzip/coreutils/awk (prebuilt install script) ship with the base.
FROM debian:bookworm-slim

RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      openssh-server openssh-sftp-server sudo bash git curl ca-certificates \
 && rm -rf /var/lib/apt/lists/*

# Non-root login user (uid 1000) with passwordless sudo (see the package note
# above). No password is ever set: login is pubkey-only.
RUN useradd --create-home --uid 1000 --shell /bin/bash grove \
 && printf 'grove ALL=(ALL) NOPASSWD:ALL\n' > /etc/sudoers.d/grove \
 && chmod 0440 /etc/sudoers.d/grove

# sshd hardening: pubkey only, no passwords, no root login. sshd_config.d is
# first-match-wins, so the grove drop-in sorts FIRST (00-) — the same lesson
# the tart provider learned from the image-shipped 50-cloud-init.conf.
RUN mkdir -p /run/sshd \
 && printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin no\n' \
      > /etc/ssh/sshd_config.d/00-grove.conf

# Login-shell PATH includes the grove bin dir the prebuilt install targets
# (mirrors the gcp bootstrap's /etc/profile.d entry and tart's layer-0 prep;
# the generated remote scripts also export it explicitly).
RUN printf 'export PATH="$HOME/.local/share/grove/bin:$PATH"\n' > /etc/profile.d/grove-satellite.sh

COPY entrypoint.sh /usr/local/bin/grove-satellite-entrypoint
RUN chmod 0755 /usr/local/bin/grove-satellite-entrypoint

EXPOSE 22
ENTRYPOINT ["/usr/local/bin/grove-satellite-entrypoint"]
