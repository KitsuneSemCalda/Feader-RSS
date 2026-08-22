#!/usr/bin/env bash
set -euo pipefail

config_file="${XDG_CONFIG_HOME:-${HOME}/.config}/omarchy/rss-reader.json"
state_file="${XDG_STATE_HOME:-${HOME}/.local/state}/omarchy/rss-reader/items.json"
backup_root="${XDG_STATE_HOME:-${HOME}/.local/state}/omarchy/rss-reader/backups"

if [[ ! -f "${config_file}" && ! -f "${state_file}" ]]; then
  printf '%s\n' "Nenhum dado do Feader RSS para salvar."
  exit 0
fi

backup_dir="${backup_root}/$(date -u +%Y%m%dT%H%M%S%NZ)"
while [[ -e "${backup_dir}" ]]; do
  backup_dir="${backup_root}/$(date -u +%Y%m%dT%H%M%S%NZ)-${RANDOM}"
done
mkdir -p "${backup_dir}"

if [[ -f "${config_file}" ]]; then
  cp -p "${config_file}" "${backup_dir}/rss-reader.json"
fi
if [[ -f "${state_file}" ]]; then
  cp -p "${state_file}" "${backup_dir}/items.json"
fi

printf '%s\n' "Backup criado em ${backup_dir}"
