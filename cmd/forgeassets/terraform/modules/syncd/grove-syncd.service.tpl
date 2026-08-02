[Unit]
Description=grove-syncd notebook sync server (grove forge)
Documentation=https://github.com/grovetools/sync
After=network-online.target
Wants=network-online.target
# The binary is shipped from the laptop AFTER terraform apply
# (`grove forge up --prebuilt`), so the unit is enabled at provision time and
# only becomes startable once this path exists.
ConditionPathExists=/usr/local/bin/grove-syncd

[Service]
Type=simple
DynamicUser=yes
StateDirectory=grove-syncd
LogsDirectory=grove-syncd
# DynamicUser has no HOME, so the grove unified logger's XDG resolution comes
# back empty and falls back to a CWD-relative "logs" dir, which fails under
# ProtectSystem=strict. Point it at the systemd-managed log dir explicitly.
WorkingDirectory=/var/lib/grove-syncd
Environment=GROVE_LOG_FILE=/var/log/grove-syncd/grove-syncd.log
# A non-loopback bind without a certificate is refused by the server itself,
# and there is no flag here that waives that refusal — the refusal IS the
# posture this deployment wants.
ExecStart=/usr/local/bin/grove-syncd serve \
  --data-dir /var/lib/grove-syncd \
  --bind 0.0.0.0:${port} \
  --tls-cert /etc/grove-forge/tls/cert.pem \
  --tls-key /etc/grove-forge/tls/key.pem
Restart=on-failure
RestartSec=5
# The private key is root-owned and group-readable; DynamicUser joins the group
# rather than owning the file.
SupplementaryGroups=${tls_group}

# Hardening — kept in lockstep with sync/systemd/grove-syncd.service.
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
LockPersonality=yes
MemoryDenyWriteExecute=yes
ReadOnlyPaths=/etc/grove-forge/tls

[Install]
WantedBy=multi-user.target
