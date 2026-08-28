# Changelog

All notable changes to Feader RSS are documented in this file.

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
