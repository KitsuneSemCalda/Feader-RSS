#!/usr/bin/env python3
"""Fetch a small, dependency-free RSS/Atom snapshot for the Quickshell UI."""
import argparse
import hashlib
import html
import json
import re
import sys
import urllib.request
from urllib.parse import urlparse
import xml.etree.ElementTree as ET
from html.parser import HTMLParser

ALLOWED_SCHEMES = {"http", "https"}
MAX_RESPONSE_BYTES = 5 * 1024 * 1024


def require_safe_url(url):
    scheme = urlparse(url).scheme.lower()
    if scheme not in ALLOWED_SCHEMES:
        raise ValueError(f"unsupported URL scheme: {scheme or '(none)'}")


class _SafeRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Re-validate the scheme of every redirect target, not just the initial URL."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        require_safe_url(newurl)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


_OPENER = urllib.request.build_opener(
    urllib.request.UnknownHandler,
    urllib.request.HTTPHandler,
    urllib.request.HTTPSHandler,
    urllib.request.HTTPDefaultErrorHandler,
    urllib.request.HTTPErrorProcessor,
    _SafeRedirectHandler,
)


def text(node, *names):
    for name in names:
        found = node.find(name)
        if found is not None and (found.text or "").strip():
            return " ".join((found.text or "").split())
    return ""


def clean(value):
    value = html.unescape(value or "")
    value = re.sub(r"<[^>]+>", " ", value)
    value = " ".join(value.split())
    return re.sub(r"\s+([,.;:!?])", r"\1", value)


def parse_feed(name, url):
    require_safe_url(url)
    request = urllib.request.Request(url, headers={"User-Agent": "io.github.kitsunesemcalda.feader-rss/0.1"})
    with _OPENER.open(request, timeout=15) as response:
        root = ET.fromstring(response.read(MAX_RESPONSE_BYTES))
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


class ArticleParser(HTMLParser):
    """Extract readable text from an HTML article using only the stdlib."""

    BLOCK_TAGS = {"article", "br", "div", "h1", "h2", "h3", "h4", "li", "p", "pre", "section"}
    SKIP_TAGS = {"aside", "footer", "form", "header", "nav", "script", "style", "svg"}

    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.parts = []
        self.title_parts = []
        self.skip_depth = 0
        self.in_title = False

    def handle_starttag(self, tag, attrs):
        tag = tag.lower()
        if tag in self.SKIP_TAGS:
            self.skip_depth += 1
        if tag == "title":
            self.in_title = True
        if self.skip_depth == 0 and tag in self.BLOCK_TAGS:
            self.parts.append("\n")

    def handle_endtag(self, tag):
        tag = tag.lower()
        if self.skip_depth == 0 and tag in self.BLOCK_TAGS:
            self.parts.append("\n")
        if tag == "title":
            self.in_title = False
        if tag in self.SKIP_TAGS and self.skip_depth:
            self.skip_depth -= 1

    def handle_data(self, data):
        if self.skip_depth:
            return
        value = " ".join(data.split())
        if not value:
            return
        self.parts.append(value + " ")
        if self.in_title:
            self.title_parts.append(value)

    def result(self):
        content = re.sub(r"[ \t]+", " ", "".join(self.parts))
        content = re.sub(r"\n[ \t]+", "\n", content)
        content = re.sub(r"\s+([,.;:!?])", r"\1", content)
        content = re.sub(r"\n{3,}", "\n\n", content).strip()
        title = " ".join(self.title_parts).strip()
        if not title:
            title = next((line.strip() for line in content.splitlines() if line.strip()), "Article")
        return title, content


def fetch_article(url):
    require_safe_url(url)
    request = urllib.request.Request(url, headers={"User-Agent": "io.github.kitsunesemcalda.feader-rss/0.1"})
    with _OPENER.open(request, timeout=20) as response:
        raw = response.read(MAX_RESPONSE_BYTES)
        charset = response.headers.get_content_charset() or "utf-8"
    parser = ArticleParser()
    parser.feed(raw.decode(charset, errors="replace"))
    title, content = parser.result()
    return {"url": url, "title": title, "content": content}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--limit", type=int, default=200)
    parser.add_argument("--article")
    parser.add_argument("feeds", nargs="*")
    args = parser.parse_args()
    if args.article:
        try:
            print(json.dumps(fetch_article(args.article), ensure_ascii=False))
        except Exception as error:
            print(json.dumps({"error": str(error), "url": args.article}, ensure_ascii=False))
            return 1
        return 0
    items = []
    errors = []
    for index in range(0, len(args.feeds) - 1, 2):
        name, url = args.feeds[index:index + 2]
        try:
            items.extend(parse_feed(name, url))
        except Exception as error:
            errors.append({"feed": name, "url": url, "error": str(error)})
            print(f"{name}: {error}", file=sys.stderr)
    items.sort(key=lambda item: item.get("published", ""), reverse=True)
    print(json.dumps({"items": items[:args.limit], "errors": errors}, ensure_ascii=False))


if __name__ == "__main__":
    main()
