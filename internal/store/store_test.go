package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/KitsuneSemCalda/feader-rss/internal/feed"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenAutoMigratesLegacyState(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "items.db")
	legacyPath := filepath.Join(dir, "items.json")

	legacyJSON := `{"version":1,"items":[
		{"id":"legacy1","title":"Old Read","url":"https://x/1","feed":"F","published":"2020-01-01","summary":"s","read":true},
		{"id":"legacy2","title":"Old Unread","url":"https://x/2","feed":"F","published":"2020-01-02","summary":"s","read":false}
	]}`
	if err := os.WriteFile(legacyPath, []byte(legacyJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	listed, err := s.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected 2 migrated articles, got %d", len(listed))
	}
	byID := map[string]feed.Item{}
	for _, it := range listed {
		byID[it.ID] = it
	}
	if !byID["legacy1"].Read {
		t.Error("expected legacy1 to be imported as read")
	}
	if byID["legacy2"].Read {
		t.Error("expected legacy2 to be imported as unread")
	}

	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Errorf("expected legacy state file to be renamed away, stat err = %v", err)
	}
	if _, err := os.Stat(legacyPath + ".migrated"); err != nil {
		t.Errorf("expected %s.migrated to exist: %v", legacyPath, err)
	}
}

func TestOpenSkipsMigrationWhenDatabaseAlreadyHasData(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "items.db")
	legacyPath := filepath.Join(dir, "items.json")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.Upsert([]feed.Item{
		{ID: "fresh", Feed: "F", Title: "Fresh", URL: "https://x/fresh", Published: "2024-01-01"},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	s.Close()

	// A legacy file only appears after real data already exists (e.g. a
	// leftover from a previous, incomplete migration attempt): it must not
	// be imported over live data.
	legacyJSON := `{"items":[{"id":"legacy","title":"Legacy","url":"https://x/legacy","feed":"F","published":"2020-01-01","read":true}]}`
	if err := os.WriteFile(legacyPath, []byte(legacyJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open (reopen): %v", err)
	}
	defer s2.Close()

	listed, err := s2.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != "fresh" {
		t.Fatalf("expected only the pre-existing article, got %+v", listed)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Errorf("expected legacy file to be left untouched: %v", err)
	}
}

func TestUpsertAndList(t *testing.T) {
	s := openTestStore(t)

	items := []feed.Item{
		{ID: "a", Feed: "Feed", Title: "First", URL: "https://x/1", Published: "2024-01-02"},
		{ID: "b", Feed: "Feed", Title: "Second", URL: "https://x/2", Published: "2024-01-01"},
	}
	fresh, err := s.Upsert(items)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(fresh) != 2 {
		t.Fatalf("expected 2 fresh items, got %d", len(fresh))
	}

	listed, err := s.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected 2 items, got %d", len(listed))
	}
	if listed[0].ID != "a" {
		t.Errorf("expected newest-first ordering, got %+v", listed)
	}
}

func TestUpsertPreservesReadFlag(t *testing.T) {
	s := openTestStore(t)
	item := feed.Item{ID: "a", Feed: "Feed", Title: "T", URL: "https://x/1", Published: "2024-01-01"}

	if _, err := s.Upsert([]feed.Item{item}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.MarkRead("a", true); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	// Re-fetch the same item with updated metadata; read flag must survive.
	item.Title = "Updated Title"
	fresh, err := s.Upsert([]feed.Item{item})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(fresh) != 0 {
		t.Errorf("expected no new items on re-upsert, got %d", len(fresh))
	}

	listed, err := s.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || !listed[0].Read {
		t.Fatalf("expected read flag preserved, got %+v", listed)
	}
	if listed[0].Title != "Updated Title" {
		t.Errorf("expected title to be updated, got %q", listed[0].Title)
	}
}

func TestMarkAllRead(t *testing.T) {
	s := openTestStore(t)
	items := []feed.Item{
		{ID: "a", Feed: "F", Title: "A", URL: "https://x/a", Published: "2024-01-01"},
		{ID: "b", Feed: "F", Title: "B", URL: "https://x/b", Published: "2024-01-02"},
	}
	if _, err := s.Upsert(items); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.MarkAllRead(true); err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	listed, err := s.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, it := range listed {
		if !it.Read {
			t.Errorf("expected all items read, got %+v", it)
		}
	}
	if err := s.MarkAllRead(false); err != nil {
		t.Fatalf("MarkAllRead(false): %v", err)
	}
	listed, _ = s.List(0)
	for _, it := range listed {
		if it.Read {
			t.Errorf("expected all items unread, got %+v", it)
		}
	}
}

func TestListLimit(t *testing.T) {
	s := openTestStore(t)
	items := []feed.Item{
		{ID: "a", Feed: "F", Title: "A", URL: "https://x/a", Published: "2024-01-03"},
		{ID: "b", Feed: "F", Title: "B", URL: "https://x/b", Published: "2024-01-02"},
		{ID: "c", Feed: "F", Title: "C", URL: "https://x/c", Published: "2024-01-01"},
	}
	if _, err := s.Upsert(items); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	listed, err := s.List(2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected 2 items, got %d", len(listed))
	}
}

func TestSetContent(t *testing.T) {
	s := openTestStore(t)
	item := feed.Item{ID: "a", Feed: "F", Title: "A", URL: "https://x/a", Published: "2024-01-01"}
	if _, err := s.Upsert([]feed.Item{item}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.SetContent("a", "cached body"); err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	content, ok, err := s.ContentForURL("https://x/a")
	if err != nil {
		t.Fatalf("ContentForURL: %v", err)
	}
	if !ok || content != "cached body" {
		t.Fatalf("ContentForURL = (%q, %v), want (\"cached body\", true)", content, ok)
	}
}

func TestContentForURLMissing(t *testing.T) {
	s := openTestStore(t)
	item := feed.Item{ID: "a", Feed: "F", Title: "A", URL: "https://x/a", Published: "2024-01-01"}
	if _, err := s.Upsert([]feed.Item{item}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	_, ok, err := s.ContentForURL("https://x/a")
	if err != nil {
		t.Fatalf("ContentForURL: %v", err)
	}
	if ok {
		t.Fatal("expected no cached content before SetContent")
	}
	_, ok, err = s.ContentForURL("https://x/unknown")
	if err != nil {
		t.Fatalf("ContentForURL: %v", err)
	}
	if ok {
		t.Fatal("expected no cached content for unknown URL")
	}
}

func TestIDForURL(t *testing.T) {
	s := openTestStore(t)
	item := feed.Item{ID: "a", Feed: "F", Title: "A", URL: "https://x/a", Published: "2024-01-01"}
	if _, err := s.Upsert([]feed.Item{item}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	id, err := s.IDForURL("https://x/a")
	if err != nil {
		t.Fatalf("IDForURL: %v", err)
	}
	if id != "a" {
		t.Errorf("IDForURL = %q, want %q", id, "a")
	}
	id, err = s.IDForURL("https://x/unknown")
	if err != nil {
		t.Fatalf("IDForURL: %v", err)
	}
	if id != "" {
		t.Errorf("IDForURL for unknown url = %q, want empty", id)
	}
}

func TestListReportsCachedFlag(t *testing.T) {
	s := openTestStore(t)
	items := []feed.Item{
		{ID: "a", Feed: "F", Title: "A", URL: "https://x/a", Published: "2024-01-02"},
		{ID: "b", Feed: "F", Title: "B", URL: "https://x/b", Published: "2024-01-01"},
	}
	if _, err := s.Upsert(items); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.SetContent("a", "full text"); err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	listed, err := s.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byID := map[string]feed.Item{}
	for _, it := range listed {
		byID[it.ID] = it
	}
	if !byID["a"].Cached {
		t.Error("expected item 'a' to be reported as cached")
	}
	if byID["b"].Cached {
		t.Error("expected item 'b' to be reported as not cached")
	}
}

func TestPendingPrefetch(t *testing.T) {
	s := openTestStore(t)
	items := []feed.Item{
		{ID: "a", Feed: "F", Title: "A", URL: "https://x/a", Published: "2024-01-03"},
		{ID: "b", Feed: "F", Title: "B", URL: "https://x/b", Published: "2024-01-02"},
		{ID: "c", Feed: "F", Title: "C", URL: "https://x/c", Published: "2024-01-01"},
	}
	if _, err := s.Upsert(items); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// b already has cached content: should be excluded.
	if err := s.SetContent("b", "already cached"); err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	// c is read: should be excluded, since it's unlikely to be opened next.
	if err := s.MarkRead("c", true); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	pending, err := s.PendingPrefetch(0)
	if err != nil {
		t.Fatalf("PendingPrefetch: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "a" {
		t.Fatalf("PendingPrefetch = %+v, want only item 'a'", pending)
	}
}

func TestPendingPrefetchLimit(t *testing.T) {
	s := openTestStore(t)
	items := []feed.Item{
		{ID: "a", Feed: "F", Title: "A", URL: "https://x/a", Published: "2024-01-03"},
		{ID: "b", Feed: "F", Title: "B", URL: "https://x/b", Published: "2024-01-02"},
	}
	if _, err := s.Upsert(items); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	pending, err := s.PendingPrefetch(1)
	if err != nil {
		t.Fatalf("PendingPrefetch: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 item, got %d", len(pending))
	}
}

func TestImportPreservesReadAndSkipsExisting(t *testing.T) {
	s := openTestStore(t)

	// Simulate an article already fetched normally (unread, no legacy data).
	if _, err := s.Upsert([]feed.Item{
		{ID: "existing", Feed: "F", Title: "Existing", URL: "https://x/existing", Published: "2024-01-01"},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	legacyItems := []feed.Item{
		{ID: "existing", Feed: "F", Title: "Existing (legacy)", URL: "https://x/existing", Published: "2024-01-01", Read: true},
		{ID: "legacy-only", Feed: "F", Title: "Legacy Only", URL: "https://x/legacy", Published: "2023-12-31", Read: true},
		{ID: "", Feed: "F", Title: "Missing ID", URL: "https://x/no-id", Published: "2023-12-30"},
	}
	imported, err := s.Import(legacyItems)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if imported != 1 {
		t.Fatalf("imported = %d, want 1 (only the brand-new legacy article)", imported)
	}

	listed, err := s.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byID := map[string]feed.Item{}
	for _, it := range listed {
		byID[it.ID] = it
	}
	if len(byID) != 2 {
		t.Fatalf("expected 2 stored articles, got %d", len(byID))
	}
	if byID["existing"].Read {
		t.Error("Import must not resurrect read=true onto an article already fetched normally")
	}
	if byID["existing"].Title != "Existing" {
		t.Errorf("Import must not overwrite an existing article's title, got %q", byID["existing"].Title)
	}
	if !byID["legacy-only"].Read {
		t.Error("expected legacy-only article to be imported as read")
	}
}
