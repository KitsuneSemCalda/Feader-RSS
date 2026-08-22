#!/usr/bin/env bash
set -euo pipefail

# Local-development installer for Feader RSS.
# Public distribution should use: omarchy plugin add <git-url> --enable

plugin_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
plugin_id="io.github.kitsunesemcalda.feader-rss"
destination="${XDG_CONFIG_HOME:-${HOME}/.config}/omarchy/plugins/${plugin_id}"

if ! command -v omarchy >/dev/null 2>&1; then
  printf '%s\n' "Erro: o comando 'omarchy' não foi encontrado." >&2
  exit 1
fi

printf '%s\n' "Validando o plugin..."
omarchy plugin validate "${plugin_root}"

if [[ -e "${destination}" && ! -d "${destination}" ]]; then
  printf '%s\n' "Erro: o destino existe e não é um diretório: ${destination}" >&2
  exit 1
fi

mkdir -p "${destination}"

# Keep the installed checkout limited to the plugin contract and its runtime
# helper. This avoids copying repository metadata or development artifacts.
install -m 0644 \
  "${plugin_root}/manifest.json" \
  "${plugin_root}/BarWidget.qml" \
  "${plugin_root}/Panel.qml" \
  "${plugin_root}/rss-fetch.py" \
  "${destination}/"

printf '%s\n' "Plugin copiado para ${destination}"
omarchy plugin enable "${plugin_id}" right
printf '%s\n' "Feader RSS habilitado na seção direita da barra."
