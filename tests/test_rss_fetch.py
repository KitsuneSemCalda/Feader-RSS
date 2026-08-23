import importlib.util
import io
import json
import socket
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


def fake_getaddrinfo(ip_by_host):
    """Build a socket.getaddrinfo replacement that never touches the network."""

    def _fake(host, *args, **kwargs):
        ip = ip_by_host.get(host)
        if ip is None:
            raise socket.gaierror(f"no fake DNS entry for {host}")
        family = socket.AF_INET6 if ":" in ip else socket.AF_INET
        return [(family, socket.SOCK_STREAM, 6, "", (ip, 0))]

    return _fake


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

    @patch.object(rss_fetch.socket, "getaddrinfo", side_effect=fake_getaddrinfo({"example.org": "93.184.216.34"}))
    @patch.object(rss_fetch._OPENER, "open", return_value=Response())
    def test_parse_feed_normalizes_article_fields(self, _urlopen, _dns):
        items = rss_fetch.parse_feed("Example", "https://example.org/feed.xml")

        self.assertEqual(len(items), 1)
        self.assertEqual(items[0]["title"], "A useful article")
        self.assertEqual(items[0]["summary"], "Hello RSS.")
        self.assertEqual(items[0]["feed"], "Example")
        self.assertFalse(items[0]["read"])

    @patch.object(rss_fetch.socket, "getaddrinfo", side_effect=fake_getaddrinfo({"example.org": "93.184.216.34"}))
    @patch.object(rss_fetch._OPENER, "open", side_effect=OSError("offline"))
    def test_main_keeps_json_output_when_a_feed_fails(self, _urlopen, _dns):
        with patch("sys.argv", ["rss-fetch.py", "--limit", "2", "Offline", "https://example.org/feed.xml"]):
            with patch("sys.stdout", new_callable=io.StringIO) as stdout:
                rss_fetch.main()

        self.assertEqual(json.loads(stdout.getvalue()), {
            "items": [],
            "errors": [{"feed": "Offline", "url": "https://example.org/feed.xml", "error": "offline"}],
        })


