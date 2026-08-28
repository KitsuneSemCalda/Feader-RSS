// Package store persists articles and their read state in a local SQLite
// database, replacing the JSON state file the Quickshell UI used to own.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/KitsuneSemCalda/feader-rss/internal/feed"
)

// legacyStateFilename is the JSON state file older (pre-SQLite) versions of
// this plugin kept next to the database directory. Open imports it
// automatically the first time it finds an empty database, so upgrading
// never requires a separate manual migration step.
const legacyStateFilename = "items.json"

const schema = `
CREATE TABLE IF NOT EXISTS articles (
	id         TEXT PRIMARY KEY,
	feed       TEXT NOT NULL,
	title      TEXT NOT NULL,
	url        TEXT NOT NULL,
	published  TEXT NOT NULL DEFAULT '',
	summary    TEXT NOT NULL DEFAULT '',
	content    TEXT,
	read       INTEGER NOT NULL DEFAULT 0,
	first_seen TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_articles_published ON articles(published DESC);
`

// Store wraps a SQLite-backed article database.
type Store struct {
	db *sql.DB
}

// Open creates (if needed) and opens the database at path.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating state dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // modernc.org/sqlite is not safe for concurrent writers on one *DB
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}
	s := &Store{db: db}
	if err := s.autoMigrateLegacyState(path); err != nil {
		// Never fail startup over a migration hiccup: the app should still
		// work with an empty history rather than refuse to open.
		fmt.Fprintf(os.Stderr, "warning: could not migrate legacy state: %s\n", err)
	}
	return s, nil
}

