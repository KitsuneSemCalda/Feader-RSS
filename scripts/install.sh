#!/usr/bin/env bash
set -euo pipefail

# Local-development installer for Feader RSS.
# Public distribution should use: omarchy plugin add <git-url> --enable

plugin_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
plugin_id="io.github.kitsunesemcalda.feader-rss"
destination="${XDG_CONFIG_HOME:-${HOME}/.config}/omarchy/plugins/${plugin_id}"

if ! command -v omarchy >/dev/null 2>&1; then
  printf '%s\n' "Error: 'omarchy' command not found." >&2
  exit 1
fi

printf '%s\n' "Validating the plugin..."
omarchy plugin validate "${plugin_root}"

bash "${plugin_root}/scripts/backup.sh"

if [[ -e "${destination}" && ! -d "${destination}" ]]; then
  printf '%s\n' "Error: destination exists and is not a directory: ${destination}" >&2
  exit 1
fi

if [[ -L "${destination}" ]]; then
  printf '%s\n' "Error: destination is a symlink; refusing to clean ${destination}." >&2
  exit 1
fi

mkdir -p "${destination}"

# Remove only files owned by this plugin before copying the new version. Keep
# .git, the user feed configuration, and the persisted article state untouched.
for file in manifest.json BarWidget.qml Panel.qml rss-fetch.py README.md example-config.json; do
  rm -f "${destination}/${file}"
done
rm -rf "${destination}/src" "${destination}/scripts"

# Keep the installed checkout limited to the plugin contract and its runtime
# helper. This avoids copying repository metadata or development artifacts.
install -m 0644 \
  "${plugin_root}/manifest.json" \
  "${plugin_root}/BarWidget.qml" \
  "${plugin_root}/Panel.qml" \
  "${plugin_root}/rss-fetch.py" \
  "${destination}/"

printf '%s\n' "Plugin copied to ${destination}"
omarchy plugin enable "${plugin_id}" right
printf '%s\n' "Feader RSS enabled in the right bar section."
