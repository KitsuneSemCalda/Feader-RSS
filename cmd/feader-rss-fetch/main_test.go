package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	readerArticle "github.com/KitsuneSemCalda/feader-rss/internal/article"
	"github.com/KitsuneSemCalda/feader-rss/internal/feed"
	"github.com/KitsuneSemCalda/feader-rss/internal/opml"
	"github.com/KitsuneSemCalda/feader-rss/internal/store"
)

func captureCLI(t *testing.T, fn func() int) (stdout, stderr string, code int) {
	t.Helper()
	oldStdout, oldStderr := os.Stdout, os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		stdoutReader.Close()
		stdoutWriter.Close()
		t.Fatalf("create stderr pipe: %v", err)
	}
	os.Stdout, os.Stderr = stdoutWriter, stderrWriter

	code = fn()
	stdoutWriter.Close()
	stderrWriter.Close()
	os.Stdout, os.Stderr = oldStdout, oldStderr

	stdoutBytes, readErr := io.ReadAll(stdoutReader)
	if readErr != nil {
		t.Fatalf("read stdout: %v", readErr)
	}
	stderrBytes, readErr := io.ReadAll(stderrReader)
	if readErr != nil {
		t.Fatalf("read stderr: %v", readErr)
	}
	stdoutReader.Close()
	stderrReader.Close()
	return string(stdoutBytes), string(stderrBytes), code
}

func seedCLIDB(t *testing.T) (string, []feed.Item) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state", "items.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open seed database: %v", err)
	}
	items := []feed.Item{
		{ID: "one", Feed: "Tech", Title: "Go testing", URL: "https://example.test/one", Published: "2024-01-03", Summary: "unit coverage"},
		{ID: "two", Feed: "News", Title: "RSS reader", URL: "https://example.test/two", Published: "2024-01-02", Summary: "daily headlines"},
		{ID: "three", Feed: "Tech", Title: "SQLite notes", URL: "https://example.test/three", Published: "2024-01-01", Summary: "local database"},
	}
	if _, err := db.Upsert(items); err != nil {
		db.Close()
		t.Fatalf("seed Upsert: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed database: %v", err)
	}
	return path, items
}

func writeCLIFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func decodeCLIJSON(t *testing.T, output string, target interface{}) {
	t.Helper()
	if err := json.Unmarshal([]byte(output), target); err != nil {
		t.Fatalf("decode CLI JSON %q: %v", output, err)
	}
}

func TestRunReportsUsageUnknownCommandsAndStringLists(t *testing.T) {
	_, stderr, code := captureCLI(t, func() int { return run(nil) })
	if code != 2 || !strings.Contains(stderr, "usage:") {
		t.Fatalf("empty invocation: code=%d stderr=%q", code, stderr)
	}
	_, stderr, code = captureCLI(t, func() int { return run([]string{"wat"}) })
	if code != 2 || !strings.Contains(stderr, "unknown subcommand: wat") {
		t.Fatalf("unknown invocation: code=%d stderr=%q", code, stderr)
	}

	var values stringList
	if err := values.Set("first"); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if err := values.Set("second"); err != nil {
		t.Fatalf("second Set: %v", err)
	}
	if got := values.String(); got != "first,second" {
		t.Errorf("stringList.String() = %q, want first,second", got)
	}
}

