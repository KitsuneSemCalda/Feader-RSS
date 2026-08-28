package feed

import (
	"strings"
	"testing"
)

const rssSample = `<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Sample Feed</title>
    <item>
      <title>Hello &amp; World</title>
      <link>https://example.com/a</link>
      <pubDate>Wed, 02 Oct 2024 15:00:00 GMT</pubDate>
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
