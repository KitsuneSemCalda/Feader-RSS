// Package feed parses RSS 2.0 and Atom feeds into a common Item shape.
package feed

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	stdhtml "html"
	"io"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	htmlparser "golang.org/x/net/html"

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
	// PublishedAt is a Unix timestamp used for reliable cross-format sorting.
	// Published keeps the source text so the UI can show a useful fallback when
	// a publisher uses a non-standard date format.
	PublishedAt int64    `json:"publishedAt,omitempty"`
	Author      string   `json:"author,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	Summary     string   `json:"summary"`
	Read        bool     `json:"read"`
	Starred     bool     `json:"starred"`
	Tags        []string `json:"tags,omitempty"`
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
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	PubDate     string   `xml:"pubDate"`
	Description string   `xml:"description"`
	Content     string   `xml:"encoded"` // content:encoded, matched by local name
	Author      string   `xml:"author"`
	Creator     string   `xml:"creator"` // dc:creator, matched by local name
	Categories  []string `xml:"category"`
}

// --- Atom ---

type atomDoc struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title      string         `xml:"title"`
	ID         string         `xml:"id"`
	Links      []atomLink     `xml:"link"`
	Published  string         `xml:"published"`
	Updated    string         `xml:"updated"`
	Summary    string         `xml:"summary"`
	Content    string         `xml:"content"`
	Authors    []atomAuthor   `xml:"author"`
	Categories []atomCategory `xml:"category"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

type atomAuthor struct {
	Name  string `xml:"name"`
	Email string `xml:"email"`
}

type atomCategory struct {
	Term string `xml:"term,attr"`
}

var tagRE = regexp.MustCompile(`<[^>]+>`)
var spaceBeforePunctRE = regexp.MustCompile(`\s+([,.;:!?])`)

var summaryBlockTags = map[string]bool{
	"article": true, "blockquote": true, "br": true, "div": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true,
	"h6": true, "li": true, "p": true, "pre": true, "section": true,
	"tr": true,
}

var summarySkipTags = map[string]bool{
	"aside": true, "canvas": true, "footer": true, "form": true,
	"header": true, "nav": true, "script": true, "style": true,
	"svg": true,
	// See the matching comments in internal/article: golang.org/x/net/html
	// parses <noscript> content as one literal text node (raw tag syntax
	// included), so it must be skipped explicitly or it leaks into the
	// summary verbatim; <template> content is real child elements but is
	// inert by spec (never rendered unless cloned by script) and must be
	// skipped the same way.
	"noscript": true,
	"template": true,
}

var get = safefetch.Get
var fetchFeed = Fetch
var sleep = time.Sleep

// clean extracts readable text from an RSS/Atom HTML fragment. Using the HTML
// parser instead of a tag regexp prevents script/style contents from leaking
// into the article summary and preserves paragraph boundaries for the UI.
func clean(value string) string {
	doc, err := htmlparser.Parse(strings.NewReader(value))
	if err != nil {
		return cleanFallback(value)
	}

	var parts []string
	var walk func(*htmlparser.Node)
	walk = func(n *htmlparser.Node) {
		if n.Type == htmlparser.ElementNode {
			tag := strings.ToLower(n.Data)
			if summarySkipTags[tag] {
				return
			}
			if summaryBlockTags[tag] {
				parts = append(parts, "\n")
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
			if summaryBlockTags[tag] {
				parts = append(parts, "\n")
			}
			return
		}
		if n.Type == htmlparser.TextNode {
			text := strings.Join(strings.Fields(n.Data), " ")
			if text != "" {
				parts = append(parts, text+" ")
			}
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return normalizeText(strings.Join(parts, ""))
}

func cleanFallback(value string) string {
	value = stdhtml.UnescapeString(value)
	value = tagRE.ReplaceAllString(value, " ")
	return normalizeText(value)
}

func normalizeText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Split(value, "\n")
	result := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		line = spaceBeforePunctRE.ReplaceAllString(line, "$1")
		if line == "" {
			if len(result) > 0 && !blank {
				result = append(result, "")
			}
			blank = true
			continue
		}
		result = append(result, line)
		blank = false
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.Join(strings.Fields(v), " ")
		}
	}
	return ""
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// PublishedTimestamp parses the date formats commonly emitted by RSS 2.0 and
// Atom. It returns zero when the publisher's date is not parseable.
func PublishedTimestamp(value string) int64 {
	value = strings.TrimSpace(value)
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		time.DateOnly,
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		time.RFC850,
		time.UnixDate,
		time.ANSIC,
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Unix()
		}
	}
	return 0
}

func normalizeCategories(values ...string) []string {
	seen := make(map[string]bool)
	categories := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		categories = append(categories, value)
	}
	return categories
}

func resolveLink(raw string, base *url.URL) string {
	link := firstNonEmpty(raw)
	if link == "" || base == nil {
		return link
	}
	parsed, err := url.Parse(link)
	if err != nil {
		return link
	}
	return canonicalizeURL(base.ResolveReference(parsed)).String()
}

// canonicalizeURL applies only the equivalences the URL spec itself
// guarantees never change what a request fetches: lowercasing the
// case-insensitive scheme and host, dropping a port that matches the
// scheme's own default, and dropping the fragment (never sent to the
// server). The path and query — where a real difference in meaning is
// possible — are left exactly as the publisher wrote them.
func canonicalizeURL(u *url.URL) *url.URL {
	out := *u
	out.Scheme = strings.ToLower(out.Scheme)
	if host, port, err := net.SplitHostPort(out.Host); err == nil {
		host = strings.ToLower(host)
		if isDefaultPort(out.Scheme, port) {
			out.Host = host
		} else {
			out.Host = net.JoinHostPort(host, port)
		}
	} else {
		out.Host = strings.ToLower(out.Host)
	}
	out.Fragment = ""
	out.RawFragment = ""
	return &out
}

