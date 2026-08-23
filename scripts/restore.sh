#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || ! -d "$1" ]]; then
  printf '%s\n' "Uso: ./scripts/restore.sh /caminho/para/backup" >&2
  exit 2
fi

config_file="${XDG_CONFIG_HOME:-${HOME}/.config}/omarchy/rss-reader.json"
state_file="${XDG_STATE_HOME:-${HOME}/.local/state}/omarchy/rss-reader/items.json"
backup_dir="$(cd -- "$1" && pwd)"

bash "$(dirname -- "${BASH_SOURCE[0]}")/backup.sh"
mkdir -p "$(dirname -- "${config_file}")" "$(dirname -- "${state_file}")"

if [[ -f "${backup_dir}/rss-reader.json" ]]; then
  cp -p "${backup_dir}/rss-reader.json" "${config_file}"
fi
if [[ -f "${backup_dir}/items.json" ]]; then
  cp -p "${backup_dir}/items.json" "${state_file}"
fi

printf '%s\n' "Backup restaurado de ${backup_dir}"
