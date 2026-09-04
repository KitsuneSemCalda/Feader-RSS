package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestSQLiteBackupAndRestoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "items.db")
	backupPath := filepath.Join(dir, "backup", "items.db")

	source, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open source: %v", err)
	}
	if _, err := source.Upsert([]feed.Item{{
		ID: "saved", Feed: "Feed", Title: "Saved", URL: "https://x/saved",
		Published: "2024-01-01", Summary: "summary", Author: "Alice",
		Categories: []string{"News"},
	}}); err != nil {
		source.Close()
		t.Fatalf("Upsert source: %v", err)
	}
	if err := source.SetContent("saved", "cached article"); err != nil {
		source.Close()
		t.Fatalf("SetContent source: %v", err)
	}
	if err := source.MarkRead("saved", true); err != nil {
		source.Close()
		t.Fatalf("MarkRead source: %v", err)
	}
	if err := Backup(dbPath, backupPath); err != nil {
		source.Close()
		t.Fatalf("Backup: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("Close source: %v", err)
	}

	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(backupPath + suffix); !os.IsNotExist(err) {
			t.Errorf("backup unexpectedly has %s sidecar, stat err = %v", suffix, err)
		}
	}
	if info, err := os.Stat(backupPath); err != nil {
		t.Errorf("Stat backup: %v", err)
	} else if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("backup file permissions = %o, want 0600", perm)
	}
	if info, err := os.Stat(filepath.Dir(backupPath)); err != nil {
		t.Errorf("Stat backup dir: %v", err)
	} else if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("backup dir permissions = %o, want 0700", perm)
	}

	target, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open target: %v", err)
	}
	if _, err := target.Upsert([]feed.Item{
		{ID: "saved", Feed: "Feed", Title: "Changed", URL: "https://x/saved", Published: "2024-01-02"},
		{ID: "extra", Feed: "Feed", Title: "Extra", URL: "https://x/extra", Published: "2024-01-03"},
	}); err != nil {
		target.Close()
		t.Fatalf("Upsert target: %v", err)
	}
	if err := target.MarkRead("saved", false); err != nil {
		target.Close()
		t.Fatalf("MarkRead target: %v", err)
	}
	if err := target.SetContent("saved", "changed article"); err != nil {
		target.Close()
		t.Fatalf("SetContent target: %v", err)
	}
	if err := target.Close(); err != nil {
		t.Fatalf("Close target: %v", err)
	}

	if err := Restore(dbPath, backupPath); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	restored, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open restored: %v", err)
	}
	defer restored.Close()
	items, err := restored.List(0)
	if err != nil {
		t.Fatalf("List restored: %v", err)
	}
	if len(items) != 1 || items[0].ID != "saved" || items[0].Title != "Saved" || !items[0].Read {
		t.Fatalf("restored items = %+v, want the original saved item only", items)
	}
	content, ok, err := restored.ContentForURL("https://x/saved")
	if err != nil {
		t.Fatalf("ContentForURL restored: %v", err)
	}
	if !ok || content != "cached article" {
		t.Fatalf("restored content = (%q, %v), want cached article", content, ok)
	}
	if info, err := os.Stat(dbPath); err != nil {
		t.Errorf("Stat restored db: %v", err)
	} else if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("restored db permissions = %o, want 0600", perm)
	}
}

func TestOpenRestrictsStateDirAndDatabasePermissions(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("Chmod dir: %v", err)
	}
	dbPath := filepath.Join(dir, "items.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if info, err := os.Stat(dir); err != nil {
		t.Fatalf("Stat dir: %v", err)
	} else if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("state dir permissions = %o, want 0700", perm)
	}
	if info, err := os.Stat(dbPath); err != nil {
		t.Fatalf("Stat db: %v", err)
	} else if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("database file permissions = %o, want 0600", perm)
	}
}

