#!/usr/bin/env python3
"""Fetch a small, dependency-free RSS/Atom snapshot for the Quickshell UI."""
import argparse
import hashlib
import html
import re
import sys
import urllib.request
import xml.etree.ElementTree as ET


def text(node, *names):
    for name in names:
        found = node.find(name)
        if found is not None and (found.text or "").strip():
            return " ".join((found.text or "").split())
    return ""


def clean(value):
    value = html.unescape(value or "")
    value = re.sub(r"<[^>]+>", " ", value)
    return " ".join(value.split())


def parse_feed(name, url):
    request = urllib.request.Request(url, headers={"User-Agent": "io.github.kitsunesemcalda.feader-rss/0.1"})
    with urllib.request.urlopen(request, timeout=15) as response:
        root = ET.fromstring(response.read())
    channel = root.find("channel")
    entries = list(channel) if channel is not None else list(root)
    items = []
    for entry in entries:
        if entry.tag.rsplit("}", 1)[-1] not in ("item", "entry"):
            continue
        title = text(entry, "title", "{*}title")
        link = text(entry, "link", "{*}link")
        if not link:
            link_node = entry.find("{*}link")
            link = (link_node.attrib.get("href", "") if link_node is not None else "")
        published = text(entry, "pubDate", "published", "updated", "{*}published", "{*}updated")
        summary = clean(text(entry, "description", "summary", "content", "{*}summary", "{*}content"))
        if not title or not link:
            continue
        article_id = hashlib.sha256((name + "\0" + link).encode()).hexdigest()[:24]
        items.append({"id": article_id, "title": title, "url": link, "feed": name,
                      "published": published, "summary": summary[:500], "read": False})
    return items


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--limit", type=int, default=200)
    parser.add_argument("feeds", nargs="*")
    args = parser.parse_args()
    items = []
    for index in range(0, len(args.feeds) - 1, 2):
        name, url = args.feeds[index:index + 2]
        try:
            items.extend(parse_feed(name, url))
        except Exception as error:
            print(f"{name}: {error}", file=sys.stderr)
    items.sort(key=lambda item: item.get("published", ""), reverse=True)
    print(__import__("json").dumps({"items": items[:args.limit]}, ensure_ascii=False))


if __name__ == "__main__":
    main()
