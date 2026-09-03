# Changelog

All notable changes to Feader RSS are documented in this file.

## [Unreleased]

## [0.3.2] - 2026-09-03

### Added
- Backup and restore now use SQLite's online backup API, preserving the active
  `items.db` (including committed WAL state) and restoring it over an existing
  non-empty database.
- Backups now include UI preferences alongside feed configuration and article
  state, while retaining compatibility with legacy `items.json` backups.
- Added sparse refresh intervals (`15 min`, `30 min`, `1 hour`, and `5 hours`)
  alongside the existing minute intervals.
- Added SQLite retention, global unread counts, FTS5 search, saved articles,
  user tags, folder filters, OPML import/export, feed deduplication, and
  transient fetch retry/backoff.

### Changed
- Refined the Quickshell reader UI with an unread inbox summary, clearer
  action affordances, hover feedback on article cards, a retry banner for
  failed feeds, and a debounced search field with a clear action.

## [0.3.1] - 2026-08-28

### Security
- `scripts/install.sh` now fails closed if `checksums.txt` cannot be fetched, instead of warning and installing the binary unverified.
- Added a build-provenance attestation check (`gh attestation verify`) so the downloaded binary is cryptographically bound to the exact commit/workflow run that built it, rather than trusting a checksum sourced from the same mutable release.
- The release workflow now generates a signed attestation (`actions/attest-build-provenance`) for every release binary, `checksums.txt`, and the plugin archive.
- `internal/safefetch` now rejects HTTP responses outside the 2xx range before parsing or storing any content.

### Fixed
- Fixed a race condition in the release workflow where parallel per-platform build jobs wrote to a shared `checksums.txt`; each platform now produces its own checksum file, combined and validated (exactly 4 entries) in the release job.

### Added
- Feeds now carry author and category metadata (RSS `<author>`/`dc:creator`, Atom `<author>`), shown in the article list and detail view.
- Article summaries are now extracted with an HTML parser instead of a tag-stripping regexp, preserving paragraph breaks and excluding `script`/`style` content.
- Relative article links are now resolved against the feed's URL.
- Articles are sorted by a normalized `published_at` timestamp (parsed from RSS/Atom date formats) instead of raw string comparison, with existing SQLite databases migrated and backfilled automatically.
- `Panel.qml` now respects `XDG_CONFIG_HOME` for the feed configuration path.
- Clearer empty states and an inline "Configure feeds" action when no feed is set up yet.

### Removed
- A leftover `console.warn("DEBUG ...")` that fired on every unread-notification update.

## [0.3.0] - 2026-08-27

### Changed
- Rewrote the fetch/parse backend in Go (`cmd/feader-rss-fetch`, `internal/`), replacing `rss-fetch.py`. The binary keeps the same CLI contract with the QML frontend and ships as a prebuilt release asset per platform (linux/darwin, amd64/arm64) with checksum verification.
- Replaced the JSON article store with a SQLite-backed store (`internal/store`, pure-Go `modernc.org/sqlite`, no cgo).
- `scripts/install.sh` now builds the Go binary locally when a `.git` checkout and `go` toolchain are present, otherwise downloads and checksum-verifies the matching release binary from GitHub.
- Reworked the panel UI (`Panel.qml`) for better UX and responsiveness.

### Added
- `scrollStep` and `panelGap` configuration options in `example-config.json`.
- Go module (`go.mod`, `go.sum`) and CI job running `go test -race ./...`.
- Release workflow now cross-compiles and publishes `feader-rss-fetch` binaries for each supported platform alongside the plugin archive.

### Removed
- `rss-fetch.py` and its Python test suite (`tests/test_rss_fetch.py`), superseded by the Go backend.

## [0.2.0] - 2026-08-XX

### Added
- Marketplace preview image (`preview.png`).

### Fixed
- Closed a DNS-rebinding SSRF gap by pinning outgoing fetch connections to the already-validated address.
- Blocked SSRF to internal, loopback, and link-local destinations.
- Hardened URL handling and rendering in feed and article views.
- Manifest now reports the correct GitHub username as author.

## [0.1.0] - Initial release

### Added
- Persistent RSS/Atom reader as an Omarchy bar-widget plugin.
- Feed configuration UI (add/remove feeds, URL validation, keyboard navigation).
- Article list with search, read/unread/all filters, and per-feed filtering.
- Local persistence of feed configuration and article read state, with backup/restore scripts.
- Theming via Omarchy's `Color`/`Style` tokens and low-urgency notifications for new articles.
