#!/usr/bin/env bash
set -euo pipefail

service_name="safe-web-note.service"
user_systemd_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
service_path="${user_systemd_dir}/${service_name}"

systemctl --user stop "${service_name}" || true
systemctl --user disable "${service_name}" || true

if [ -f "${service_path}" ]; then
  rm "${service_path}"
fi

systemctl --user daemon-reload

echo "Removed ${service_path}"
