#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(pwd)"
service_name="safe-web-note.service"

if [ "${EUID}" -eq 0 ]; then
  systemd_dir="/etc/systemd/system"
  service_path="${systemd_dir}/${service_name}"
  systemctl_cmd="systemctl"
  run_user="${SUDO_USER:-root}"
  wanted_by="multi-user.target"
else
  systemd_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
  service_path="${systemd_dir}/${service_name}"
  systemctl_cmd="systemctl --user"
  run_user="${USER}"
  wanted_by="default.target"
fi

mkdir -p "${systemd_dir}"

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
User=${run_user}

[Install]
WantedBy=${wanted_by}
EOF

${systemctl_cmd} daemon-reload
${systemctl_cmd} enable "${service_name}"
${systemctl_cmd} start "${service_name}"

echo "Installed ${service_path}"
echo "Started: ${systemctl_cmd} start ${service_name}"
