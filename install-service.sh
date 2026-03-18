#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(pwd)"
service_name="safe-web-note.service"
user_systemd_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
service_path="${user_systemd_dir}/${service_name}"

mkdir -p "${user_systemd_dir}"

cat > "${service_path}" <<EOF
[Unit]
Description=Safe Web Note server
After=network.target

[Service]
Type=simple
WorkingDirectory=${repo_dir}
ExecStart=/usr/bin/env go run .
Restart=on-failure
RestartSec=2
User=${USER}

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable "${service_name}"
systemctl --user start "${service_name}"

echo "Installed ${service_path}"
echo "Started: systemctl --user start ${service_name}"
