package feed

import (
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
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
	items, err := Parse("Sample", []byte(rssSample))
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
	wantID := articleID("Sample", "https://example.com/a")
	if item.ID != wantID {
		t.Errorf("id = %q, want %q", item.ID, wantID)
	}
	if len(item.ID) != 24 {
		t.Errorf("id length = %d, want 24", len(item.ID))
	}
}

func TestParseAtom(t *testing.T) {
	items, err := Parse("Sample", []byte(atomSample))
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
	_, err := Parse("Sample", []byte(`<html><body>not a feed</body></html>`))
	if err == nil {
		t.Fatal("expected error for unsupported root element")
	}
}

func TestParseInvalidXML(t *testing.T) {
	_, err := Parse("Sample", []byte(`not xml at all`))
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
	items, err := Parse("Sample", []byte(xmlDoc))
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
	items, err := Parse("Sample", []byte(xmlDoc))
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

func TestRSSPrefersEncodedContentWhenAvailable(t *testing.T) {
	xmlDoc := `<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><item>
		<title>Rich content</title><link>https://example.com/rich</link>
		<description>Short description</description>
		<content:encoded><![CDATA[<p>Fuller content with <b>formatting</b>.</p>]]></content:encoded>
	</item></channel></rss>`
	items, err := Parse("Sample", []byte(xmlDoc))
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
	items, err := Parse("Sample", []byte(xmlDoc))
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
