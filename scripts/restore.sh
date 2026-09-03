#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || ! -d "$1" ]]; then
  printf '%s\n' "Usage: ./scripts/restore.sh /path/to/backup" >&2
  exit 2
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
plugin_root="$(cd -- "${script_dir}/.." && pwd)"
# shellcheck source=database-tools.sh
source "${script_dir}/database-tools.sh"

config_file="${XDG_CONFIG_HOME:-${HOME}/.config}/omarchy/rss-reader.json"
state_dir="${XDG_STATE_HOME:-${HOME}/.local/state}/omarchy/rss-reader"
preferences_file="${state_dir}/preferences.json"
db_file="${state_dir}/items.db"
legacy_file="${state_dir}/items.json"
backup_dir="$(cd -- "$1" && pwd)"

if [[ ! -f "${backup_dir}/rss-reader.json" &&
  ! -f "${backup_dir}/preferences.json" &&
  ! -f "${backup_dir}/items.db" &&
  ! -f "${backup_dir}/items.json" ]]; then
  printf '%s\n' "Error: backup directory contains no Feader RSS data: ${backup_dir}" >&2
  exit 1
fi

bash "$(dirname -- "${BASH_SOURCE[0]}")/backup.sh"
mkdir -p "$(dirname -- "${config_file}")" "${state_dir}"

if [[ -f "${backup_dir}/items.db" ]]; then
  restore_database "${db_file}" "${backup_dir}/items.db" >/dev/null
fi

if [[ -f "${backup_dir}/rss-reader.json" ]]; then
  cp -p "${backup_dir}/rss-reader.json" "${config_file}"
fi
if [[ -f "${backup_dir}/preferences.json" ]]; then
  cp -p "${backup_dir}/preferences.json" "${preferences_file}"
fi
if [[ -f "${backup_dir}/items.json" ]]; then
  cp -p "${backup_dir}/items.json" "${legacy_file}"
fi

printf '%s\n' "Backup restored from ${backup_dir}"
