// Package article extracts readable text from an HTML page, equivalent to
// the stdlib-only ArticleParser in the original Python implementation.
package article

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"github.com/KitsuneSemCalda/feader-rss/internal/safefetch"
)

const MaxResponseBytes = 5 * 1024 * 1024

// Article is the readable-text result of fetching a page.
type Article struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

var blockTags = map[string]bool{
	"article": true, "br": true, "div": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "li": true, "p": true, "pre": true, "section": true,
}

var skipTags = map[string]bool{
	"aside": true, "footer": true, "form": true, "header": true,
	"nav": true, "script": true, "style": true, "svg": true,
	// golang.org/x/net/html parses <noscript> the way a scripting-enabled
	// browser does: its entire content is one literal text node (raw tag
	// syntax included, e.g. tracking-pixel <img> or lazy-load <style>
	// fallbacks), never real child elements. Without this, that markup
	// leaks straight into the extracted text instead of being discarded.
	"noscript": true,
	// <template> content is real, ordinary child elements as far as the
	// parser is concerned, but it is inert by spec — never rendered unless
	// cloned by script — so it must be skipped the same way, or its text
	// (often a hidden modal, cookie banner, or lazy-loaded component)
	// appears in the article as if it were visible body content.
	"template": true,
}

var (
	tabsRE       = regexp.MustCompile(`[ \t]+`)
	newlineTabRE = regexp.MustCompile(`\n[ \t]+`)
	punctRE      = regexp.MustCompile(`\s+([,.;:!?])`)
	blankLinesRE = regexp.MustCompile(`\n{3,}`)
)

var get = safefetch.Get
var fetchArticle = Fetch
var sleep = time.Sleep

// Extract walks the parsed HTML tree and produces (title, readable content),
// mirroring the block/skip-tag behavior of the Python ArticleParser.
func Extract(r io.Reader) (title, content string, err error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", "", fmt.Errorf("invalid HTML: %w", err)
	}

	var parts []string
	var titleParts []string
	skipDepth := 0
	inTitle := false

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			tag := strings.ToLower(n.Data)
			if skipTags[tag] {
				skipDepth++
				defer func() { skipDepth-- }()
			}
			wasTitle := tag == "title"
			if wasTitle {
				inTitle = true
			}
			if skipDepth == 0 && blockTags[tag] {
				parts = append(parts, "\n")
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			if skipDepth == 0 && blockTags[tag] {
				parts = append(parts, "\n")
			}
			if wasTitle {
				inTitle = false
			}
			return
		}
		if n.Type == html.TextNode {
			if skipDepth > 0 {
				return
			}
			value := strings.Join(strings.Fields(n.Data), " ")
			if value == "" {
				return
			}
			parts = append(parts, value+" ")
			if inTitle {
				titleParts = append(titleParts, value)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	joined := tabsRE.ReplaceAllString(strings.Join(parts, ""), " ")
	joined = newlineTabRE.ReplaceAllString(joined, "\n")
	joined = punctRE.ReplaceAllString(joined, "$1")
	joined = blankLinesRE.ReplaceAllString(joined, "\n\n")
	joined = strings.TrimSpace(joined)

	title = strings.TrimSpace(strings.Join(titleParts, " "))
	if title == "" {
		for _, line := range strings.Split(joined, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				title = trimmed
				break
			}
		}
		if title == "" {
			title = "Article"
		}
	}
	return title, joined, nil
}

// Fetch downloads url and extracts its readable title/content.
func Fetch(url string) (*Article, error) {
	resp, err := get(url, 20*time.Second)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := safefetch.ReadCapped(resp.Body, MaxResponseBytes)
	if err != nil {
		return nil, err
	}

	reader, err := charset.NewReader(strings.NewReader(string(data)), resp.Header.Get("Content-Type"))
	if err != nil {
		reader = strings.NewReader(string(data))
	}

	title, content, err := Extract(reader)
	if err != nil {
		return nil, err
	}
	return &Article{URL: url, Title: title, Content: content}, nil
}

// FetchWithRetry retries only transient network/server failures. It is used
// by the background prefetcher so a temporary outage does not get retried on
// every refresh without a delay, while malformed or unsafe pages fail fast.
func FetchWithRetry(url string, attempts int, backoff time.Duration) (*Article, error) {
	if attempts < 1 {
		attempts = 1
	}
	if backoff < 0 {
		backoff = 0
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		result, err := fetchArticle(url)
		if err == nil {
			return result, nil
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