func TestCLIListSearchAndMutationCommands(t *testing.T) {
	dbPath, _ := seedCLIDB(t)

	stdout, stderr, code := captureCLI(t, func() int {
		return run([]string{"list", "--db", dbPath, "--limit", "1", "--feed", "Tech"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("list: code=%d stderr=%q", code, stderr)
	}
	var listed snapshot
	decodeCLIJSON(t, stdout, &listed)
	if len(listed.Items) != 1 || listed.Items[0].Feed != "Tech" {
		t.Fatalf("scoped list = %+v", listed.Items)
	}
	if listed.UnreadCount != 2 || listed.UnreadFeedCount != 1 {
		t.Fatalf("scoped unread counts = (%d, %d), want (2, 1)", listed.UnreadCount, listed.UnreadFeedCount)
	}
	if len(listed.Errors) != 0 || listed.Errors == nil || listed.NewItems == nil {
		t.Fatalf("list should normalize nil arrays: %+v", listed)
	}

	stdout, stderr, code = captureCLI(t, func() int {
		return run([]string{"search", "--db", dbPath, "--query", "unit coverage", "--feed", "Tech"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("search: code=%d stderr=%q", code, stderr)
	}
	var searchResult snapshot
	decodeCLIJSON(t, stdout, &searchResult)
	if len(searchResult.Items) != 1 || searchResult.Items[0].ID != "one" {
		t.Fatalf("search result = %+v", searchResult.Items)
	}

	commands := [][]string{
		{"mark-read", "--db", dbPath, "one"},
		{"mark-read", "--db", dbPath, "--unread", "two"},
		{"mark-all", "--db", dbPath, "--feed", "Tech"},
		{"mark-all", "--db", dbPath, "--unread", "--feed", "Tech"},
		{"star", "--db", dbPath, "--value=true", "one"},
		{"star", "--db", dbPath, "--value=false", "one"},
		{"set-tags", "--db", dbPath, "--tags", "Go, go, ,rss", "one"},
	}
	for _, args := range commands {
		stdout, stderr, code = captureCLI(t, func() int { return run(args) })
		if code != 0 || stdout != "" || stderr != "" {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen mutated database: %v", err)
	}
	items, err := db.List(0)
	if err != nil {
		db.Close()
		t.Fatalf("list mutated database: %v", err)
	}
	db.Close()
	byID := make(map[string]feed.Item, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	if byID["one"].Read || byID["three"].Read || byID["two"].Read {
		t.Errorf("read states after scoped mark-all = one:%v two:%v three:%v", byID["one"].Read, byID["two"].Read, byID["three"].Read)
	}
	if byID["one"].Starred {
		t.Error("--value=false should clear starred state")
	}
	if got, want := byID["one"].Tags, []string{"Go", "rss"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("tags = %#v, want %#v", got, want)
	}
}

func TestCLIMissingArgumentsReturnUsageErrors(t *testing.T) {
	dbPath, _ := seedCLIDB(t)
	cases := [][]string{
		{"fetch"},
		{"fetch", "--db", dbPath, "only-a-name"},
		{"list"},
		{"search", "--db", dbPath},
		{"article"},
		{"prefetch"},
		{"mark-read", "--db", dbPath},
		{"mark-all"},
		{"star", "--db", dbPath},
		{"set-tags", "--db", dbPath},
		{"opml-export"},
		{"opml-import", "--config", filepath.Join(t.TempDir(), "config.json")},
		{"migrate", "--db", dbPath},
		{"backup", "--db", dbPath},
		{"restore", "--db", dbPath},
	}
	for _, args := range cases {
		_, stderr, code := captureCLI(t, func() int { return run(args) })
		if code != 2 || stderr == "" {
			t.Errorf("%v: code=%d stderr=%q", args, code, stderr)
		}
	}
}

func TestCLIFetchUsesFeedBoundaryAndReportsErrors(t *testing.T) {
	oldFetcher := fetchFeedWithRetry
	t.Cleanup(func() { fetchFeedWithRetry = oldFetcher })
	var calls []string
	fetchFeedWithRetry = func(name, rawURL string, attempts int, backoff time.Duration) ([]feed.Item, error) {
		calls = append(calls, name+"|"+rawURL)
		if name == "Broken" {
			return nil, errors.New("temporary feed outage")
		}
		return []feed.Item{{
			ID: "fetched", Feed: name, Title: "Fetched", URL: rawURL, Summary: "from fake feed",
		}}, nil
	}
	dbPath := filepath.Join(t.TempDir(), "fetch.db")
	stdout, stderr, code := captureCLI(t, func() int {
		return run([]string{
			"fetch", "--db", dbPath, "--limit", "5", "--retention", "4",
			"--attempts", "2", "--retry-backoff", "0s",
			"Good", "https://good.test/feed", "Broken", "https://bad.test/feed",
		})
	})
	if code != 0 || !strings.Contains(stderr, "Broken: temporary feed outage") {
		t.Fatalf("fetch: code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	if len(calls) != 2 || calls[0] != "Good|https://good.test/feed" || calls[1] != "Broken|https://bad.test/feed" {
		t.Fatalf("fetch calls = %#v", calls)
	}
	var result snapshot
	decodeCLIJSON(t, stdout, &result)
	if len(result.Items) != 1 || len(result.NewItems) != 1 || len(result.Errors) != 1 {
		t.Fatalf("fetch snapshot = %+v", result)
	}
	if result.UnreadCount != 1 || result.UnreadFeedCount != 1 {
		t.Fatalf("fetch unread counts = (%d, %d)", result.UnreadCount, result.UnreadFeedCount)
	}
}

func TestCLIArticleServesCacheFetchesAndReportsFailures(t *testing.T) {
	dbPath, items := seedCLIDB(t)
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open article database: %v", err)
	}
	if err := db.SetContent(items[0].ID, "cached article body"); err != nil {
		db.Close()
		t.Fatalf("cache article: %v", err)
	}
	db.Close()

	stdout, stderr, code := captureCLI(t, func() int {
		return run([]string{"article", "--db", dbPath, items[0].URL})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("cached article: code=%d stderr=%q", code, stderr)
	}
	var cached readerArticle.Article
	decodeCLIJSON(t, stdout, &cached)
	if cached.URL != items[0].URL || cached.Content != "cached article body" {
		t.Fatalf("cached article = %+v", cached)
	}

	oldFetcher := fetchArticle
	t.Cleanup(func() { fetchArticle = oldFetcher })
	fetchArticle = func(url string) (*readerArticle.Article, error) {
		return &readerArticle.Article{URL: url, Title: "Fetched title", Content: "fresh article body"}, nil
	}
	stdout, stderr, code = captureCLI(t, func() int {
		return run([]string{"article", "--db", dbPath, items[1].URL})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("fetched article: code=%d stderr=%q", code, stderr)
	}
	var fetched readerArticle.Article
	decodeCLIJSON(t, stdout, &fetched)
	if fetched.Title != "Fetched title" || fetched.Content != "fresh article body" {
		t.Fatalf("fetched article = %+v", fetched)
	}
	db, err = store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen article database: %v", err)
	}
	content, ok, err := db.ContentForURL(items[1].URL)
	db.Close()
	if err != nil || !ok || content != "fresh article body" {
		t.Fatalf("cached fetched article = (%q, %v, %v)", content, ok, err)
	}

	fetchArticle = func(url string) (*readerArticle.Article, error) {
		return nil, errors.New("article service unavailable")
	}
	stdout, stderr, code = captureCLI(t, func() int {
		return run([]string{"article", "https://failure.test/article"})
	})
	if code != 1 || stderr != "" {
		t.Fatalf("failed article: code=%d stderr=%q", code, stderr)
	}
	var failure map[string]string
	decodeCLIJSON(t, stdout, &failure)
	if failure["url"] != "https://failure.test/article" || failure["error"] != "article service unavailable" {
		t.Fatalf("failure response = %#v", failure)
	}
}

func TestCLIPrefetchMigrateBackupAndRestore(t *testing.T) {
	dbPath, items := seedCLIDB(t)
	oldFetcher := fetchArticleWithRetry
	t.Cleanup(func() { fetchArticleWithRetry = oldFetcher })
	fetchArticleWithRetry = func(url string, attempts int, backoff time.Duration) (*readerArticle.Article, error) {
		if strings.HasSuffix(url, "/two") || strings.HasSuffix(url, "/three") {
			return nil, errors.New("prefetch unavailable")
		}
		return &readerArticle.Article{URL: url, Title: "Prefetched", Content: "prefetched body"}, nil
	}
	stdout, stderr, code := captureCLI(t, func() int {
		return run([]string{"prefetch", "--db", dbPath, "--limit", "0", "--concurrency", "0"})
	})
	if code != 0 || !strings.Contains(stderr, "prefetch unavailable") {
		t.Fatalf("prefetch: code=%d stderr=%q", code, stderr)
	}
	var prefetchResult map[string]int
	decodeCLIJSON(t, stdout, &prefetchResult)
	if prefetchResult["candidates"] != 3 || prefetchResult["prefetched"] != 1 || prefetchResult["failed"] != 2 {
		t.Fatalf("prefetch result = %#v", prefetchResult)
	}

	legacyPath := writeCLIFile(t, "items.json", `{"items":[{"id":"legacy","feed":"Legacy","title":"Migrated","url":"https://legacy.test/item","published":"2024-01-01","read":true}]}`)
	migratedDB := filepath.Join(t.TempDir(), "migrated", "items.db")
	stdout, stderr, code = captureCLI(t, func() int {
		return run([]string{"migrate", "--db", migratedDB, "--json", legacyPath})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("migrate: code=%d stderr=%q", code, stderr)
	}
	var migrationResult map[string]int
	decodeCLIJSON(t, stdout, &migrationResult)
	if migrationResult["imported"] != 1 || migrationResult["candidates"] != 1 {
		t.Fatalf("migration result = %#v", migrationResult)
	}

	backupPath := filepath.Join(t.TempDir(), "backup", "items.db")
	stdout, stderr, code = captureCLI(t, func() int {
		return run([]string{"backup", "--db", dbPath, "--output", backupPath})
	})
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("backup: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	targetDB := filepath.Join(t.TempDir(), "target", "items.db")
	target, err := store.Open(targetDB)
	if err != nil {
		t.Fatalf("open restore target: %v", err)
	}
	if _, err := target.Upsert([]feed.Item{{ID: "target", Feed: "Target", Title: "To replace", URL: "https://target.test/item"}}); err != nil {
		t.Fatalf("seed restore target: %v", err)
	}
	target.Close()
	stdout, stderr, code = captureCLI(t, func() int {
		return run([]string{"restore", "--db", targetDB, "--input", backupPath})
	})
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("restore: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	restored, err := store.Open(targetDB)
	if err != nil {
		t.Fatalf("open restored database: %v", err)
	}
	itemsAfterRestore, err := restored.List(0)
	restored.Close()
	if err != nil {
		t.Fatalf("list restored database: %v", err)
	}
	if len(itemsAfterRestore) != len(items) {
		t.Fatalf("restored items = %+v, want %d source items", itemsAfterRestore, len(items))
	}

	_, stderr, code = captureCLI(t, func() int {
		return run([]string{"backup", "--db", filepath.Join(t.TempDir(), "missing.db"), "--output", backupPath})
	})
	if code != 1 || stderr == "" {
		t.Fatalf("invalid backup source: code=%d stderr=%q", code, stderr)
	}
	invalidJSON := writeCLIFile(t, "invalid-items.json", "not json")
	_, stderr, code = captureCLI(t, func() int {
		return run([]string{"migrate", "--db", filepath.Join(t.TempDir(), "bad.db"), "--json", invalidJSON})
	})
	if code != 1 || !strings.Contains(stderr, "invalid legacy JSON") {
		t.Fatalf("invalid migration: code=%d stderr=%q", code, stderr)
	}
}

func TestCLIOPMLExportImportAndConfigHelpers(t *testing.T) {
	configPath := writeCLIFile(t, "config.json", `{"maxItems":50,"feeds":[{"name":"One","url":"https://one.test/feed","folder":"Tech"},{"name":"Two","url":"https://two.test/feed"}]}`)
	opmlPath := filepath.Join(t.TempDir(), "subscriptions.opml")
	stdout, stderr, code := captureCLI(t, func() int {
		return run([]string{"opml-export", "--config", configPath, "--output", opmlPath})
	})
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("OPML export file: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	opmlData, err := os.ReadFile(opmlPath)
	if err != nil {
		t.Fatalf("read OPML export: %v", err)
	}
	if !bytes.Contains(opmlData, []byte("https://one.test/feed")) {
		t.Fatalf("OPML export missing first feed: %s", opmlData)
	}
	stdout, stderr, code = captureCLI(t, func() int {
		return run([]string{"opml-export", "--config", configPath})
	})
	if code != 0 || stderr != "" || !strings.Contains(stdout, "<opml") {
		t.Fatalf("OPML export stdout: code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}

	importConfig := filepath.Join(t.TempDir(), "new-config.json")
	stdout, stderr, code = captureCLI(t, func() int {
		return run([]string{"opml-import", "--config", importConfig, "--input", opmlPath})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("OPML import replace: code=%d stderr=%q", code, stderr)
	}
	var importResult map[string]interface{}
	decodeCLIJSON(t, stdout, &importResult)
	if importResult["imported"] != float64(2) || importResult["total"] != float64(2) || importResult["merged"] != false {
		t.Fatalf("OPML replace result = %#v", importResult)
	}

	mergeConfig := writeCLIFile(t, "merge-config.json", `{"feeds":[{"name":"Existing","url":"https://one.test/feed/"}]}`)
	mergeOPML := writeCLIFile(t, "merge.opml", `<?xml version="1.0"?><opml version="2.0"><body><outline text="Existing duplicate" xmlUrl="https://one.test/feed"/><outline text="New" xmlUrl="https://new.test/feed"/></body></opml>`)
	stdout, stderr, code = captureCLI(t, func() int {
		return run([]string{"opml-import", "--config", mergeConfig, "--input", mergeOPML, "--merge"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("OPML import merge: code=%d stderr=%q", code, stderr)
	}
	decodeCLIJSON(t, stdout, &importResult)
	if importResult["imported"] != float64(2) || importResult["total"] != float64(2) || importResult["merged"] != true {
		t.Fatalf("OPML merge result = %#v", importResult)
	}

	invalidConfig := writeCLIFile(t, "invalid-config.json", "not json")
	_, stderr, code = captureCLI(t, func() int {
		return run([]string{"opml-export", "--config", invalidConfig})
	})
	if code != 1 || !strings.Contains(stderr, "invalid JSON configuration") {
		t.Fatalf("invalid OPML export config: code=%d stderr=%q", code, stderr)
	}
	invalidInput := writeCLIFile(t, "invalid.opml", "not xml")
	_, stderr, code = captureCLI(t, func() int {
		return run([]string{"opml-import", "--config", importConfig, "--input", invalidInput})
	})
	if code != 1 || stderr == "" {
		t.Fatalf("invalid OPML import: code=%d stderr=%q", code, stderr)
	}

	merged := mergeOPMLFeeds(
		[]opml.Feed{{Name: "Existing", URL: "https://one.test/feed/"}},
		[]opml.Feed{
			{Name: "Duplicate", URL: "https://one.test/feed"},
			{Name: "New", URL: " https://new.test/feed/ "},
			{Name: "Empty", URL: "   "},
		},
	)
	if len(merged) != 2 || merged[1].Name != "New" || merged[1].URL != " https://new.test/feed/ " {
		t.Fatalf("mergeOPMLFeeds = %#v", merged)
	}
}

func TestCLIUtilityErrorAndNilBranches(t *testing.T) {
	if got := nonNilItems(nil); got == nil || len(got) != 0 {
		t.Errorf("nonNilItems(nil) = %#v", got)
	}
	if got := nonNilErrors(nil); got == nil || len(got) != 0 {
		t.Errorf("nonNilErrors(nil) = %#v", got)
	}
	items := []feed.Item{{ID: "one"}}
	if got := nonNilItems(items); &got[0] != &items[0] {
		t.Error("nonNilItems should preserve a non-nil slice")
	}

	stdout, stderr, code := captureCLI(t, func() int { return printJSON(func() {}) })
	if code != 1 || stdout != "" || stderr == "" {
		t.Fatalf("unsupported JSON value: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	blockingPath := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blockingPath, []byte("blocking"), 0o600); err != nil {
		t.Fatalf("write blocking path: %v", err)
	}
	if err := writeJSONConfig(filepath.Join(blockingPath, "config.json"), map[string]json.RawMessage{}); err == nil {
		t.Fatal("writeJSONConfig should fail when its parent is a file")
	}

	dbPath, _ := seedCLIDB(t)
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open utility database: %v", err)
	}
	db.Close()
	if _, err := makeSnapshot(db, nil, nil, nil); err == nil {
		t.Fatal("makeSnapshot should report a closed database")
	}
}