// autoMigrateLegacyState imports the old JSON state file (if present) into
// an otherwise-empty database, preserving each article's read flag. It is a
// no-op once the database already holds data, so it only ever runs once per
// installation, right after an upgrade from a pre-SQLite version.
func (s *Store) autoMigrateLegacyState(dbPath string) error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM articles`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	legacyPath := filepath.Join(filepath.Dir(dbPath), legacyStateFilename)
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading legacy state %s: %w", legacyPath, err)
	}

	var legacy struct {
		Items []feed.Item `json:"items"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("legacy state %s is corrupt, skipping migration: %w", legacyPath, err)
	}
	if len(legacy.Items) == 0 {
		return nil
	}
	if _, err := s.Import(legacy.Items); err != nil {
		return fmt.Errorf("importing legacy state: %w", err)
	}
	if err := os.Rename(legacyPath, legacyPath+".migrated"); err != nil {
		// Import already succeeded; a rename failure just means the old file
		// lingers (and could theoretically be re-imported into a future
		// empty database), which is harmless.
		fmt.Fprintf(os.Stderr, "warning: migrated legacy state but could not rename %s: %s\n", legacyPath, err)
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// Upsert inserts new items and updates the mutable fields (title, published,
// summary) of existing ones, without touching their stored `read` flag.
// It returns the subset of items that were not previously known, so callers
// can surface "new article" notifications.
func (s *Store) Upsert(items []feed.Item) ([]feed.Item, error) {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	existing, err := s.KnownIDs(ids)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var fresh []feed.Item
	now := time.Now().UTC().Format(time.RFC3339)
	for _, item := range items {
		_, err := tx.Exec(`
			INSERT INTO articles (id, feed, title, url, published, summary, read, first_seen)
			VALUES (?, ?, ?, ?, ?, ?, 0, ?)
			ON CONFLICT(id) DO UPDATE SET
				feed = excluded.feed,
				title = excluded.title,
				url = excluded.url,
				published = excluded.published,
				summary = excluded.summary
		`, item.ID, item.Feed, item.Title, item.URL, item.Published, item.Summary, now)
		if err != nil {
			return nil, fmt.Errorf("upserting article %s: %w", item.ID, err)
		}
		if !existing[item.ID] {
			fresh = append(fresh, item)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return fresh, nil
}

// List returns stored articles ordered by published date (newest first),
// capped at limit (0 or negative means no cap).
func (s *Store) List(limit int) ([]feed.Item, error) {
	query := `
		SELECT id, feed, title, url, published, summary, read,
			CASE WHEN content IS NOT NULL AND content != '' THEN 1 ELSE 0 END AS cached
		FROM articles
		ORDER BY published DESC
	`
	args := []interface{}{}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []feed.Item
	for rows.Next() {
		var it feed.Item
		var read, cached int
		if err := rows.Scan(&it.ID, &it.Feed, &it.Title, &it.URL, &it.Published, &it.Summary, &read, &cached); err != nil {
			return nil, err
		}
		it.Read = read != 0
		it.Cached = cached != 0
		items = append(items, it)
	}
	return items, rows.Err()
}

// KnownIDs returns the subset of the given ids that already exist in the store.
func (s *Store) KnownIDs(ids []string) (map[string]bool, error) {
	known := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return known, nil
	}
	placeholders := ""
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args[i] = id
	}
	rows, err := s.db.Query(`SELECT id FROM articles WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		known[id] = true
	}
	return known, rows.Err()
}

// SetContent caches extracted article content for a given article id.
func (s *Store) SetContent(id, content string) error {
	_, err := s.db.Exec(`UPDATE articles SET content = ? WHERE id = ?`, content, id)
	return err
}

// ContentForURL returns the cached content for the article with the given
// URL, if any has been prefetched. The second return value is false when
// there is no cached content yet (article unknown, or not prefetched).
func (s *Store) ContentForURL(url string) (string, bool, error) {
	var content sql.NullString
	err := s.db.QueryRow(`SELECT content FROM articles WHERE url = ? LIMIT 1`, url).Scan(&content)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !content.Valid || content.String == "" {
		return "", false, nil
	}
	return content.String, true, nil
}

// IDForURL returns the stored article id matching url, or "" if unknown.
func (s *Store) IDForURL(url string) (string, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM articles WHERE url = ? LIMIT 1`, url).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// PendingPrefetch returns up to limit unread articles that have no cached
// content yet, newest first, so the background prefetcher can warm the
// cache for the articles a reader is most likely to open next.
func (s *Store) PendingPrefetch(limit int) ([]feed.Item, error) {
	query := `
		SELECT id, feed, title, url, published, summary, read
		FROM articles
		WHERE read = 0 AND (content IS NULL OR content = '')
		ORDER BY published DESC
	`
	args := []interface{}{}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []feed.Item
	for rows.Next() {
		var it feed.Item
		var read int
		if err := rows.Scan(&it.ID, &it.Feed, &it.Title, &it.URL, &it.Published, &it.Summary, &read); err != nil {
			return nil, err
		}
		it.Read = read != 0
		items = append(items, it)
	}
	return items, rows.Err()
}

// Import inserts items coming from a legacy state file (e.g. the old
// JSON-based state), preserving their read flag. Unlike Upsert, it never
// overwrites an existing row: once an article has been fetched normally,
// the legacy import is no longer authoritative for it.
func (s *Store) Import(items []feed.Item) (imported int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, item := range items {
		if item.ID == "" || item.URL == "" || item.Title == "" {
			continue
		}
		res, err := tx.Exec(`
			INSERT INTO articles (id, feed, title, url, published, summary, read, first_seen)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO NOTHING
		`, item.ID, item.Feed, item.Title, item.URL, item.Published, item.Summary, boolToInt(item.Read), now)
		if err != nil {
			return 0, fmt.Errorf("importing article %s: %w", item.ID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			imported++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return imported, nil
}

// MarkRead sets the read flag for a single article id.
func (s *Store) MarkRead(id string, read bool) error {
	_, err := s.db.Exec(`UPDATE articles SET read = ? WHERE id = ?`, boolToInt(read), id)
	return err
}

// MarkAllRead sets the read flag for every stored article.
func (s *Store) MarkAllRead(read bool) error {
	_, err := s.db.Exec(`UPDATE articles SET read = ?`, boolToInt(read))
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
