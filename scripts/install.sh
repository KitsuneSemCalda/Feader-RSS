#!/usr/bin/env bash
set -euo pipefail

# Local-development installer for Feader RSS.
# Public distribution should use: omarchy plugin add <git-url> --enable

plugin_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
plugin_id="io.github.kitsunesemcalda.feader-rss"
destination="${XDG_CONFIG_HOME:-${HOME}/.config}/omarchy/plugins/${plugin_id}"
repo="KitsuneSemCalda/Feader-RSS"
binary_name="feader-rss-fetch"

if ! command -v omarchy >/dev/null 2>&1; then
  printf '%s\n' "Error: 'omarchy' command not found." >&2
  exit 1
fi

version="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${plugin_root}/manifest.json" | head -n1)"
if [[ -z "${version}" ]]; then
  printf '%s\n' "Error: could not read version from manifest.json" >&2
  exit 1
fi

detect_platform() {
  local os arch
  case "$(uname -s)" in
    Linux) os="linux" ;;
    Darwin) os="darwin" ;;
    *) printf '%s\n' "Error: unsupported OS: $(uname -s)" >&2; exit 1 ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) printf '%s\n' "Error: unsupported architecture: $(uname -m)" >&2; exit 1 ;;
  esac
  printf '%s_%s\n' "${os}" "${arch}"
}

is_local_checkout() {
  [[ -e "${plugin_root}/.git" ]]
}

build_local() {
  local tmp_dir out_path
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "${tmp_dir}"' RETURN
  out_path="${tmp_dir}/${binary_name}"

  printf '%s\n' "Local checkout detected (.git present); building ${binary_name} from source..."
  if ! (cd "${plugin_root}" && go build -o "${out_path}" "./cmd/${binary_name}"); then
    printf '%s\n' "Error: local build failed." >&2
    return 1
  fi

  install -m 0755 "${out_path}" "${destination}/${binary_name}"
}

fetch_binary() {
  local platform asset_name url checksums_url tmp_dir asset_path checksums_path expected actual

  if ! command -v gh >/dev/null 2>&1; then
    printf '%s\n' "Error: 'gh' (GitHub CLI) command not found." >&2
    printf '%s\n' "It is required to verify release binary provenance before install. See https://cli.github.com" >&2
    exit 1
  fi

  platform="$(detect_platform)"
  asset_name="${binary_name}_${version}_${platform}"
  url="https://github.com/${repo}/releases/download/v${version}/${asset_name}"
  checksums_url="https://github.com/${repo}/releases/download/v${version}/checksums.txt"

  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "${tmp_dir}"' RETURN
  asset_path="${tmp_dir}/${binary_name}"
  checksums_path="${tmp_dir}/checksums.txt"

  printf '%s\n' "Downloading ${asset_name} from GitHub release v${version}..."
  if ! curl -fsSL --proto '=https' --tlsv1.2 -o "${asset_path}" "${url}"; then
    printf '%s\n' "Error: failed to download release asset: ${url}" >&2
    printf '%s\n' "If this version has no published release yet, build locally with: go build -o scripts/${binary_name} ./cmd/${binary_name}" >&2
    exit 1
  fi

  if ! curl -fsSL --proto '=https' --tlsv1.2 -o "${checksums_path}" "${checksums_url}"; then
    printf '%s\n' "Error: failed to download checksums.txt: ${checksums_url}" >&2
    printf '%s\n' "Refusing to install an unverified binary." >&2
    exit 1
  fi
  expected="$(grep -F " ${asset_name}" "${checksums_path}" | awk '{print $1}' | head -n1)"
  if [[ -z "${expected}" ]]; then
    printf '%s\n' "Error: no checksum entry for ${asset_name} in checksums.txt" >&2
    exit 1
  fi
  actual="$(sha256sum "${asset_path}" | awk '{print $1}')"
  if [[ "${expected}" != "${actual}" ]]; then
    printf '%s\n' "Error: checksum mismatch for ${asset_name} (expected ${expected}, got ${actual})" >&2
    exit 1
  fi
  printf '%s\n' "Checksum verified."

  printf '%s\n' "Verifying build provenance attestation..."
  if ! gh attestation verify "${asset_path}" --repo "${repo}" >/dev/null; then
    printf '%s\n' "Error: build provenance attestation verification failed for ${asset_name}." >&2
    printf '%s\n' "Refusing to install a binary that cannot be verified as built by ${repo}'s release workflow from a signed commit." >&2
    exit 1
  fi
  printf '%s\n' "Provenance verified: binary was built by ${repo}'s release workflow."

  chmod +x "${asset_path}"
  install -m 0755 "${asset_path}" "${destination}/${binary_name}"
}

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
for file in manifest.json BarWidget.qml Panel.qml README.md example-config.json "${binary_name}" rss-fetch.py; do
  rm -f "${destination}/${file}"
done
rm -rf "${destination}/src" "${destination}/scripts"

# Keep the installed checkout limited to the plugin contract. The fetch/parse
# backend is a compiled Go binary, downloaded from the matching GitHub
# release rather than shipped in the plugin source tree.
install -m 0644 \
  "${plugin_root}/manifest.json" \
  "${plugin_root}/BarWidget.qml" \
  "${plugin_root}/Panel.qml" \
  "${plugin_root}/README.md" \
  "${plugin_root}/example-config.json" \
  "${destination}/"

if is_local_checkout && command -v go >/dev/null 2>&1; then
  if ! build_local; then
    printf '%s\n' "Falling back to downloading a prebuilt release binary..." >&2
    fetch_binary
  fi
elif is_local_checkout; then
  printf '%s\n' "Local checkout detected but 'go' is not installed; downloading a prebuilt release binary instead." >&2
  fetch_binary
else
  fetch_binary
fi

printf '%s\n' "Plugin copied to ${destination}"
omarchy plugin enable "${plugin_id}" right
printf '%s\n' "Feader RSS enabled in the right bar section."
