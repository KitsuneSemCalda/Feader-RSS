# Feader RSS

A persistent RSS reader for the Omarchy shell, built as a Quickshell plugin.

<p align="center">
  <img src="screenshots/0.3.0/HasNewFeeds.png" alt="Feader RSS bar icon with unread articles" height="48">
  <img src="screenshots/0.3.0/AllFeedsReaded.png" alt="Feader RSS bar icon with all articles read" height="48">
</p>

<p align="center">
  <img src="screenshots/0.3.0/FeaderMainPage.png" alt="Feader RSS article list panel" width="30%">
  <img src="screenshots/0.3.0/FeaderArticle.png" alt="Feader RSS article detail view" width="30%">
  <img src="screenshots/0.3.0/FeaderSettings.png" alt="Feader RSS feed configuration panel" width="30%">
</p>

## Local installation

To install a local copy during development:

```bash
./scripts/install.sh
```

The installer validates the manifest, removes the previous plugin files, and
copies only the required files to
`~/.config/omarchy/plugins/io.github.kitsunesemcalda.feader-rss/`, then enables
the widget in the right bar section. It preserves your feed configuration and
creates a timestamped backup of the configuration, UI preferences, and SQLite
article database, then keeps the active article cache so read/unread state
persists across updates. If the shell does not detect the copy immediately, run
`omarchy restart shell`.

Backups are stored separately in
`~/.local/state/omarchy/rss-reader/backups/`. To create one manually, run
`./scripts/backup.sh`.
Each backup contains `rss-reader.json`, `preferences.json`, and an `items.db`
snapshot when those files exist. The SQLite snapshot uses SQLite's online
backup API, so committed data in the WAL is included and no `-wal`/`-shm`
sidecars need to be copied. Older backups containing only `items.json` remain
accepted for compatibility.
To restore a backup, run `./scripts/restore.sh /path/to/backup`; the current
data is backed up first, and an `items.db` snapshot replaces the active
database even when it already contains articles.

## Distribution through the Omarchy plugin system

The standard distribution format is a public Git repository with
`manifest.json` at its root. After publishing this repository, users can
install it with:

```bash
omarchy plugin add https://github.com/KitsuneSemCalda/Feader-RSS.git --enable
```

Omarchy validates the manifest before copying the plugin to
`~/.config/omarchy/plugins/`. The shell does not execute an install hook.

The runtime requires Omarchy's Quattro shell and network access to the
configured RSS/Atom feeds. It runs with the user's permissions inside the
long-running shell process. The plugin invokes the `feader-rss-fetch` Go
binary (built locally or downloaded as a checksum-verified release asset by
`scripts/install.sh`), creates the state directory with `mkdir`, opens article
URLs in the browser, and may call `omarchy-notification-send` for new posts.
No elevated privileges, background service, or remote build is required.

## Feeds and persistence

Create `~/.config/omarchy/rss-reader.json`:

```json
{
  "maxItems": 200,
  "retentionItems": 1000,
  "refreshMinutes": 5,
  "feeds": []
}
```

`maxItems` controls how many articles the panel displays. `retentionItems`
controls how many articles are kept in SQLite; use `0` to keep everything.
Read and unsaved articles are pruned before unread or saved articles.

`refreshMinutes` accepts `1`, `2`, `3`, `4`, `5`, `15`, `30`, `60`, or `300`.
The panel exposes `1 min`, `5 min`, `15 min`, `30 min`, `1 hour`, and `5 hours`;
the default is to refresh feeds every 5 minutes. The longer intervals are useful
for feeds that publish less often and reduce network traffic.

You can also open the reader and choose **Configure feeds** to edit the feed
name, optional folder, and URL without leaving the application. The
configuration screen supports adding and removing feeds, validation of
`http`/`https` URLs, keyboard focus, arrow-key navigation, `Enter`, `Tab`, and
`Escape`. Press `s` from the reader to open it directly. Duplicate feed URLs
are rejected.

OPML subscription lists can be exchanged with the Go helper. Import replaces
the configured feeds by default; pass `--merge` to append only new URLs:

```bash
~/.config/omarchy/plugins/io.github.kitsunesemcalda.feader-rss/feader-rss-fetch \
  opml-export --config ~/.config/omarchy/rss-reader.json --output subscriptions.opml
~/.config/omarchy/plugins/io.github.kitsunesemcalda.feader-rss/feader-rss-fetch \
  opml-import --config ~/.config/omarchy/rss-reader.json --input subscriptions.opml --merge
```

### Keyboard shortcuts

While the reader has focus: `↑`/`↓` moves the selection, `Enter` opens the
selected article, `←` returns to the list from an open article, and `Tab`
switches to the next bar panel. The single-key shortcuts below can be
remapped with an optional `shortcuts` object in the config file — any subset
of actions can be overridden, unset ones keep their default:

```json
{
  "shortcuts": {
    "refresh": "r",
    "settings": "s",
    "search": "/",
    "markAllRead": "a",
    "openArticle": "o",
    "filterAll": "1",
    "filterUnread": "2",
    "filterRead": "3",
    "filterStarred": "4"
  }
}
```

`refresh` re-fetches all feeds, `settings` opens **Configure feeds**, `search`
focuses the search field, `markAllRead` prompts to mark every article read,
`openArticle` opens the selected (or currently open) article in the browser,
and `filterAll`/`filterUnread`/`filterRead`/`filterStarred` switch the
read-state or saved filter.
Each value must be a single character; the current bindings are always shown
at the bottom of the article list.

The reader provides full-text search across titles, summaries, cached article
content, authors, categories, and user tags; filters for all/read/unread/saved
articles; and feed/folder filters. Saved articles and tags can be edited from
the article detail view. These preferences are stored with the article state
and restored when the reader starts. Failed feeds are reported in the status
line without hiding articles successfully loaded from the other feeds.

Articles are stored in a SQLite database at
`~/.local/state/omarchy/rss-reader/items.db` (a legacy `items.json` from
earlier versions is imported automatically and left in place). The backup and
restore scripts include both the current SQLite database and that legacy file
when present. Left-click
opens the reader,
middle-click refreshes it, and right-click opens the first unread article.
Clicking an article marks it as read and loads the full text inside the reader;
the detail view also offers saved/tag controls and a button to open the original
page in the browser. The unread badge and notification count the complete
configured-feed database, not only the first `maxItems` visible rows. Feed
refreshes retry transient network/5xx failures with exponential backoff, while
failed article prefetches are remembered and delayed for up to six hours.

## Theme and notifications

The panel uses Omarchy's live `Color` and `Style` tokens, so surfaces, focus
states, spacing, typography, and buttons follow the active theme. When a
refresh finds posts that were not present in the persisted article store, the
plugin sends one low-urgency Omarchy notification summarizing the new posts.
The initial import does not notify, so a fresh installation does not produce a
burst of alerts.

## Tests

Run the dependency-free tests, backup round-trip checks, and Omarchy validation
checks with:

```bash
python3 -m unittest discover -s tests -v
go test -race ./...
omarchy plugin validate .
qmllint -I "$OMARCHY_PATH/shell" BarWidget.qml Panel.qml
```

GitHub Actions runs the Python tests, manifest validation, installer syntax
checks, whitespace checks, and the Go backend tests/build on pushes and pull
requests. Pushing a tag such as `v0.1.0` creates a GitHub Release with a
plugin archive and the cross-compiled `feader-rss-fetch` binaries attached.
