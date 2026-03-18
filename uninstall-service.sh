#!/usr/bin/env bash
set -euo pipefail

service_name="safe-web-note.service"
user_systemd_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
user_service_path="${user_systemd_dir}/${service_name}"
system_service_path="/etc/systemd/system/${service_name}"

mode="auto"
if [ "${1:-}" = "--user" ]; then
  mode="user"
elif [ "${1:-}" = "--system" ]; then
  mode="system"
fi

uninstall_user() {
  systemctl --user stop "${service_name}" || true
  systemctl --user disable "${service_name}" || true
  if [ -f "${user_service_path}" ]; then
    rm "${user_service_path}"
  fi
  systemctl --user daemon-reload
  echo "Removed ${user_service_path}"
}

uninstall_system() {
  systemctl stop "${service_name}" || true
  systemctl disable "${service_name}" || true
  if [ -f "${system_service_path}" ]; then
    rm "${system_service_path}"
  fi
  systemctl daemon-reload
  echo "Removed ${system_service_path}"
}

if [ "${mode}" = "user" ]; then
  uninstall_user
  exit 0
fi

if [ "${mode}" = "system" ]; then
  if [ "${EUID}" -ne 0 ]; then
    echo "System service uninstall requires sudo."
    exec sudo bash "$0" --system
  fi
  uninstall_system
  exit 0
fi

if [ -f "${user_service_path}" ]; then
  uninstall_user
  exit 0
fi

if [ -f "${system_service_path}" ]; then
  if [ "${EUID}" -ne 0 ]; then
    echo "User service not found. System service exists at ${system_service_path}."
    echo "Requesting sudo to uninstall system service."
    exec sudo bash "$0" --system
  fi
  uninstall_system
  exit 0
fi

echo "No service file found in ${user_service_path} or ${system_service_path}."
