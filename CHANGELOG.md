# Changelog

All notable changes to Feader RSS are documented in this file.

## [Unreleased]

### Fixed
- Fixed raw markup leaking into extracted article text and RSS summaries:
  `<noscript>` (tracking pixels, lazy-load CSS fallbacks, "enable JavaScript
  to view comments" widgets like Disqus's) and `<template>` (inert content
  never meant to render) were not in the skip-tag list. `golang.org/x/net/html`
  hands back `<noscript>` content as one literal text node — tag syntax
  included — so without an explicit skip it appeared verbatim in the reader;
  `<template>` content is real child elements but is never rendered by spec.
  Affected roughly half of the cached articles in real-world testing.

### Changed
- Simplified the reader's visual language: removed decorative symbols
  (★ ☆ ⚡ ⚠ 📁) from badges, the save button, the folder filter, and error
  text, keeping the shared Omarchy button icon set untouched. `Color.accent`
  is now reserved for the unread count and keyboard selection; the article
  title, the "UNREAD" badge, and the unread left border no longer carry
  color, relying on font weight and label text instead. The unread inbox
  summary card is now a neutral panel with only its count in accent.
- Improved article-reading typography: the RSS summary and full article text
  now use proportional line-height for better paragraph readability, and the
  "RSS SUMMARY"/"FULL ARTICLE" section labels use slight letter-spacing.

### Security
- `safefetch` now falls back across every validated public IP address for a
  host (previously only the first) when connecting and following redirects,
  so a host with multiple or mixed-family DNS answers no longer fails
  outright when one address is unreachable.
- The SQLite state directory, database file, and its WAL/SHM sidecars are now
  restricted to `0700`/`0600`; backup snapshots and their directory follow
  the same restriction, so other local users cannot read feed content or
  read/unread state from the local cache.
- Article ids are now derived from the feed's URL instead of its editable
  display name, so renaming a feed no longer discards its read/starred/tag
  history or re-triggers "new article" notifications for articles already
  seen. Existing databases are migrated automatically the next time each
  configured feed is fetched.
- Article links are now canonicalized (lowercased scheme/host, default port
  and fragment stripped) before being stored or hashed into an id, so two
  publisher URLs that are guaranteed equivalent by the URL spec no longer
  produce duplicate cache entries. The path and query string, where a real
  difference in meaning is possible, are left untouched.
- Feed configuration loaded from disk (a hand-edited file or an OPML import)
  is now validated and capped at 8 feeds, matching the settings UI: invalid
  or duplicate URLs are dropped, and `opml-import` reports `truncated: true`
  when the merged list exceeded the limit.
- `scripts/install.sh` now builds/downloads the binary into a staging
  directory and verifies everything before touching the live plugin
  directory, so a failed build, download, or attestation check leaves the
  previous installation untouched instead of partially replaced. A failed
  local build no longer silently falls back to a downloaded binary; that now
  requires the explicit `FEADER_RSS_ALLOW_REMOTE_FALLBACK=1` environment
  variable, or the install fails with a diagnostic.
- The downloaded release binary's attestation is now also checked against
  the exact release workflow file and the source tag it must have been
  built from (`--signer-workflow`, `--source-ref`), not just the repository.
- CI now fails a release if `manifest.json`'s version does not match the
  git tag being released.

### Fixed
- Fixed the save/star button in the reader: it silently failed on every
  click because the `--value` flag was passed as two separate process
  arguments, which Go's flag parser does not accept for a boolean flag.
- Fixed a QML reference error (`Cannot read property 'running' of
  undefined`) in the article list's status line, which fired on every
  re-render because it addressed the search process through a nonexistent
  `root.searchProcess` instead of `searchProcess`.
- Fixed silent persistence failures: saving feed settings previously reported
  "Feeds saved" even when the write to `rss-reader.json` failed (e.g. a
  dangling symlink, a missing directory, or a permissions problem), losing
  the change with no indication anything went wrong. The panel now reports
  the actual outcome (`onSaved`/`onSaveFailed`), surfaces a non-`FileNotFound`
  config load failure the same way, and creates the config directory
  upfront alongside the existing state directory.
- `rss-reader.json` is now written atomically (temp file + rename), matching
  the existing UI-preferences file, so a crash or power loss mid-write can no
  longer leave a truncated config that silently loads as "no feeds
  configured".
- Added a test exercising the database the way the plugin actually uses it:
  many short-lived connections against the same `items.db` file mutating and
  reading concurrently, confirming WAL mode and the existing busy timeout
  absorb real cross-process contention without errors or lost writes.

## [0.3.3] - 2026-09-02

### Added
- Added behavioral coverage for the CLI commands, including fetch, search,
  article caching, prefetch, migration, backup/restore, OPML, and mutations.
- Added edge-case coverage for parsers, safe networking, retry/backoff,
  SQLite retention and migration, and OPML serialization.

### Changed
- Renamed `TODO-go-migration.md` to `TODO.md` as the ongoing project roadmap.

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
