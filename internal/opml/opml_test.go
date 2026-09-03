package opml

import (
	"bytes"
	"strings"
	"testing"
)

func TestImportPreservesFoldersAndSkipsDuplicateURLs(t *testing.T) {
	input := `<opml version="2.0"><head><title>Subscriptions</title></head><body>
<outline text="Linux" title="Linux">
  <outline text="LWN" title="LWN" type="rss" xmlUrl="https://lwn.net/rss/" />
  <outline text="LWN duplicate" type="rss" xmlUrl="https://lwn.net/rss/" />
  <outline text="Kernel" type="rss" xmlUrl="https://kernel.org/feed.xml" />
</outline>
<outline text="Personal" title="Personal"><outline text="Blog" xmlUrl="https://example.org/feed" /></outline>
</body></opml>`

	feeds, err := Import(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(feeds) != 3 {
		t.Fatalf("feeds = %+v, want 3 unique feeds", feeds)
	}
	if feeds[0].Name != "LWN" || feeds[0].Folder != "Linux" {
		t.Fatalf("first feed = %+v, want LWN in Linux", feeds[0])
	}
	if feeds[2].Folder != "Personal" {
		t.Fatalf("third feed folder = %q, want Personal", feeds[2].Folder)
	}
}

func TestExportGroupsFolders(t *testing.T) {
	var output bytes.Buffer
	err := Export(&output, []Feed{
		{Name: "One", URL: "https://one.test/feed", Folder: "Tech"},
		{Name: "Two", URL: "https://two.test/feed", Folder: "Tech"},
		{Name: "Three", URL: "https://three.test/feed"},
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	text := output.String()
	if !strings.HasPrefix(text, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Fatalf("missing XML declaration: %q", text[:min(len(text), 80)])
	}
	if strings.Count(text, `text="Tech"`) != 1 || strings.Count(text, `xmlUrl="https://one.test/feed"`) != 1 {
		t.Fatalf("folder grouping missing from output: %s", text)
	}
	if strings.Contains(text, `text="Tech" title="Tech" type=""`) {
		t.Fatal("empty feed attributes should not be emitted on folder outlines")
	}
	if strings.Index(text, `text="Three"`) < strings.Index(text, `text="Tech"`) {
		t.Fatal("ungrouped feed moved before its original position")
	}
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
