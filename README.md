# Feader RSS

A persistent RSS reader for the Omarchy shell, built as a Quickshell plugin.

## Local installation

To install a local copy during development:

```bash
./install.sh
```

The installer validates the manifest, copies only the required files to
`~/.config/omarchy/plugins/io.github.kitsunesemcalda.feader-rss/`, and enables
the widget in the right bar section. If the shell does not detect the copy
immediately, run `omarchy restart shell`.

## Distribution through the Omarchy plugin system

The standard distribution format is a public Git repository with
`manifest.json` at its root. After publishing this repository, users can
install it with:

```bash
omarchy plugin add https://github.com/KitsuneSemCalda/Feader-RSS.git --enable
```

Omarchy validates the manifest before copying the plugin to
`~/.config/omarchy/plugins/`. The shell does not execute an install hook.

## Feeds and persistence

Create `~/.config/omarchy/rss-reader.json`:

```json
{
  "maxItems": 200,
  "feeds": [
    { "name": "LWN", "url": "https://lwn.net/headlines/rss" },
    { "name": "My feed", "url": "https://example.org/feed.xml" }
  ]
}
```

Articles are stored in
`~/.local/state/omarchy/rss-reader/items.json`. Left-click opens the reader,
middle-click refreshes it, and right-click opens the first unread article.
Clicking an article marks it as read and opens it in the browser.
