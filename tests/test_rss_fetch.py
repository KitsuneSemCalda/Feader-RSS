import importlib.util
import io
import json
import unittest
from pathlib import Path
from unittest.mock import patch


MODULE_PATH = Path(__file__).parents[1] / "rss-fetch.py"
SPEC = importlib.util.spec_from_file_location("rss_fetch", MODULE_PATH)
rss_fetch = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(rss_fetch)


RSS = b'''<?xml version="1.0"?>
<rss version="2.0"><channel>
  <item>
    <title> A useful article </title>
    <link>https://example.org/article</link>
    <pubDate>Sat, 22 Aug 2026 12:00:00 GMT</pubDate>
    <description><![CDATA[<p>Hello <b>RSS</b>.</p>]]></description>
  </item>
</channel></rss>'''


class Response:
    def __enter__(self):
        return io.BytesIO(RSS)

    def __exit__(self, *_):
        return False


class RssFetchTests(unittest.TestCase):
    def test_article_parser_extracts_readable_text(self):
        parser = rss_fetch.ArticleParser()
        parser.feed("""
            <html><head><title>Full story</title><style>hidden()</style></head>
            <body><nav>Menu</nav><article><h1>Full story</h1>
            <p>First paragraph.</p><p>Second <b>paragraph</b>.</p>
            </article><footer>Footer</footer></body></html>
        """)

        title, content = parser.result()
        self.assertEqual(title, "Full story")
        self.assertIn("First paragraph.", content)
        self.assertIn("Second paragraph.", content)
        self.assertNotIn("Menu", content)
        self.assertNotIn("hidden()", content)

    @patch.object(rss_fetch.urllib.request, "urlopen", return_value=Response())
    def test_parse_feed_normalizes_article_fields(self, _urlopen):
        items = rss_fetch.parse_feed("Example", "https://example.org/feed.xml")

        self.assertEqual(len(items), 1)
        self.assertEqual(items[0]["title"], "A useful article")
        self.assertEqual(items[0]["summary"], "Hello RSS.")
        self.assertEqual(items[0]["feed"], "Example")
        self.assertFalse(items[0]["read"])

    @patch.object(rss_fetch.urllib.request, "urlopen", side_effect=OSError("offline"))
    def test_main_keeps_json_output_when_a_feed_fails(self, _urlopen):
        with patch("sys.argv", ["rss-fetch.py", "--limit", "2", "Offline", "https://example.org/feed.xml"]):
            with patch("sys.stdout", new_callable=io.StringIO) as stdout:
                rss_fetch.main()

        self.assertEqual(json.loads(stdout.getvalue()), {
            "items": [],
            "errors": [{"feed": "Offline", "url": "https://example.org/feed.xml", "error": "offline"}],
        })


if __name__ == "__main__":
    unittest.main()