func TestRestrictDatabasePermissionsChmodsSidecarsAndIgnoresMissingFiles(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "items.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.WriteFile(base+suffix, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", suffix, err)
		}
	}
	if err := restrictDatabasePermissions(base); err != nil {
		t.Fatalf("restrictDatabasePermissions: %v", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(base + suffix)
		if err != nil {
			t.Fatalf("Stat %s: %v", suffix, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s permissions = %o, want 0600", suffix, perm)
		}
	}
	if err := restrictDatabasePermissions(filepath.Join(dir, "missing.db")); err != nil {
		t.Errorf("restrictDatabasePermissions on missing file: %v", err)
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

func TestListOrdersRSSAndAtomDatesChronologically(t *testing.T) {
	s := openTestStore(t)
	items := []feed.Item{
		{ID: "rss", Feed: "RSS", Title: "Older", URL: "https://x/rss", Published: "Wed, 02 Oct 2024 15:00:00 GMT"},
		{ID: "atom", Feed: "Atom", Title: "Newer", URL: "https://x/atom", Published: "2024-10-03T15:00:00Z"},
	}
	if _, err := s.Upsert(items); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	listed, err := s.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 2 || listed[0].ID != "atom" {
		t.Fatalf("expected Atom item first, got %+v", listed)
	}
}

func TestRichMetadataRoundTrip(t *testing.T) {
	s := openTestStore(t)
	item := feed.Item{
		ID: "rich", Feed: "Feed", Title: "Rich", URL: "https://x/rich",
		Published: "2024-10-02T15:00:00Z", Author: "Alice",
		Categories: []string{"News", "Tech"}, Summary: "summary",
	}
	if _, err := s.Upsert([]feed.Item{item}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	listed, err := s.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected one item, got %d", len(listed))
	}
	got := listed[0]
	if got.Author != item.Author || got.PublishedAt == 0 || len(got.Categories) != 2 {
		t.Fatalf("rich metadata did not round-trip: %+v", got)
	}
}

func TestOpenMigratesExistingSQLiteSchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "items.db")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	_, err = raw.Exec(`CREATE TABLE articles (
		id TEXT PRIMARY KEY,
		feed TEXT NOT NULL,
		title TEXT NOT NULL,
		url TEXT NOT NULL,
		published TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		content TEXT,
		read INTEGER NOT NULL DEFAULT 0,
		first_seen TEXT NOT NULL
	)`)
	if err != nil {
		raw.Close()
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open migrated db: %v", err)
	}
	defer s.Close()
	if _, err := s.Upsert([]feed.Item{{
		ID: "migrated", Feed: "F", Title: "Migrated", URL: "https://x/migrated",
		Published: "2024-10-02T15:00:00Z", Author: "Alice", Categories: []string{"Tech"},
	}}); err != nil {
		t.Fatalf("Upsert after migration: %v", err)
	}
	listed, err := s.List(0)
	if err != nil || len(listed) != 1 || listed[0].Author != "Alice" {
		t.Fatalf("migrated schema unusable: items=%+v err=%v", listed, err)
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

func TestMigrateArticleIdentityRenamesLegacyRowsAndPreservesState(t *testing.T) {
	s := openTestStore(t)
	feedURL := "https://example.com/feed.xml"
	legacyID := feed.LegacyArticleID("Old Name", "https://example.com/a")
	newID := feed.ArticleID(feedURL, "https://example.com/a")
	if legacyID == newID {
		t.Fatal("test setup: legacy and new ids must differ")
	}

	if _, err := s.Upsert([]feed.Item{{
		ID: legacyID, Feed: "Old Name", Title: "T", URL: "https://example.com/a", Published: "2024-01-01",
	}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.MarkRead(legacyID, true); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if err := s.SetStarred(legacyID, true); err != nil {
		t.Fatalf("SetStarred: %v", err)
	}
	if err := s.SetTags(legacyID, []string{"favorite"}); err != nil {
		t.Fatalf("SetTags: %v", err)
	}

	migrated, err := s.MigrateArticleIdentity(map[string]string{"Old Name": feedURL})
	if err != nil {
		t.Fatalf("MigrateArticleIdentity: %v", err)
	}
	if migrated != 1 {
		t.Fatalf("migrated = %d, want 1", migrated)
	}

	listed, err := s.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 article, got %d", len(listed))
	}
	item := listed[0]
	if item.ID != newID {
		t.Errorf("id = %q, want %q", item.ID, newID)
	}
	if !item.Read || !item.Starred || len(item.Tags) != 1 || item.Tags[0] != "favorite" {
		t.Errorf("migration lost article state: %+v", item)
	}

	// Re-running the migration is a no-op: the row is already on the new scheme.
	migrated, err = s.MigrateArticleIdentity(map[string]string{"Old Name": feedURL})
	if err != nil {
		t.Fatalf("MigrateArticleIdentity (second run): %v", err)
	}
	if migrated != 0 {
		t.Errorf("expected no further migration, got %d", migrated)
	}
}

func TestMigrateArticleIdentityIgnoresUnconfiguredFeedsAndCollisions(t *testing.T) {
	s := openTestStore(t)
	legacyID := feed.LegacyArticleID("Removed Feed", "https://example.com/a")
	if _, err := s.Upsert([]feed.Item{{
		ID: legacyID, Feed: "Removed Feed", Title: "T", URL: "https://example.com/a", Published: "2024-01-01",
	}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// No URL known for "Removed Feed": nothing to migrate to, row stays put.
	if migrated, err := s.MigrateArticleIdentity(map[string]string{"Other Feed": "https://example.com/other.xml"}); err != nil {
		t.Fatalf("MigrateArticleIdentity: %v", err)
	} else if migrated != 0 {
		t.Errorf("migrated = %d, want 0 for an unconfigured feed", migrated)
	}
	if migrated, err := s.MigrateArticleIdentity(nil); err != nil || migrated != 0 {
		t.Errorf("MigrateArticleIdentity(nil) = (%d, %v), want (0, nil)", migrated, err)
	}

	listed, err := s.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != legacyID {
		t.Fatalf("expected legacy id untouched, got %+v", listed)
	}

	// A row already sitting at the target id blocks the rename instead of
	// clobbering it.
	feedURL := "https://example.com/feed.xml"
	collidingID := feed.ArticleID(feedURL, "https://example.com/a")
	if _, err := s.Upsert([]feed.Item{{
		ID: collidingID, Feed: "Removed Feed", Title: "Existing", URL: "https://example.com/other", Published: "2024-01-02",
	}}); err != nil {
		t.Fatalf("Upsert colliding row: %v", err)
	}
	if migrated, err := s.MigrateArticleIdentity(map[string]string{"Removed Feed": feedURL}); err != nil {
		t.Fatalf("MigrateArticleIdentity: %v", err)
	} else if migrated != 0 {
		t.Errorf("migrated = %d, want 0 when the target id is already taken", migrated)
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

func TestPrefetchFailuresAreBackedOffUntilTheNextWindow(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Upsert([]feed.Item{{ID: "a", Feed: "F", Title: "A", URL: "https://x/a"}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.RecordPrefetchFailure("a", "temporary outage"); err != nil {
		t.Fatalf("RecordPrefetchFailure: %v", err)
	}
	pending, err := s.PendingPrefetch(0)
	if err != nil {
		t.Fatalf("PendingPrefetch after failure: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after failure = %+v, want none during backoff", pending)
	}
	if err := s.SetContent("a", "recovered article"); err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	pending, err = s.PendingPrefetch(0)
	if err != nil {
		t.Fatalf("PendingPrefetch after success: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("cached item should not be pending: %+v", pending)
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

func TestSearchIncludesCachedContentAndTags(t *testing.T) {
	s := openTestStore(t)
	items := []feed.Item{
		{ID: "content", Feed: "F", Title: "A normal title", URL: "https://x/content", Published: "2024-01-02"},
		{ID: "tagged", Feed: "F", Title: "Another title", URL: "https://x/tagged", Published: "2024-01-01"},
	}
	if _, err := s.Upsert(items); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.SetContent("content", "the hidden phrase lives in the cached article"); err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	if err := s.SetTags("tagged", []string{"bookmark", "reading"}); err != nil {
		t.Fatalf("SetTags: %v", err)
	}
	if err := s.SetStarred("tagged", true); err != nil {
		t.Fatalf("SetStarred: %v", err)
	}

	results, err := s.Search("hidden phrase", 0)
	if err != nil {
		t.Fatalf("Search cached content: %v", err)
	}
	if len(results) != 1 || results[0].ID != "content" {
		t.Fatalf("cached-content search = %+v, want content", results)
	}
	results, err = s.Search("bookmark", 0)
	if err != nil {
		t.Fatalf("Search tag: %v", err)
	}
	if len(results) != 1 || results[0].ID != "tagged" || !results[0].Starred || len(results[0].Tags) != 2 {
		t.Fatalf("tag search = %+v, want starred tagged article", results)
	}
}

func TestUnreadCountsCanBeScopedToConfiguredFeeds(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Upsert([]feed.Item{
		{ID: "one", Feed: "One", Title: "One", URL: "https://x/one"},
		{ID: "two", Feed: "Two", Title: "Two", URL: "https://x/two"},
		{ID: "two-read", Feed: "Two", Title: "Read", URL: "https://x/two-read"},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.MarkRead("two-read", true); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	unread, feeds, err := s.UnreadCounts("Two")
	if err != nil {
		t.Fatalf("UnreadCounts scoped: %v", err)
	}
	if unread != 1 || feeds != 1 {
		t.Fatalf("scoped unread = (%d, %d), want (1, 1)", unread, feeds)
	}
	unread, feeds, err = s.UnreadCounts()
	if err != nil {
		t.Fatalf("UnreadCounts global: %v", err)
	}
	if unread != 2 || feeds != 2 {
		t.Fatalf("global unread = (%d, %d), want (2, 2)", unread, feeds)
	}
}

// TestConcurrentMultiConnectionAccessDoesNotFailOrCorrupt mirrors how the
// real plugin uses the database: every CLI invocation (fetch, mark-read,
// star, set-tags, list, search) opens its own short-lived connection against
// the same on-disk file, and several of these can legitimately overlap (a
// background refresh while the user marks an article read or stars it). WAL
// mode plus a generous busy_timeout should absorb that contention rather
// than surfacing "database is locked" errors or losing an update.
func TestConcurrentMultiConnectionAccessDoesNotFailOrCorrupt(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "items.db")

	seed, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open seed: %v", err)
	}
	const n = 30
	items := make([]feed.Item, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, feed.Item{
			ID:        fmt.Sprintf("concurrent-%02d", i),
			Feed:      "Feed",
			Title:     fmt.Sprintf("Concurrent Title %d", i),
			URL:       fmt.Sprintf("https://example.test/concurrent/%d", i),
			Published: "2024-01-01",
		})
	}
	if _, err := seed.Upsert(items); err != nil {
		seed.Close()
		t.Fatalf("seed Upsert: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, n*3)
	withStore := func(op func(*Store) error) {
		defer wg.Done()
		s, err := Open(dbPath)
		if err != nil {
			errCh <- fmt.Errorf("open: %w", err)
			return
		}
		defer s.Close()
		if err := op(s); err != nil {
			errCh <- err
		}
	}

	for i := 0; i < n; i++ {
		id := items[i].ID

		wg.Add(3)
		go withStore(func(s *Store) error { return s.MarkRead(id, true) })
		go withStore(func(s *Store) error { return s.SetStarred(id, true) })
		go withStore(func(s *Store) error {
			_, err := s.List(0)
			return err
		})
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent access error: %v", err)
	}

	final, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open final: %v", err)
	}
	defer final.Close()
	listed, err := final.List(0)
	if err != nil {
		t.Fatalf("List final: %v", err)
	}
	if len(listed) != n {
		t.Fatalf("expected %d articles to survive concurrent access, got %d", n, len(listed))
	}
	for _, item := range listed {
		if !item.Read || !item.Starred {
			t.Errorf("article %s = read:%v starred:%v, want both true", item.ID, item.Read, item.Starred)
		}
	}
	results, err := final.Search("Concurrent", 0)
	if err != nil {
		t.Fatalf("Search final: %v", err)
	}
	if len(results) != n {
		t.Errorf("FTS index inconsistent after concurrent writes: got %d results, want %d", len(results), n)
	}
}

func TestUpsertDeduplicatesSameFeedAndURL(t *testing.T) {
	s := openTestStore(t)
	fresh, err := s.Upsert([]feed.Item{
		{ID: "first", Feed: "F", Title: "First", URL: "https://x/same"},
		{ID: "second", Feed: "F", Title: "Duplicate", URL: "https://x/same"},
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(fresh) != 1 || fresh[0].ID != "first" {
		t.Fatalf("fresh = %+v, want only first item", fresh)
	}
	items, err := s.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].ID != "first" {
		t.Fatalf("deduplicated list = %+v", items)
	}
}

func TestPrunePrefersReadAndUnstarredItems(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Upsert([]feed.Item{
		{ID: "old-read", Feed: "F", Title: "Old read", URL: "https://x/old-read", Published: "2024-01-01"},
		{ID: "old-starred", Feed: "F", Title: "Old starred", URL: "https://x/old-starred", Published: "2024-01-02"},
		{ID: "new-unread", Feed: "F", Title: "New unread", URL: "https://x/new-unread", Published: "2024-01-03"},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.MarkRead("old-read", true); err != nil {
		t.Fatalf("MarkRead old-read: %v", err)
	}
	if err := s.MarkRead("old-starred", true); err != nil {
		t.Fatalf("MarkRead old-starred: %v", err)
	}
	if err := s.SetStarred("old-starred", true); err != nil {
		t.Fatalf("SetStarred: %v", err)
	}
	removed, err := s.Prune(2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	items, err := s.List(0)
	if err != nil {
		t.Fatalf("List after prune: %v", err)
	}
	byID := map[string]feed.Item{}
	for _, item := range items {
		byID[item.ID] = item
	}
	if _, ok := byID["old-read"]; ok {
		t.Fatal("old read article should have been pruned first")
	}
	if _, ok := byID["old-starred"]; !ok {
		t.Fatal("starred article should survive pruning")
	}
	if _, ok := byID["new-unread"]; !ok {
		t.Fatal("unread article should survive pruning")
	}
}

func TestOpenHandlesInvalidPathsEmptyLegacyStateAndBackfillsDates(t *testing.T) {
	blockingPath := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blockingPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocking path: %v", err)
	}
	if _, err := Open(filepath.Join(blockingPath, "state.db")); err == nil {
		t.Fatal("Open should fail when the state directory is a regular file")
	}
	databaseDirectory := filepath.Join(t.TempDir(), "database-directory")
	if err := os.MkdirAll(databaseDirectory, 0o755); err != nil {
		t.Fatalf("create database directory: %v", err)
	}
	if _, err := Open(databaseDirectory); err == nil {
		t.Fatal("Open should fail when the database path is a directory")
	}

	emptyLegacyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(emptyLegacyDir, "items.json"), []byte(`{"items":[]}`), 0o600); err != nil {
		t.Fatalf("write empty legacy state: %v", err)
	}
	emptyLegacyDB, err := Open(filepath.Join(emptyLegacyDir, "items.db"))
	if err != nil {
		t.Fatalf("Open empty legacy state: %v", err)
	}
	emptyLegacyDB.Close()
	if _, err := os.Stat(filepath.Join(emptyLegacyDir, "items.json")); err != nil {
		t.Errorf("empty legacy state should remain available: %v", err)
	}

	corruptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(corruptDir, "items.json"), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write corrupt legacy state: %v", err)
	}
	corruptDB, err := Open(filepath.Join(corruptDir, "items.db"))
	if err != nil {
		t.Fatalf("corrupt legacy state should not prevent Open: %v", err)
	}
	corruptDB.Close()

	dateDir := t.TempDir()
	datePath := filepath.Join(dateDir, "items.db")
	dateDB, err := Open(datePath)
	if err != nil {
		t.Fatalf("Open date database: %v", err)
	}
	_, err = dateDB.db.Exec(`
		INSERT INTO articles (id, feed, title, url, published, summary, first_seen)
		VALUES ('dated', 'F', 'Dated', 'https://example.test/dated', 'Wed, 02 Oct 2024 15:00:00 GMT', '', '2024-10-02T15:00:00Z'),
		       ('undated', 'F', 'Undated', 'https://example.test/undated', 'not a date', '', '2024-10-02T15:00:00Z')
	`)
	if err != nil {
		dateDB.Close()
		t.Fatalf("insert date rows: %v", err)
	}
	dateDB.Close()
	dateDB, err = Open(datePath)
	if err != nil {
		t.Fatalf("reopen date database: %v", err)
	}
	items, err := dateDB.List(0)
	dateDB.Close()
	if err != nil {
		t.Fatalf("list backfilled dates: %v", err)
	}
	byID := map[string]feed.Item{}
	for _, item := range items {
		byID[item.ID] = item
	}
	if byID["dated"].PublishedAt == 0 || byID["undated"].PublishedAt != 0 {
		t.Errorf("backfilled dates = dated:%d undated:%d", byID["dated"].PublishedAt, byID["undated"].PublishedAt)
	}
}

func TestSearchForFeedsAndStoreFilters(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Upsert([]feed.Item{
		{ID: "tech", Feed: "Tech", Title: "Go language", URL: "https://x/tech", Summary: "compiler"},
		{ID: "news", Feed: "News", Title: "Go news", URL: "https://x/news", Summary: "headlines"},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	results, err := s.SearchForFeeds("Go", 0, "", "Tech", "Tech")
	if err != nil {
		t.Fatalf("SearchForFeeds: %v", err)
	}
	if len(results) != 1 || results[0].ID != "tech" {
		t.Fatalf("scoped search = %+v", results)
	}
	results, err = s.Search("   ", 0)
	if err != nil {
		t.Fatalf("empty Search: %v", err)
	}
	if results == nil || len(results) != 0 {
		t.Fatalf("empty Search = %+v", results)
	}
	items, err := s.List(0, "", "Missing")
	if err != nil {
		t.Fatalf("empty feed filter: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("empty feed filter = %+v", items)
	}
}

func TestStoreRetentionPrefetchAndMutationEdges(t *testing.T) {
	s := openTestStore(t)
	if removed, err := s.Prune(0); err != nil || removed != 0 {
		t.Fatalf("Prune disabled = (%d, %v)", removed, err)
	}
	if _, err := s.Upsert([]feed.Item{{ID: "only", Feed: "F", Title: "Only", URL: "https://x/only"}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if removed, err := s.Prune(2); err != nil || removed != 0 {
		t.Fatalf("Prune under limit = (%d, %v)", removed, err)
	}
	if known, err := s.KnownIDs(nil); err != nil || len(known) != 0 {
		t.Fatalf("KnownIDs empty = (%v, %v)", known, err)
	}
	if err := s.SetTags("only", nil); err != nil {
		t.Fatalf("SetTags empty: %v", err)
	}
	if err := s.SetContent("missing", "ignored"); err != nil {
		t.Fatalf("SetContent unknown: %v", err)
	}
	if err := s.SetStarred("missing", true); err != nil {
		t.Fatalf("SetStarred unknown: %v", err)
	}
	if err := s.RecordPrefetchFailure("missing", "ignored"); err != nil {
		t.Fatalf("RecordPrefetchFailure unknown: %v", err)
	}
	if err := s.MarkAllRead(true, "F"); err != nil {
		t.Fatalf("scoped MarkAllRead: %v", err)
	}

	if _, err := s.Upsert([]feed.Item{{ID: "retry", Feed: "F", Title: "Retry", URL: "https://x/retry"}}); err != nil {
		t.Fatalf("Upsert retry: %v", err)
	}
	for i := 0; i < 18; i++ {
		if err := s.RecordPrefetchFailure("retry", strings.Repeat("x", i)); err != nil {
			t.Fatalf("RecordPrefetchFailure %d: %v", i, err)
		}
	}
	var attempts int
	var next string
	if err := s.db.QueryRow(`SELECT prefetch_attempts, prefetch_next_at FROM articles WHERE id = 'retry'`).Scan(&attempts, &next); err != nil {
		t.Fatalf("read retry state: %v", err)
	}
	if attempts != 16 || next == "" {
		t.Errorf("retry state = attempts:%d next:%q", attempts, next)
	}
	if err := s.SetContent("retry", "recovered"); err != nil {
		t.Fatalf("clear retry state: %v", err)
	}
	if err := s.db.QueryRow(`SELECT prefetch_attempts, prefetch_next_at FROM articles WHERE id = 'retry'`).Scan(&attempts, &next); err != nil {
		t.Fatalf("read cleared retry state: %v", err)
	}
	if attempts != 0 || next != "" {
		t.Errorf("cleared retry state = attempts:%d next:%q", attempts, next)
	}
}

func TestStoreSerializationHelpersAndDeduplicationEdges(t *testing.T) {
	if got := encodeCategories(nil); got != "" {
		t.Errorf("encodeCategories(nil) = %q", got)
	}
	if got := encodeTags([]string{" Go ", "go", "", "RSS"}); got != `["Go","RSS"]` {
		t.Errorf("encodeTags normalized = %q", got)
	}
	if got := encodeTags([]string{"", "   "}); got != "" {
		t.Errorf("encodeTags all empty = %q", got)
	}
	if got := decodeCategories(""); got == nil || len(got) != 0 {
		t.Errorf("decodeCategories empty = %#v", got)
	}
	if got := decodeCategories("not json"); got == nil || len(got) != 0 {
		t.Errorf("decodeCategories malformed = %#v", got)
	}
	if got := decodeTags(""); got == nil || len(got) != 0 {
		t.Errorf("decodeTags empty = %#v", got)
	}
	if got := decodeTags("not json"); got == nil || len(got) != 0 {
		t.Errorf("decodeTags malformed = %#v", got)
	}
	where, args := feedFilter([]string{" F ", "F", ""})
	if where != "feed IN (?)" || len(args) != 1 || args[0] != "F" {
		t.Errorf("feedFilter dedup = (%q, %#v)", where, args)
	}
	where, args = feedFilter([]string{"", "  "})
	if where != "1 = 0" || len(args) != 0 {
		t.Errorf("feedFilter empty = (%q, %#v)", where, args)
	}
	items := deduplicateItems([]feed.Item{
		{ID: "missing-url", Feed: "F", Title: "Missing URL"},
		{ID: "missing-title", Feed: "F", URL: "https://x/title"},
		{ID: "valid", Feed: " F ", Title: "Valid", URL: " https://x/item "},
		{ID: "duplicate", Feed: "F", Title: "Duplicate", URL: " https://x/item "},
	})
	if len(items) != 1 || items[0].ID != "valid" {
		t.Fatalf("deduplicate edge cases = %+v", items)
	}
}

func TestBackupValidationHelpers(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := validateDatabaseSource(""); err == nil {
		t.Error("empty source should fail")
	}
	if err := validateDatabaseSource(filepath.Join(dir, "missing.db")); err == nil {
		t.Error("missing source should fail")
	}
	if err := validateDatabaseSource(dir); err == nil {
		t.Error("directory source should fail")
	}
	if err := validateDatabaseSource(source); err != nil {
		t.Fatalf("regular source: %v", err)
	}
	if err := validateDistinctDatabasePaths(source, ""); err == nil {
		t.Error("empty destination should fail")
	}
	if err := validateDistinctDatabasePaths(source, source); err == nil {
		t.Error("same destination should fail")
	}
	if err := validateDistinctDatabasePaths(source, source+"-wal"); err == nil {
		t.Error("WAL sidecar destination should fail")
	}
	link := filepath.Join(dir, "source-link.db")
	if err := os.Link(source, link); err != nil {
		t.Fatalf("create source hard link: %v", err)
	}
	if err := validateDistinctDatabasePaths(source, link); err == nil {
		t.Error("same-file hard link destination should fail")
	}
	if err := validateDistinctDatabasePaths(source, filepath.Join(dir, "new", "destination.db")); err != nil {
		t.Fatalf("distinct destination: %v", err)
	}

	if err := prepareDatabaseDestination(filepath.Join(dir, "nested", "snapshot.db")); err != nil {
		t.Fatalf("prepare destination: %v", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := filepath.Join(dir, "nested", "snapshot.db") + suffix
		if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
			t.Fatalf("write old destination %s: %v", suffix, err)
		}
	}
	if err := prepareDatabaseDestination(filepath.Join(dir, "nested", "snapshot.db")); err != nil {
		t.Fatalf("prepare existing destination: %v", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(filepath.Join(dir, "nested", "snapshot.db") + suffix); !os.IsNotExist(err) {
			t.Errorf("destination sidecar %s still exists: %v", suffix, err)
		}
	}
}

func TestRebuildFTSReportsExecutorErrors(t *testing.T) {
	first := &failingExecer{failAt: 1}
	if err := rebuildFTS(first); err == nil || first.calls != 1 {
		t.Fatalf("first FTS error = %v after %d calls", err, first.calls)
	}
	second := &failingExecer{failAt: 2}
	if err := rebuildFTS(second); err == nil || second.calls != 2 {
		t.Fatalf("second FTS error = %v after %d calls", err, second.calls)
	}
}

type failingExecer struct {
	failAt int
	calls  int
}

func (f *failingExecer) Exec(string, ...interface{}) (sql.Result, error) {
	f.calls++
	if f.calls == f.failAt {
		return nil, errors.New("executor failed")
	}
	return nil, nil
}

func TestBackupAndRestoreRejectInvalidSourcesAndDestinations(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	if err := os.WriteFile(source, []byte("not sqlite"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := Backup(filepath.Join(dir, "missing.db"), filepath.Join(dir, "backup.db")); err == nil {
		t.Error("Backup should reject missing source")
	}
	if err := Backup(source, source); err == nil {
		t.Error("Backup should reject same path")
	}
	if err := Backup(source, filepath.Join(dir, "invalid-backup.db")); err == nil {
		t.Error("Backup should reject an invalid SQLite source during the online copy")
	}
	if err := Restore(filepath.Join(dir, "target.db"), filepath.Join(dir, "missing.db")); err == nil {
		t.Error("Restore should reject missing source")
	}
	if err := Restore(source, source); err == nil {
		t.Error("Restore should reject same path")
	}
	if err := Restore(filepath.Join(dir, "restore-target.db"), source); err == nil {
		t.Error("Restore should reject an invalid SQLite source during the online copy")
	}

	maintenance, err := openMaintenanceDatabase(filepath.Join(dir, "maintenance.db"))
	if err != nil {
		t.Fatalf("openMaintenanceDatabase: %v", err)
	}
	if err := maintenance.Close(); err != nil {
		t.Fatalf("close maintenance database: %v", err)
	}
}
