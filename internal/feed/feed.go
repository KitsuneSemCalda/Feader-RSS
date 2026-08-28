// Package feed parses RSS 2.0 and Atom feeds into a common Item shape.
package feed

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/KitsuneSemCalda/feader-rss/internal/safefetch"
)

const MaxResponseBytes = 5 * 1024 * 1024
const MaxSummaryLength = 500

// Item is a single article extracted from a feed, in the shape expected by
// the Quickshell UI.
type Item struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Feed      string `json:"feed"`
	Published string `json:"published"`
	Summary   string `json:"summary"`
	Read      bool   `json:"read"`
	// Cached reports whether the article's full text has already been
	// prefetched, so the UI can tell readers which articles will open
	// instantly. It is only populated by store.List, never by feed parsing.
	Cached bool `json:"cached"`
}

// --- RSS 2.0 ---

type rssDoc struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
	Content     string `xml:"encoded"` // content:encoded, matched by local name
}

// --- Atom ---

type atomDoc struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title     string     `xml:"title"`
	Links     []atomLink `xml:"link"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
	Summary   string     `xml:"summary"`
	Content   string     `xml:"content"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

var tagRE = regexp.MustCompile(`<[^>]+>`)
var spaceBeforePunctRE = regexp.MustCompile(`\s+([,.;:!?])`)

// clean strips HTML tags and unescapes entities, collapsing whitespace like
// the original Python implementation.
func clean(value string) string {
	value = html.UnescapeString(value)
	value = tagRE.ReplaceAllString(value, " ")
	value = strings.Join(strings.Fields(value), " ")
	return spaceBeforePunctRE.ReplaceAllString(value, "$1")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.Join(strings.Fields(v), " ")
		}
	}
	return ""
}

func articleID(feedName, link string) string {
	sum := sha256.Sum256([]byte(feedName + "\x00" + link))
	return hex.EncodeToString(sum[:])[:24]
}

// Parse decodes RSS 2.0 or Atom XML, returning normalized items. It
// autodetects the format from the root element.
func Parse(name string, data []byte) ([]Item, error) {
	root, err := rootElementName(data)
	if err != nil {
		return nil, err
	}
	switch root {
	case "rss":
		return parseRSS(name, data)
	case "feed":
		return parseAtom(name, data)
	default:
		return nil, fmt.Errorf("unsupported feed format: root element <%s>", root)
	}
}

func rootElementName(data []byte) (string, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			return "", fmt.Errorf("empty or invalid XML document")
		}
		if err != nil {
			return "", fmt.Errorf("invalid XML: %w", err)
		}
		if start, ok := tok.(xml.StartElement); ok {
			return start.Name.Local, nil
		}
	}
}

func parseRSS(name string, data []byte) ([]Item, error) {
	var doc rssDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid RSS: %w", err)
	}
	items := make([]Item, 0, len(doc.Channel.Items))
	for _, it := range doc.Channel.Items {
		title := firstNonEmpty(it.Title)
		link := firstNonEmpty(it.Link)
		if title == "" || link == "" {
			continue
		}
		summary := clean(firstNonEmpty(it.Description, it.Content))
		if len(summary) > MaxSummaryLength {
			summary = summary[:MaxSummaryLength]
		}
		items = append(items, Item{
			ID:        articleID(name, link),
			Title:     title,
			URL:       link,
			Feed:      name,
			Published: firstNonEmpty(it.PubDate),
			Summary:   summary,
			Read:      false,
		})
	}
	return items, nil
}

func parseAtom(name string, data []byte) ([]Item, error) {
	var doc atomDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid Atom: %w", err)
	}
	items := make([]Item, 0, len(doc.Entries))
	for _, entry := range doc.Entries {
		title := firstNonEmpty(entry.Title)
		link := atomLinkHref(entry.Links)
		if title == "" || link == "" {
			continue
		}
		summary := clean(firstNonEmpty(entry.Summary, entry.Content))
		if len(summary) > MaxSummaryLength {
			summary = summary[:MaxSummaryLength]
		}
		items = append(items, Item{
			ID:        articleID(name, link),
			Title:     title,
			URL:       link,
			Feed:      name,
			Published: firstNonEmpty(entry.Published, entry.Updated),
			Summary:   summary,
			Read:      false,
		})
	}
	return items, nil
}

// atomLinkHref picks the best <link> for an entry: prefer rel="alternate" or
// no rel attribute (the Atom default), falling back to the first link.
func atomLinkHref(links []atomLink) string {
	for _, l := range links {
		if l.Rel == "" || l.Rel == "alternate" {
			return l.Href
		}
	}
	if len(links) > 0 {
		return links[0].Href
	}
	return ""
}

// Fetch downloads and parses the feed at url, labelling items with name.
func Fetch(name, url string) ([]Item, error) {
	resp, err := safefetch.Get(url, 15*time.Second)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := safefetch.ReadCapped(resp.Body, MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	return Parse(name, data)
}
