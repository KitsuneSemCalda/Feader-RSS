package opml

import (
	"bytes"
	"errors"
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

func TestImportRejectsMalformedAndWrongRootDocuments(t *testing.T) {
	if _, err := Import(strings.NewReader("not XML")); err == nil {
		t.Fatal("malformed OPML should fail")
	}
	if _, err := Import(strings.NewReader(`<rss><channel /></rss>`)); err == nil {
		t.Fatal("wrong OPML root should fail")
	}
}

func TestImportUsesTextNamesAndFlattensNestedFolders(t *testing.T) {
	input := `<opml><body><outline text="Outer"><outline text="Inner"><outline text="Readable name" xmlUrl=" https://example.test/feed " /></outline></outline></body></opml>`
	feeds, err := Import(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Import nested folders: %v", err)
	}
	if len(feeds) != 1 || feeds[0].Name != "Readable name" || feeds[0].URL != "https://example.test/feed" || feeds[0].Folder != "Outer / Inner" {
		t.Fatalf("nested folders = %+v", feeds)
	}
}

func TestExportUsesURLForUnnamedFeedsAndReportsWriterErrors(t *testing.T) {
	var output bytes.Buffer
	if err := Export(&output, []Feed{
		{Name: "", URL: "https://unnamed.test/feed"},
		{Name: "Skipped", URL: "   "},
		{Name: "Grouped", URL: "https://grouped.test/feed", Folder: "Tech"},
		{Name: "Grouped two", URL: "https://grouped-two.test/feed", Folder: "Tech"},
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if !strings.Contains(output.String(), `text="https://unnamed.test/feed"`) || strings.Count(output.String(), `text="Tech"`) != 1 || !strings.Contains(output.String(), "grouped-two.test") {
		t.Fatalf("export fallback/grouping = %s", output.String())
	}

	headerFailure := &failingWriter{err: errors.New("header write failed"), failAfter: 0}
	if err := Export(headerFailure, nil); err == nil || err.Error() != "header write failed" {
		t.Fatalf("header writer error = %v", err)
	}
	encodeFailure := &failingWriter{err: errors.New("encode write failed"), failAfter: 1}
	if err := Export(encodeFailure, []Feed{{Name: "Feed", URL: "https://example.test/feed"}}); err == nil || !strings.Contains(err.Error(), "encode write failed") {
		t.Fatalf("encode writer error = %v", err)
	}
}

type failingWriter struct {
	writes    int
	failAfter int
	err       error
}

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.writes >= w.failAfter {
		return 0, w.err
	}
	w.writes++
	return len(p), nil
}
