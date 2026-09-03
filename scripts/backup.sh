#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
plugin_root="$(cd -- "${script_dir}/.." && pwd)"
# shellcheck source=database-tools.sh
source "${script_dir}/database-tools.sh"

config_file="${XDG_CONFIG_HOME:-${HOME}/.config}/omarchy/rss-reader.json"
state_dir="${XDG_STATE_HOME:-${HOME}/.local/state}/omarchy/rss-reader"
preferences_file="${state_dir}/preferences.json"
db_file="${state_dir}/items.db"
legacy_file="${state_dir}/items.json"
backup_root="${state_dir}/backups"

if [[ ! -f "${config_file}" && ! -f "${preferences_file}" && ! -f "${db_file}" && ! -f "${legacy_file}" ]]; then
  printf '%s\n' "No Feader RSS data to back up."
  exit 0
fi

mkdir -p "${backup_root}"

backup_stamp="$(date -u +%Y%m%dT%H%M%S%NZ)"
backup_dir="${backup_root}/${backup_stamp}"
while [[ -e "${backup_dir}" ]]; do
  backup_dir="${backup_root}/${backup_stamp}-${RANDOM}"
done

backup_tmp="$(mktemp -d "${backup_root}/.backup.XXXXXX")"
cleanup_backup_tmp() {
  rm -rf -- "${backup_tmp}"
}
trap cleanup_backup_tmp EXIT

if [[ -f "${config_file}" ]]; then
  cp -p "${config_file}" "${backup_tmp}/rss-reader.json"
fi
if [[ -f "${preferences_file}" ]]; then
  cp -p "${preferences_file}" "${backup_tmp}/preferences.json"
fi
if [[ -f "${legacy_file}" ]]; then
  cp -p "${legacy_file}" "${backup_tmp}/items.json"
fi
if [[ -f "${db_file}" ]]; then
  backup_database "${db_file}" "${backup_tmp}/items.db" >/dev/null
fi

mv -- "${backup_tmp}" "${backup_dir}"
trap - EXIT
printf '%s\n' "Backup created at ${backup_dir}"
