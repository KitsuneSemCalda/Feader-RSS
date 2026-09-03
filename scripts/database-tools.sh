#!/usr/bin/env bash

# Shared database helpers for the source-checkout backup and restore scripts.
# The installed plugin uses its release binary; the Go source and sqlite3
# fallbacks keep these maintenance scripts usable from a checkout as well.

fetch_binary="${FEADER_RSS_FETCH:-${plugin_root}/feader-rss-fetch}"

sqlite_quote() {
  local value="$1"
  value="${value//\'/\'\'}"
  printf "'%s'" "${value}"
}

backup_database() {
  local db_path="$1"
  local output_path="$2"

  if [[ -x "${fetch_binary}" ]]; then
    "${fetch_binary}" backup --db "${db_path}" --output "${output_path}"
    return
  fi

  if [[ -f "${plugin_root}/go.mod" ]] && command -v go >/dev/null 2>&1; then
    if (cd -- "${plugin_root}" && go run ./cmd/feader-rss-fetch backup --db "${db_path}" --output "${output_path}"); then
      return
    fi
  fi

  if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 "${db_path}" ".backup $(sqlite_quote "${output_path}")"
    return
  fi

  printf '%s\n' "Error: no Feader RSS backend or sqlite3 is available to snapshot ${db_path}." >&2
  return 1
}

restore_database() {
  local db_path="$1"
  local input_path="$2"

  if [[ -x "${fetch_binary}" ]]; then
    "${fetch_binary}" restore --db "${db_path}" --input "${input_path}"
    return
  fi

  if [[ -f "${plugin_root}/go.mod" ]] && command -v go >/dev/null 2>&1; then
    if (cd -- "${plugin_root}" && go run ./cmd/feader-rss-fetch restore --db "${db_path}" --input "${input_path}"); then
      return
    fi
  fi

  if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 "${db_path}" ".restore main $(sqlite_quote "${input_path}")"
    sqlite3 "${db_path}" "PRAGMA journal_mode=WAL;" >/dev/null
    return
  fi

  printf '%s\n' "Error: no Feader RSS backend or sqlite3 is available to restore ${input_path}." >&2
  return 1
}