class UrlSchemeSecurityTests(unittest.TestCase):
    """Regression tests for the file://-URL local file disclosure bug."""

    DANGEROUS_URLS = [
        "file:///etc/passwd",
        "file://localhost/etc/passwd",
        "FILE:///etc/passwd",  # scheme check must be case-insensitive
        "ftp://attacker.example/payload",
        "data:text/html,<script>alert(1)</script>",
        "javascript:alert(1)",
        "gopher://attacker.example/",
        "",  # no scheme at all
    ]

    def test_require_safe_url_rejects_dangerous_schemes(self):
        for url in self.DANGEROUS_URLS:
            with self.subTest(url=url):
                with self.assertRaises(ValueError):
                    rss_fetch.require_safe_url(url)

    @patch.object(rss_fetch.socket, "getaddrinfo", side_effect=fake_getaddrinfo({
        "example.org": "93.184.216.34",
    }))
    def test_require_safe_url_allows_http_and_https(self, _dns):
        rss_fetch.require_safe_url("http://example.org/feed.xml")
        rss_fetch.require_safe_url("https://example.org/feed.xml")

    @patch.object(rss_fetch._OPENER, "open")
    def test_fetch_article_never_opens_a_file_url(self, urlopen):
        with self.assertRaises(ValueError):
            rss_fetch.fetch_article("file:///etc/passwd")
        urlopen.assert_not_called()

    @patch.object(rss_fetch._OPENER, "open")
    def test_parse_feed_never_opens_a_file_url(self, urlopen):
        with self.assertRaises(ValueError):
            rss_fetch.parse_feed("Local", "file:///etc/passwd")
        urlopen.assert_not_called()

    def test_redirect_to_disallowed_scheme_is_blocked(self):
        import http.server
        import threading

        class RedirectHandler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(302)
                self.send_header("Location", "ftp://attacker.example/payload")
                self.end_headers()

            def log_message(self, *_args):
                pass

        server = http.server.HTTPServer(("127.0.0.1", 0), RedirectHandler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            url = f"http://127.0.0.1:{server.server_port}/"
            with self.assertRaises(Exception):
                rss_fetch.fetch_article(url)
        finally:
            server.shutdown()
            thread.join()

    def test_cli_article_mode_reports_scheme_error_as_json_without_reading_file(self):
        with patch("sys.argv", ["rss-fetch.py", "--article", "file:///etc/passwd"]):
            with patch("sys.stdout", new_callable=io.StringIO) as stdout:
                exit_code = rss_fetch.main()

        self.assertEqual(exit_code, 1)
        payload = json.loads(stdout.getvalue())
        self.assertEqual(payload["url"], "file:///etc/passwd")
        self.assertIn("scheme", payload["error"])


class SsrfDestinationSecurityTests(unittest.TestCase):
    """A malicious/compromised feed must not be able to reach internal network destinations."""

    NON_PUBLIC_URLS = [
        "http://127.0.0.1/",
        "http://127.0.0.1:8080/admin",
        "http://localhost/",
        "http://169.254.169.254/latest/meta-data/",  # cloud metadata endpoint
        "http://192.168.1.1/",
        "http://10.0.0.5/",
        "http://172.16.0.5/",
        "http://[::1]/",
        "http://[fe80::1]/",
        "http://0.0.0.0/",
    ]

    def test_require_safe_url_rejects_loopback_private_and_link_local_ips(self):
        for url in self.NON_PUBLIC_URLS:
            with self.subTest(url=url):
                with self.assertRaises(ValueError):
                    rss_fetch.require_safe_url(url)

    @patch.object(rss_fetch.socket, "getaddrinfo", side_effect=fake_getaddrinfo({
        "public.example": "93.184.216.34",
    }))
    def test_require_safe_url_allows_a_hostname_resolving_to_a_public_ip(self, _dns):
        rss_fetch.require_safe_url("https://public.example/feed.xml")

    @patch.object(rss_fetch.socket, "getaddrinfo", side_effect=fake_getaddrinfo({
        "internal.example": "10.0.0.5",
    }))
    def test_require_safe_url_rejects_a_hostname_resolving_to_a_private_ip(self, _dns):
        with self.assertRaises(ValueError):
            rss_fetch.require_safe_url("https://internal.example/feed.xml")

    @patch.object(rss_fetch.socket, "getaddrinfo", side_effect=fake_getaddrinfo({
        "public.example": "93.184.216.34",
    }))
    def test_redirect_from_public_host_to_private_ip_is_blocked(self, _dns):
        # Simulates a public-looking feed 302-redirecting to an internal address:
        # the redirect handler must re-run require_safe_url on the new target.
        rss_fetch.require_safe_url("https://public.example/feed.xml")
        with self.assertRaises(ValueError):
            rss_fetch.require_safe_url("http://169.254.169.254/latest/meta-data/")

    @patch.object(rss_fetch._OPENER, "open")
    def test_fetch_article_never_opens_a_loopback_url(self, urlopen):
        with self.assertRaises(ValueError):
            rss_fetch.fetch_article("http://127.0.0.1/secret")
        urlopen.assert_not_called()


class ResponseSizeLimitTests(unittest.TestCase):
    """A malicious or misbehaving server must not be able to exhaust memory."""

    class HugeResponse:
        def __init__(self, payload):
            self._payload = payload

        def __enter__(self):
            buf = io.BytesIO(self._payload)
            buf.headers = type("Headers", (), {"get_content_charset": lambda self: "utf-8"})()
            return buf

        def __exit__(self, *_):
            return False

    def test_fetch_article_rejects_oversized_responses_instead_of_truncating(self):
        oversized = b"<html><body><p>" + b"A" * (rss_fetch.MAX_RESPONSE_BYTES * 2) + b"</p></body></html>"
        response = self.HugeResponse(oversized)

        with patch.object(rss_fetch._OPENER, "open", return_value=response):
            with self.assertRaises(ValueError):
                rss_fetch.fetch_article("https://example.org/huge")

    def test_fetch_article_accepts_a_response_exactly_at_the_limit(self):
        payload = b"<html><body><p>" + b"A" * (rss_fetch.MAX_RESPONSE_BYTES - 100) + b"</p></body></html>"
        self.assertLessEqual(len(payload), rss_fetch.MAX_RESPONSE_BYTES)
        response = self.HugeResponse(payload)

        with patch.object(rss_fetch._OPENER, "open", return_value=response):
            article = rss_fetch.fetch_article("https://example.org/ok")

        self.assertIn("A", article["content"])


if __name__ == "__main__":
    unittest.main()
