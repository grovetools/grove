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
ReadWritePaths=/var/lib/forgejo /var/log/forgejo
CapabilityBoundingSet=

[Install]
WantedBy=multi-user.target
