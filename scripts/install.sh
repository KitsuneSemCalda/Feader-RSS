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

release_workflow="${repo}/.github/workflows/release.yml"
# Bound every download so a stalled or malicious server can't hang the
# installer or exhaust disk space: 15s to connect, 60s total, 100MB cap.
curl_guard=(--proto '=https' --tlsv1.2 --connect-timeout 15 --max-time 60 --max-filesize 100000000)

# The commit that provenance must be pinned to. A local checkout independently
# knows its own HEAD, which is the actual reviewed source this script was
# read from — verifying against that closes the gap where an attacker who can
# move/re-push the release tag (refs/tags/v*) gets a new attestation that
# still matches a --source-ref check by name alone. Without a local checkout
# there is no independently trusted commit, so fall back to resolving the tag
# once via the API; that is weaker (still trusts the tag at fetch time) but
# still pins to a concrete digest instead of a ref string.
resolve_source_digest() {
  if is_local_checkout && command -v git >/dev/null 2>&1; then
    git -C "${plugin_root}" rev-parse HEAD
    return
  fi
  gh api "repos/${repo}/commits/v${version}" --jq '.sha'
}

# build_local and fetch_binary each install the binary into the given staging
# directory rather than the live destination, so a failure here never leaves
# the installed plugin without a working binary (see the atomic swap below).
build_local() {
  local target_dir="$1"
  local tmp_dir out_path
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "${tmp_dir}"' RETURN
  out_path="${tmp_dir}/${binary_name}"

  printf '%s\n' "Local checkout detected (.git present); building ${binary_name} from source..."
  if ! (cd "${plugin_root}" && go build -o "${out_path}" "./cmd/${binary_name}"); then
    printf '%s\n' "Error: local build failed." >&2
    return 1
  fi

  install -m 0755 "${out_path}" "${target_dir}/${binary_name}"
}

fetch_binary() {
  local target_dir="$1"
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
  if ! curl -fsSL "${curl_guard[@]}" -o "${asset_path}" "${url}"; then
    printf '%s\n' "Error: failed to download release asset: ${url}" >&2
    printf '%s\n' "If this version has no published release yet, build locally with: go build -o scripts/${binary_name} ./cmd/${binary_name}" >&2
    exit 1
  fi

  if ! curl -fsSL "${curl_guard[@]}" -o "${checksums_path}" "${checksums_url}"; then
    printf '%s\n' "Error: failed to download checksums.txt: ${checksums_url}" >&2
    printf '%s\n' "Refusing to install an unverified binary." >&2
    exit 1
  fi

  local source_digest
  source_digest="$(resolve_source_digest)"
  if [[ -z "${source_digest}" ]]; then
    printf '%s\n' "Error: could not resolve the source commit for v${version} to pin provenance verification to." >&2
    exit 1
  fi

  printf '%s\n' "Verifying build provenance attestation..."
  # Pin verification to the exact workflow file that is allowed to sign a
  # release binary and to the exact commit it must have been built from
  # (--source-digest), not the release tag name. A tag is a movable pointer:
  # an attacker who can re-push/retarget refs/tags/v${version} can get a new,
  # honestly-signed attestation for a different commit that still matches a
  # ref-name check. Checking both downloads binds the checksum manifest itself
  # to the same pinned provenance, not just the binary it describes.
  for downloaded in "${checksums_path}" "${asset_path}"; do
    if ! gh attestation verify "${downloaded}" --repo "${repo}" \
        --signer-workflow "${release_workflow}" \
        --source-digest "${source_digest}" >/dev/null; then
      printf '%s\n' "Error: build provenance attestation verification failed for $(basename "${downloaded}")." >&2
      printf '%s\n' "Refusing to install: cannot verify it was built by ${release_workflow} from commit ${source_digest}." >&2
      exit 1
    fi
  done
  printf '%s\n' "Provenance verified: both downloads were built by ${release_workflow} from commit ${source_digest}."

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

  chmod +x "${asset_path}"
  install -m 0755 "${asset_path}" "${target_dir}/${binary_name}"
}

validate_destination_not_symlink() {
  if [[ -e "${destination}" && ! -d "${destination}" ]]; then
    printf '%s\n' "Error: destination exists and is not a directory: ${destination}" >&2
    exit 1
  fi
  if [[ -L "${destination}" ]]; then
    printf '%s\n' "Error: destination is a symlink; refusing to write to ${destination}." >&2
    exit 1
  fi
}

printf '%s\n' "Validating the plugin..."
omarchy plugin validate "${plugin_root}"

