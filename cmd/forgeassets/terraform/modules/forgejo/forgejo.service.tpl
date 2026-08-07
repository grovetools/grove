[Unit]
Description=Forgejo (grove forge)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${user}
Group=${user}
WorkingDirectory=/var/lib/forgejo
RuntimeDirectory=forgejo
Environment=USER=${user} HOME=/var/lib/forgejo GITEA_WORK_DIR=/var/lib/forgejo
ExecStart=/usr/local/bin/forgejo web --config /etc/forgejo/app.ini
Restart=always
RestartSec=5

# The forge holds every plan branch ever pushed. Confining it costs nothing and
# means a Forgejo compromise is not a whole-host compromise.
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
# Only paths install.sh.tpl actually creates may be listed: under
# ProtectSystem=strict systemd fails the whole mount namespace (226/NAMESPACE)
# on a ReadWritePaths entry that does not exist. Forgejo's log dir is
# /var/lib/forgejo/log (see app.ini ROOT_PATH), which this covers; there is no
# /var/log/forgejo.
ReadWritePaths=/var/lib/forgejo
%{ if tls_mode == "acme" ~}
# ACME mode: Forgejo terminates TLS on :443 with the shared certificate. The
# private key is root:${tls_group} 0640 (the same discipline grove-syncd uses),
# and binding a port below 1024 as ${user} needs exactly one capability.
SupplementaryGroups=${tls_group}
ReadOnlyPaths=/etc/grove-forge/tls
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
%{ else ~}
CapabilityBoundingSet=
%{ endif ~}

[Install]
WantedBy=multi-user.target
