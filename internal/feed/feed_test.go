package feed

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/KitsuneSemCalda/feader-rss/internal/safefetch"
)

const rssSample = `<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Sample Feed</title>
    <item>
      <title>Hello &amp; World</title>
      <link>https://example.com/a</link>
      <pubDate>Wed, 02 Oct 2024 15:00:00 GMT</pubDate>
      <author>alice@example.com</author>
      <category>News</category><category> news </category>
      <description><![CDATA[<p>Some <b>summary</b> text.</p>]]></description>
    </item>
    <item>
      <title>No link, should be skipped</title>
      <description>irrelevant</description>
    </item>
  </channel>
</rss>`

const atomSample = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Sample Atom Feed</title>
  <entry>
    <title>Atom Entry</title>
    <link rel="alternate" href="https://example.com/b"/>
    <link rel="self" href="https://example.com/feed.atom"/>
    <author><name>Alice</name></author>
    <category term="Technology"/>
    <category term="technology"/>
    <published>2024-10-02T15:00:00Z</published>
    <summary>An atom summary.</summary>
  </entry>
</feed>`

func TestParseRSS(t *testing.T) {
	items, err := Parse("Sample", "https://example.com/feed.xml", []byte(rssSample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item (missing-link item skipped), got %d", len(items))
	}
	item := items[0]
	if item.Title != "Hello & World" {
		t.Errorf("title = %q, want %q", item.Title, "Hello & World")
	}
	if item.URL != "https://example.com/a" {
		t.Errorf("url = %q", item.URL)
	}
	if item.Summary != "Some summary text." {
		t.Errorf("summary = %q", item.Summary)
	}
	if item.Published != "Wed, 02 Oct 2024 15:00:00 GMT" {
		t.Errorf("published = %q", item.Published)
	}
	if item.Author != "alice@example.com" {
		t.Errorf("author = %q, want %q", item.Author, "alice@example.com")
	}
	if len(item.Categories) != 1 || item.Categories[0] != "News" {
		t.Errorf("categories = %#v, want [News]", item.Categories)
	}
	if item.PublishedAt == 0 {
		t.Error("expected RSS publication timestamp")
	}
	wantID := ArticleID("https://example.com/feed.xml", "https://example.com/a")
	if item.ID != wantID {
		t.Errorf("id = %q, want %q", item.ID, wantID)
	}
	if len(item.ID) != 24 {
		t.Errorf("id length = %d, want 24", len(item.ID))
	}
	if renamedID, err := Parse("Renamed", "https://example.com/feed.xml", []byte(rssSample)); err != nil {
		t.Fatalf("Parse renamed: %v", err)
	} else if renamedID[0].ID != wantID {
		t.Errorf("id changed after renaming the feed: %q, want %q", renamedID[0].ID, wantID)
	}
}

func TestParseAtom(t *testing.T) {
	items, err := Parse("Sample", "https://example.com/feed.atom", []byte(atomSample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0]
	if item.Title != "Atom Entry" {
		t.Errorf("title = %q", item.Title)
	}
	if item.URL != "https://example.com/b" {
		t.Errorf("url = %q, want the rel=alternate link", item.URL)
	}
	if item.Published != "2024-10-02T15:00:00Z" {
		t.Errorf("published = %q", item.Published)
	}
	if item.Summary != "An atom summary." {
		t.Errorf("summary = %q", item.Summary)
	}
	if item.Author != "Alice" {
		t.Errorf("author = %q, want %q", item.Author, "Alice")
	}
	if len(item.Categories) != 1 || item.Categories[0] != "Technology" {
		t.Errorf("categories = %#v, want [Technology]", item.Categories)
	}
	if item.PublishedAt == 0 {
		t.Error("expected Atom publication timestamp")
	}
}

func TestParseUnsupportedFormat(t *testing.T) {
	_, err := Parse("Sample", "https://example.test/feed", []byte(`<html><body>not a feed</body></html>`))
	if err == nil {
		t.Fatal("expected error for unsupported root element")
	}
}

func TestParseInvalidXML(t *testing.T) {
	_, err := Parse("Sample", "https://example.test/feed", []byte(`not xml at all`))
	if err == nil {
		t.Fatal("expected error for invalid XML")
	}
}

func TestSummaryTruncation(t *testing.T) {
	longDesc := "<p>" + strings.Repeat("word ", 200) + "</p>"
	xmlDoc := `<rss version="2.0"><channel><item>
		<title>Long</title>
		<link>https://example.com/long</link>
		<description>` + longDesc + `</description>
	</item></channel></rss>`
	items, err := Parse("Sample", "https://example.test/feed", []byte(xmlDoc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items[0].Summary) > MaxSummaryLength {
		t.Errorf("summary length = %d, want <= %d", len(items[0].Summary), MaxSummaryLength)
	}
}

func TestSummaryCleaningSkipsScriptsAndKeepsParagraphs(t *testing.T) {
	xmlDoc := `<rss version="2.0"><channel><item>
		<title><![CDATA[<b>Readable title</b>]]></title>
		<link>https://example.com/clean</link>
		<description><![CDATA[<p>First paragraph.</p><script>secret()</script><p>Second paragraph.</p>]]></description>
	</item></channel></rss>`
	items, err := Parse("Sample", "https://example.test/feed", []byte(xmlDoc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := items[0].Title; got != "Readable title" {
		t.Errorf("title = %q, want cleaned title", got)
	}
	if strings.Contains(items[0].Summary, "secret()") {
		t.Errorf("script content leaked into summary: %q", items[0].Summary)
	}
	if !strings.Contains(items[0].Summary, "First paragraph.\n\nSecond paragraph.") {
		t.Errorf("paragraph boundary was lost: %q", items[0].Summary)
	}
}

func TestSummaryCleaningSkipsNoscriptContent(t *testing.T) {
	// See the matching regression in internal/article: golang.org/x/net/html
	// hands back a <noscript> element's entire content (tracking-pixel <img>
	// tags, lazy-load <style> fallbacks) as one literal text node, which
	// leaks raw markup into the summary unless explicitly skipped.
	xmlDoc := `<rss version="2.0"><channel><item>
		<title>Noscript</title>
		<link>https://example.com/noscript</link>
		<description><![CDATA[<p>Visible.</p><noscript><img src="https://example.com/pixel.gif"><style>.x{display:none}</style></noscript>]]></description>
	</item></channel></rss>`
	items, err := Parse("Sample", "https://example.test/feed", []byte(xmlDoc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	summary := items[0].Summary
	if strings.Contains(summary, "<img") || strings.Contains(summary, "<style") || strings.Contains(summary, "display:none") {
		t.Errorf("noscript markup leaked into summary: %q", summary)
	}
	if !strings.Contains(summary, "Visible.") {
		t.Errorf("expected visible text to survive, got: %q", summary)
	}
}

func TestSummaryCleaningSkipsTemplateContent(t *testing.T) {
	xmlDoc := `<rss version="2.0"><channel><item>
		<title>Template</title>
		<link>https://example.com/template</link>
		<description><![CDATA[<p>Visible.</p><template><p>Hidden template content.</p></template>]]></description>
	</item></channel></rss>`
	items, err := Parse("Sample", "https://example.test/feed", []byte(xmlDoc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	summary := items[0].Summary
	if strings.Contains(summary, "Hidden template content") {
		t.Errorf("template content leaked into summary: %q", summary)
	}
	if !strings.Contains(summary, "Visible.") {
		t.Errorf("expected visible text to survive, got: %q", summary)
	}
}

func TestRSSPrefersEncodedContentWhenAvailable(t *testing.T) {
	xmlDoc := `<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><item>
		<title>Rich content</title><link>https://example.com/rich</link>
		<description>Short description</description>
		<content:encoded><![CDATA[<p>Fuller content with <b>formatting</b>.</p>]]></content:encoded>
	</item></channel></rss>`
	items, err := Parse("Sample", "https://example.test/feed", []byte(xmlDoc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := items[0].Summary; got != "Fuller content with formatting." {
		t.Errorf("summary = %q, want content:encoded text", got)
	}
}

func TestSummaryTruncationPreservesUTF8(t *testing.T) {
	longDesc := "<p>" + strings.Repeat("á", MaxSummaryLength+100) + "</p>"
	xmlDoc := `<rss version="2.0"><channel><item>
		<title>Unicode</title><link>https://example.com/unicode</link>
		<description>` + longDesc + `</description>
	</item></channel></rss>`
	items, err := Parse("Sample", "https://example.test/feed", []byte(xmlDoc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := items[0].Summary; len([]rune(got)) > MaxSummaryLength || !utf8.ValidString(got) {
		t.Errorf("summary was not safely truncated: rune length=%d valid=%v", len([]rune(got)), utf8.ValidString(got))
	}
}

func TestParseResolvesRelativeLinks(t *testing.T) {
	base, err := url.Parse("https://example.com/news/feed.xml")
	if err != nil {
		t.Fatalf("Parse base URL: %v", err)
	}
	xmlDoc := `<rss version="2.0"><channel><item>
		<title>Relative</title><link>/story/one</link>
	</item></channel></rss>`
	items, err := parse("Sample", []byte(xmlDoc), base)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 || items[0].URL != "https://example.com/story/one" {
		t.Fatalf("resolved URL = %#v", items)
	}
}

func TestResolveLinkCanonicalizesSchemeHostPortAndFragment(t *testing.T) {
	base, err := url.Parse("https://example.com/feed.xml")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	cases := []struct {
		name string
		link string
		want string
	}{
		{"lowercases scheme and host", "HTTPS://Example.COM/Path", "https://example.com/Path"},
		{"drops default https port", "https://example.com:443/x", "https://example.com/x"},
		{"drops default http port", "http://example.com:80/x", "http://example.com/x"},
		{"keeps a non-default port", "https://example.com:8443/x", "https://example.com:8443/x"},
		{"keeps a non-default port for http", "http://example.com:443/x", "http://example.com:443/x"},
		{"drops the fragment", "https://example.com/x?q=1#section", "https://example.com/x?q=1"},
		{"preserves path and query case exactly", "https://example.com/Path?Query=Value", "https://example.com/Path?Query=Value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveLink(c.link, base); got != c.want {
				t.Errorf("resolveLink(%q) = %q, want %q", c.link, got, c.want)
			}
		})
	}
}

func TestPublishedTimestampSupportsRSSAndAtom(t *testing.T) {
	rss := PublishedTimestamp("Wed, 02 Oct 2024 15:00:00 GMT")
	atom := PublishedTimestamp("2024-10-02T15:00:00Z")
	if rss == 0 || atom == 0 || rss != atom {
		t.Errorf("RSS timestamp=%d Atom timestamp=%d", rss, atom)
	}
}

func TestArticleIDStable(t *testing.T) {
	id1 := articleID("feed", "https://example.com/x")
	id2 := articleID("feed", "https://example.com/x")
	if id1 != id2 {
		t.Errorf("articleID is not deterministic: %q != %q", id1, id2)
	}
	id3 := articleID("other-feed", "https://example.com/x")
	if id1 == id3 {
		t.Errorf("articleID should depend on feed name")
	}
}

func TestHelpersHandleFallbacksAndEmptyValues(t *testing.T) {
	if got := cleanFallback(`<p>Hello &amp; <b>world</b></p><script>secret()</script>`); got != "Hello & world secret()" {
		t.Errorf("cleanFallback = %q", got)
	}
	if got := truncateRunes("hello", 0); got != "" {
		t.Errorf("truncateRunes zero = %q", got)
	}
	if got := truncateRunes("hello", 10); got != "hello" {
		t.Errorf("truncateRunes within limit = %q", got)
	}
	if got := truncateRunes("hello", 3); got != "hel" {
		t.Errorf("truncateRunes over limit = %q", got)
	}
	if got := PublishedTimestamp(""); got != 0 {
		t.Errorf("empty PublishedTimestamp = %d", got)
	}
	if got := normalizeCategories("", " News ", "news", "Tech"); len(got) != 2 || got[0] != "News" || got[1] != "Tech" {
		t.Errorf("normalizeCategories = %#v", got)
	}
	if got := resolveLink("/path", nil); got != "/path" {
		t.Errorf("resolveLink without base = %q", got)
	}
	base, err := url.Parse("https://example.test/feed")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	if got := resolveLink(":bad", base); got != ":bad" {
		t.Errorf("resolveLink invalid reference = %q", got)
	}
	if got := atomAuthorName([]atomAuthor{{Email: "author@example.test"}}); got != "author@example.test" {
		t.Errorf("email author = %q", got)
	}
	if got := atomAuthorName(nil); got != "" {
		t.Errorf("empty author = %q", got)
	}
	if got := atomLinkHref(nil); got != "" {
		t.Errorf("empty atom links = %q", got)
	}
	if got := atomLinkHref([]atomLink{{Rel: "self", Href: "https://example.test/self"}}); got != "https://example.test/self" {
		t.Errorf("fallback atom link = %q", got)
	}
	if got := atomLinkHref([]atomLink{{Href: "https://example.test/default"}}); got != "https://example.test/default" {
		t.Errorf("default atom link = %q", got)
	}
}

func TestParseSkipsEntriesWithoutTitleOrLink(t *testing.T) {
	xmlDoc := `<rss version="2.0"><channel>
		<item><title></title><link>https://example.test/no-title</link></item>
		<item><title>No URL</title><link></link></item>
	</channel></rss>`
	items, err := Parse("Sample", "https://example.test/feed", []byte(xmlDoc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %+v, want no incomplete entries", items)
	}

	atomDoc := `<feed xmlns="http://www.w3.org/2005/Atom"><entry><title></title><link href="https://example.test/no-title"/></entry><entry><title>No URL</title></entry></feed>`
	items, err = Parse("Atom", "https://example.test/feed.atom", []byte(atomDoc))
	if err != nil {
		t.Fatalf("Parse Atom: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("Atom items = %+v, want no incomplete entries", items)
	}
}

func TestFetchUsesGetterAndFetchWithRetry(t *testing.T) {
	oldGet := get
	t.Cleanup(func() { get = oldGet })
	get = func(url string, timeout time.Duration) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`<rss version="2.0"><channel><item><title>Fetched</title><link>/item</link></item></channel></rss>`)),
		}, nil
	}
	items, err := Fetch("Example", "https://example.test/feed.xml")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) != 1 || items[0].URL != "https://example.test/item" {
		t.Fatalf("fetched items = %+v", items)
	}

	get = func(string, time.Duration) (*http.Response, error) {
		return nil, errors.New("feed transport failed")
	}
	if _, err := Fetch("Example", "https://example.test/feed.xml"); err == nil || err.Error() != "feed transport failed" {
		t.Errorf("Fetch getter error = %v", err)
	}
}

func TestFetchWithRetryRetriesTransientFailuresAndNormalizesOptions(t *testing.T) {
	oldFetch, oldSleep := fetchFeed, sleep
	t.Cleanup(func() {
		fetchFeed = oldFetch
		sleep = oldSleep
	})
	var delays []time.Duration
	sleep = func(delay time.Duration) { delays = append(delays, delay) }
	calls := 0
	fetchFeed = func(name, rawURL string) ([]Item, error) {
		calls++
		if calls <= 5 {
			return nil, &safefetch.HTTPStatusError{Code: http.StatusBadGateway, Status: "502 Bad Gateway"}
		}
		return []Item{{ID: "recovered", Feed: name, Title: "Recovered", URL: rawURL}}, nil
	}
	items, err := FetchWithRetry("Example", "https://example.test/retry", 6, time.Second)
	if err != nil {
		t.Fatalf("FetchWithRetry: %v", err)
	}
	if len(items) != 1 || items[0].ID != "recovered" || calls != 6 {
		t.Fatalf("retry result=%+v calls=%d", items, calls)
	}
	wantDelays := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	if len(delays) != len(wantDelays) {
		t.Fatalf("retry delays = %v, want %v", delays, wantDelays)
	}
	for i := range delays {
		if delays[i] != wantDelays[i] {
			t.Errorf("delay %d = %s, want %s", i, delays[i], wantDelays[i])
		}
	}

	calls = 0
	delays = nil
	fetchFeed = func(string, string) ([]Item, error) {
		calls++
		return nil, errors.New("invalid feed")
	}
	if items, err := FetchWithRetry("Example", "bad", 0, -time.Second); err == nil || items != nil || calls != 1 {
		t.Fatalf("permanent retry result=%+v err=%v calls=%d", items, err, calls)
	}
}