func isDefaultPort(scheme, port string) bool {
	return (scheme == "http" && port == "80") || (scheme == "https" && port == "443")
}

func atomAuthorName(authors []atomAuthor) string {
	for _, author := range authors {
		if name := firstNonEmpty(author.Name); name != "" {
			return name
		}
		if email := firstNonEmpty(author.Email); email != "" {
			return email
		}
	}
	return ""
}

func articleID(identity, link string) string {
	sum := sha256.Sum256([]byte(identity + "\x00" + link))
	return hex.EncodeToString(sum[:])[:24]
}

// ArticleID returns the stable identifier for an article whose feed has the
// given canonical URL. Renaming a feed's editable display name never changes
// this value, since the URL — not the name — is the identity component.
func ArticleID(feedURL, link string) string {
	return articleID(feedURL, link)
}

// LegacyArticleID reproduces the article identifier scheme used before
// feeds carried a stable URL-based identity: it was keyed on the feed's
// editable display name instead. Store migrations use it to recognize
// rows that still need to move to the current, rename-safe scheme (see
// Store.MigrateArticleIdentity).
func LegacyArticleID(feedName, link string) string {
	return articleID(feedName, link)
}

// feedIdentity picks the component that goes into an article's id: the
// feed's own URL when known, falling back to its display name only when no
// URL is available (matching the legacy scheme).
func feedIdentity(name string, base *url.URL) string {
	if base != nil {
		return base.String()
	}
	return name
}

// Parse decodes RSS 2.0 or Atom XML, returning normalized items. It
// autodetects the format from the root element. feedURL is the feed's own
// address; it is used both to resolve relative article links and, since it
// rarely changes, as the stable per-feed identity component of each
// article's id — unlike the editable display name in name.
func Parse(name, feedURL string, data []byte) ([]Item, error) {
	base, _ := url.Parse(feedURL)
	return parse(name, data, base)
}

func parse(name string, data []byte, base *url.URL) ([]Item, error) {
	root, err := rootElementName(data)
	if err != nil {
		return nil, err
	}
	switch root {
	case "rss":
		return parseRSS(name, data, base)
	case "feed":
		return parseAtom(name, data, base)
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

func parseRSS(name string, data []byte, base *url.URL) ([]Item, error) {
	var doc rssDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid RSS: %w", err)
	}
	identity := feedIdentity(name, base)
	items := make([]Item, 0, len(doc.Channel.Items))
	for _, it := range doc.Channel.Items {
		title := clean(firstNonEmpty(it.Title))
		link := resolveLink(it.Link, base)
		if title == "" || link == "" {
			continue
		}
		published := firstNonEmpty(it.PubDate)
		items = append(items, Item{
			ID:          articleID(identity, link),
			Title:       title,
			URL:         link,
			Feed:        name,
			Published:   published,
			PublishedAt: PublishedTimestamp(published),
			Author:      clean(firstNonEmpty(it.Author, it.Creator)),
			Categories:  normalizeCategories(it.Categories...),
			Summary:     truncateRunes(clean(firstNonEmpty(it.Content, it.Description)), MaxSummaryLength),
			Read:        false,
		})
	}
	return items, nil
}

func parseAtom(name string, data []byte, base *url.URL) ([]Item, error) {
	var doc atomDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid Atom: %w", err)
	}
	identity := feedIdentity(name, base)
	items := make([]Item, 0, len(doc.Entries))
	for _, entry := range doc.Entries {
		title := clean(firstNonEmpty(entry.Title))
		link := resolveLink(atomLinkHref(entry.Links), base)
		if title == "" || link == "" {
			continue
		}
		published := firstNonEmpty(entry.Published, entry.Updated)
		categories := make([]string, 0, len(entry.Categories))
		for _, category := range entry.Categories {
			categories = append(categories, category.Term)
		}
		items = append(items, Item{
			ID:          articleID(identity, link),
			Title:       title,
			URL:         link,
			Feed:        name,
			Published:   published,
			PublishedAt: PublishedTimestamp(published),
			Author:      clean(atomAuthorName(entry.Authors)),
			Categories:  normalizeCategories(categories...),
			Summary:     truncateRunes(clean(firstNonEmpty(entry.Summary, entry.Content)), MaxSummaryLength),
			Read:        false,
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
func Fetch(name, rawURL string) ([]Item, error) {
	resp, err := get(rawURL, 15*time.Second)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := safefetch.ReadCapped(resp.Body, MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(rawURL)
	return parse(name, data, base)
}

// FetchWithRetry retries transient network and server failures with an
// exponential delay. Permanent failures (invalid URLs, unsafe destinations,
// malformed feeds and oversized responses) are returned immediately.
func FetchWithRetry(name, rawURL string, attempts int, backoff time.Duration) ([]Item, error) {
	if attempts < 1 {
		attempts = 1
	}
	if backoff < 0 {
		backoff = 0
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		items, err := fetchFeed(name, rawURL)
		if err == nil {
			return items, nil
		}
		lastErr = err
		if attempt+1 >= attempts || !safefetch.IsRetryable(err) {
			break
		}
		delay := backoff
		for n := 0; n < attempt; n++ {
			if delay >= 5*time.Second {
				delay = 5 * time.Second
				break
			}
			delay *= 2
		}
		if delay > 5*time.Second {
			delay = 5 * time.Second
		}
		sleep(delay)
	}
	return nil, lastErr
}
