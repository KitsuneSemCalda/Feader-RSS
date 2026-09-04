// Package opml imports and exports the feed portion of an OPML subscription
// list, preserving one folder level (nested folders are flattened by joining
// their names with " / ").
package opml

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// MaxFeeds is the maximum number of feeds Feader supports configuring at
// once, matching the limit the Quickshell settings UI enforces when a feed
// is added by hand.
const MaxFeeds = 8

// Feed is the portable subset of a Feader feed configuration.
type Feed struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Folder string `json:"folder,omitempty"`
}

// Truncate caps feeds at MaxFeeds, reporting whether any were dropped. It
// keeps the first MaxFeeds entries so earlier feeds (e.g. those already
// configured, when merging) are preserved over later ones.
func Truncate(feeds []Feed) ([]Feed, bool) {
	if len(feeds) <= MaxFeeds {
		return feeds, false
	}
	return feeds[:MaxFeeds], true
}

func isValidFeedURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

type document struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr,omitempty"`
	Head    head     `xml:"head"`
	Body    body     `xml:"body"`
}

type head struct {
	Title string `xml:"title"`
}

type body struct {
	Outlines []outline `xml:"outline"`
}

type outline struct {
	Text     string    `xml:"text,attr"`
	Title    string    `xml:"title,attr,omitempty"`
	Type     string    `xml:"type,attr,omitempty"`
	XMLURL   string    `xml:"xmlUrl,attr,omitempty"`
	Outlines []outline `xml:"outline"`
}

// Import reads OPML from r and returns its RSS/Atom outlines. Folder outlines
// are retained in the returned Folder field; duplicate feed URLs are skipped
// while preserving the first occurrence.
func Import(r io.Reader) ([]Feed, error) {
	var doc document
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid OPML: %w", err)
	}
	if !strings.EqualFold(doc.XMLName.Local, "opml") {
		return nil, fmt.Errorf("invalid OPML: root element is <%s>", doc.XMLName.Local)
	}

	feeds := make([]Feed, 0)
	seen := make(map[string]bool)
	var walk func([]outline, string)
	walk = func(outlines []outline, folder string) {
		for _, item := range outlines {
			feedURL := strings.TrimSpace(item.XMLURL)
			if feedURL != "" {
				if !isValidFeedURL(feedURL) {
					continue // not an http(s) URL; skip rather than configure a feed that can never be fetched
				}
				key := strings.ToLower(strings.TrimRight(feedURL, "/"))
				if !seen[key] {
					seen[key] = true
					name := strings.TrimSpace(item.Title)
					if name == "" {
						name = strings.TrimSpace(item.Text)
					}
					feeds = append(feeds, Feed{Name: name, URL: feedURL, Folder: folder})
				}
				continue
			}
			nextFolder := folder
			label := strings.TrimSpace(item.Title)
			if label == "" {
				label = strings.TrimSpace(item.Text)
			}
			if label != "" {
				if nextFolder == "" {
					nextFolder = label
				} else {
					nextFolder += " / " + label
				}
			}
			walk(item.Outlines, nextFolder)
		}
	}
	walk(doc.Body.Outlines, "")
	return feeds, nil
}

// Export writes a deterministic OPML document containing the supplied feeds.
// Feeds with the same Folder are grouped together; ungrouped feeds remain at
// the top level.
func Export(w io.Writer, feeds []Feed) error {
	doc := document{Version: "2.0", Head: head{Title: "Feader RSS subscriptions"}}
	groups := make(map[string]int)
	for _, feed := range feeds {
		url := strings.TrimSpace(feed.URL)
		if url == "" {
			continue
		}
		item := outline{
			Text:   strings.TrimSpace(feed.Name),
			Title:  strings.TrimSpace(feed.Name),
			Type:   "rss",
			XMLURL: url,
		}
		if item.Text == "" {
			item.Text = url
			item.Title = url
		}
		folder := strings.TrimSpace(feed.Folder)
		if folder == "" {
			doc.Body.Outlines = append(doc.Body.Outlines, item)
			continue
		}
		index, ok := groups[folder]
		if !ok {
			groups[folder] = len(doc.Body.Outlines)
			doc.Body.Outlines = append(doc.Body.Outlines, outline{Text: folder, Title: folder, Outlines: []outline{item}})
			continue
		}
		doc.Body.Outlines[index].Outlines = append(doc.Body.Outlines[index].Outlines, item)
	}

	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	encoder := xml.NewEncoder(w)
	encoder.Indent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("writing OPML: %w", err)
	}
	return encoder.Flush()
}