bash "${plugin_root}/scripts/backup.sh"

validate_destination_not_symlink

destination_parent="$(dirname -- "${destination}")"
mkdir -p "${destination_parent}"

# Assemble the full plugin — manifest, QML, and the fetch binary — in a
# staging directory first, and only touch the live plugin directory once
# every piece is built/downloaded and verified. This keeps a failed
# build/download/attestation check from ever leaving the live plugin without
# a working binary: until this point, ${destination} is never modified.
staging_dir="$(mktemp -d "${destination_parent}/.feader-rss-staging.XXXXXX")"
cleanup_staging() { rm -rf -- "${staging_dir}"; }
trap cleanup_staging EXIT

install -m 0644 \
  "${plugin_root}/manifest.json" \
  "${plugin_root}/BarWidget.qml" \
  "${plugin_root}/Panel.qml" \
  "${plugin_root}/README.md" \
  "${plugin_root}/example-config.json" \
  "${staging_dir}/"

if is_local_checkout && command -v go >/dev/null 2>&1; then
  if ! build_local "${staging_dir}"; then
    if [[ "${FEADER_RSS_ALLOW_REMOTE_FALLBACK:-}" == "1" ]]; then
      printf '%s\n' "Local build failed; FEADER_RSS_ALLOW_REMOTE_FALLBACK=1 set, downloading a prebuilt release binary instead..." >&2
      fetch_binary "${staging_dir}"
    else
      printf '%s\n' "Error: local build failed; refusing to silently fall back to a downloaded binary." >&2
      printf '%s\n' "Fix the build error above, or re-run with FEADER_RSS_ALLOW_REMOTE_FALLBACK=1 to explicitly allow downloading the matching release binary instead." >&2
      exit 1
    fi
  fi
elif is_local_checkout; then
  printf '%s\n' "Local checkout detected but 'go' is not installed; downloading a prebuilt release binary instead." >&2
  fetch_binary "${staging_dir}"
else
  fetch_binary "${staging_dir}"
fi

# Every file is now built and verified in staging_dir. Commit it to the live
# plugin directory by overwriting each plugin-owned file in place (never a
# bulk directory replace) so a checkout of this repository placed directly
# at ${destination} keeps its .git history and anything else untouched.
#
# Re-validate the destination immediately before writing to it: the check
# above ran before the build/download step, which can take a while, so
# re-checking here shrinks the window an attacker has to swap ${destination}
# for a symlink to almost nothing. Then confirm we own the directory and open
# it through a file descriptor, and do every subsequent write/delete through
# that descriptor (/dev/fd/N) instead of by path — the kernel resolves a
# subpath under /dev/fd/N against the directory the descriptor already
# refers to, so a symlink substituted in after this point can no longer
# redirect these writes elsewhere.
validate_destination_not_symlink
mkdir -p "${destination}"
if [[ ! -O "${destination}" ]]; then
  printf '%s\n' "Error: destination is not owned by the current user: ${destination}" >&2
  exit 1
fi
exec {dest_fd}<"${destination}"
dest="/dev/fd/${dest_fd}"

install -m 0644 \
  "${staging_dir}/manifest.json" \
  "${staging_dir}/BarWidget.qml" \
  "${staging_dir}/Panel.qml" \
  "${staging_dir}/README.md" \
  "${staging_dir}/example-config.json" \
  "${dest}/"
install -m 0755 "${staging_dir}/${binary_name}" "${dest}/${binary_name}"

# Clean up filenames/directories from older plugin layouts that have no
# replacement above — but only when ${destination} is a separate copy target
# from ${plugin_root}. Omarchy's standard `omarchy plugin add` path clones
# straight into ${destination} and "does not execute an install hook" (see
# README.md), so the documented way to fetch the runtime binary there is to
# run this script in place, with plugin_root == destination. In that case
# scripts/ and src/ are this run's own live source tree, not legacy leftovers
# from an older layout, and rm -rf'ing them would delete the very script
# that's running plus the backup.sh/restore.sh commands README.md documents.
if [[ "$(cd -- "${destination}" && pwd)" != "${plugin_root}" ]]; then
  rm -f -- "${dest}/rss-fetch.py"
  rm -rf -- "${dest}/src" "${dest}/scripts"
fi

exec {dest_fd}<&-
trap - EXIT
rm -rf -- "${staging_dir}"

printf '%s\n' "Plugin copied to ${destination}"
omarchy plugin enable "${plugin_id}" right
printf '%s\n' "Feader RSS enabled in the right bar section."
