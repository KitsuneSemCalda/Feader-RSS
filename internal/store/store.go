// Package store persists articles and their read state in a local SQLite
// database, replacing the JSON state file the Quickshell UI used to own.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	id            TEXT PRIMARY KEY,
	feed          TEXT NOT NULL,
	title         TEXT NOT NULL,
	url           TEXT NOT NULL,
	published     TEXT NOT NULL DEFAULT '',
	published_at  INTEGER NOT NULL DEFAULT 0,
	summary       TEXT NOT NULL DEFAULT '',
	author        TEXT NOT NULL DEFAULT '',
	categories    TEXT NOT NULL DEFAULT '',
	content       TEXT,
	read          INTEGER NOT NULL DEFAULT 0,
	starred       INTEGER NOT NULL DEFAULT 0,
	tags          TEXT NOT NULL DEFAULT '',
	prefetch_attempts INTEGER NOT NULL DEFAULT 0,
	prefetch_next_at TEXT NOT NULL DEFAULT '',
	prefetch_error TEXT NOT NULL DEFAULT '',
	first_seen    TEXT NOT NULL
);
`

const indexSchema = `
CREATE INDEX IF NOT EXISTS idx_articles_published_at ON articles(published_at DESC, first_seen DESC);
CREATE INDEX IF NOT EXISTS idx_articles_url ON articles(url);
`

const ftsSchema = `
CREATE VIRTUAL TABLE IF NOT EXISTS articles_fts USING fts5(
	 id UNINDEXED,
	 title,
	 summary,
	 content,
	 author,
	 categories,
	 tags
);
`

// Store wraps a SQLite-backed article database.
type Store struct {
	db *sql.DB
}

// Open creates (if needed) and opens the database at path. The state
// directory and the database file (including its WAL/SHM sidecars) are
// restricted to the owner only, since the article cache can reveal a user's
// reading history.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("creating state dir: %w", err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, fmt.Errorf("restricting state dir permissions: %w", err)
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
	if err := ensureColumns(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating schema: %w", err)
	}
	if _, err := db.Exec(indexSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying indexes: %w", err)
	}
	if _, err := db.Exec(ftsSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying full-text search schema: %w", err)
	}
	s := &Store{db: db}
	if err := s.backfillPublishedAt(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not normalize stored publication dates: %s\n", err)
	}
	if err := s.autoMigrateLegacyState(path); err != nil {
		// Never fail startup over a migration hiccup: the app should still
		// work with an empty history rather than refuse to open.
		fmt.Fprintf(os.Stderr, "warning: could not migrate legacy state: %s\n", err)
	}
	if err := rebuildFTS(s.db); err != nil {
		s.Close()
		return nil, fmt.Errorf("building full-text search index: %w", err)
	}
	if err := restrictDatabasePermissions(path); err != nil {
		s.Close()
		return nil, fmt.Errorf("restricting database file permissions: %w", err)
	}
	return s, nil
}

// restrictDatabasePermissions locks the database file and its WAL/SHM
// sidecars (when present) down to owner-only read/write, since SQLite (and
// the OS umask) would otherwise leave them group/world readable.
func restrictDatabasePermissions(path string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		candidate := path + suffix
		if _, err := os.Stat(candidate); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := os.Chmod(candidate, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// ensureColumns adds fields introduced after the initial SQLite migration to
// databases that already exist on a user's machine.
func ensureColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(articles)`)
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "published_at", definition: "published_at INTEGER NOT NULL DEFAULT 0"},
		{name: "author", definition: "author TEXT NOT NULL DEFAULT ''"},
		{name: "categories", definition: "categories TEXT NOT NULL DEFAULT ''"},
		{name: "starred", definition: "starred INTEGER NOT NULL DEFAULT 0"},
		{name: "tags", definition: "tags TEXT NOT NULL DEFAULT ''"},
		{name: "prefetch_attempts", definition: "prefetch_attempts INTEGER NOT NULL DEFAULT 0"},
		{name: "prefetch_next_at", definition: "prefetch_next_at TEXT NOT NULL DEFAULT ''"},
		{name: "prefetch_error", definition: "prefetch_error TEXT NOT NULL DEFAULT ''"},
	} {
		if columns[column.name] {
			continue
		}
		if _, err := db.Exec("ALTER TABLE articles ADD COLUMN " + column.definition); err != nil {
			return fmt.Errorf("adding %s: %w", column.name, err)
		}
	}
	return nil
}

type sqlExecer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

func rebuildFTS(execer sqlExecer) error {
	if _, err := execer.Exec(`DELETE FROM articles_fts`); err != nil {
		return err
	}
	_, err := execer.Exec(`
		INSERT INTO articles_fts (id, title, summary, content, author, categories, tags)
		SELECT id, title, summary, COALESCE(content, ''), author, categories, tags
		FROM articles
	`)
	return err
}

func (s *Store) backfillPublishedAt() error {
	rows, err := s.db.Query(`
		SELECT id, published FROM articles
		WHERE published_at = 0 AND published != ''
	`)
	if err != nil {
		return err
	}
	type articleDate struct {
		id        string
		published string
	}
	var dates []articleDate
	for rows.Next() {
		var date articleDate
		if err := rows.Scan(&date.id, &date.published); err != nil {
			rows.Close()
			return err
		}
		dates = append(dates, date)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, date := range dates {
		if timestamp := feed.PublishedTimestamp(date.published); timestamp > 0 {
			if _, err := s.db.Exec(`UPDATE articles SET published_at = ? WHERE id = ?`, timestamp, date.id); err != nil {
				return err
			}
		}
	}
	return nil
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

// MigrateArticleIdentity moves stored articles from the legacy id scheme
// (keyed on the feed's editable display name) to the current one (keyed on
// the feed's URL), for every feed in feeds (display name -> URL). This lets
// a user rename a feed without losing read/starred/tag state or
// re-triggering "new article" notifications for articles already seen under
// the old id. Rows belonging to a feed not present in feeds are left
// untouched, since there is no current URL to migrate them to. It returns
// the number of rows renamed.
func (s *Store) MigrateArticleIdentity(feeds map[string]string) (int, error) {
	if len(feeds) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	migrated := 0
	for name, feedURL := range feeds {
		feedURL = strings.TrimSpace(feedURL)
		if name == "" || feedURL == "" {
			continue
		}
		rows, err := tx.Query(`SELECT id, url FROM articles WHERE feed = ?`, name)
		if err != nil {
			return 0, err
		}
		type legacyRow struct{ id, url string }
		var candidates []legacyRow
		for rows.Next() {
			var r legacyRow
			if err := rows.Scan(&r.id, &r.url); err != nil {
				rows.Close()
				return 0, err
			}
			candidates = append(candidates, r)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return 0, err
		}
		rows.Close()

		for _, r := range candidates {
			if r.id != feed.LegacyArticleID(name, r.url) {
				continue // already on the current scheme, or a foreign/imported id
			}
			newID := feed.ArticleID(feedURL, r.url)
			if newID == r.id {
				continue
			}
			var exists int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM articles WHERE id = ?`, newID).Scan(&exists); err != nil {
				return 0, err
			}
			if exists > 0 {
				continue // target id already taken; leave the legacy row alone
			}
			if _, err := tx.Exec(`UPDATE articles SET id = ? WHERE id = ?`, newID, r.id); err != nil {
				return 0, err
			}
			migrated++
		}
	}
	if migrated == 0 {
		return 0, nil
	}
	if err := rebuildFTS(tx); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return migrated, nil
}

// Upsert inserts new items and updates mutable feed fields (title, published,
// summary, author, categories) without touching stored read/starred/tags flags.
// It returns the subset of items that were not previously known, so callers
// can surface "new article" notifications.
func (s *Store) Upsert(items []feed.Item) ([]feed.Item, error) {
	items = deduplicateItems(items)
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
		publishedAt := item.PublishedAt
		if publishedAt == 0 {
			publishedAt = feed.PublishedTimestamp(item.Published)
		}
		_, err := tx.Exec(`
				INSERT INTO articles (id, feed, title, url, published, published_at, summary, author, categories, read, starred, tags, first_seen)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET
				feed = excluded.feed,
				title = excluded.title,
				url = excluded.url,
				published = excluded.published,
				published_at = excluded.published_at,
					summary = excluded.summary,
					author = excluded.author,
					categories = excluded.categories
			`, item.ID, item.Feed, item.Title, item.URL, item.Published, publishedAt, item.Summary, item.Author, encodeCategories(item.Categories), boolToInt(item.Starred), encodeTags(item.Tags), now)
		if err != nil {
			return nil, fmt.Errorf("upserting article %s: %w", item.ID, err)
		}
		if !existing[item.ID] {
			fresh = append(fresh, item)
		}
	}
	if err := rebuildFTS(tx); err != nil {
		return nil, fmt.Errorf("updating full-text search index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return fresh, nil
}

// List returns stored articles ordered by published date (newest first),
// capped at limit (0 or negative means no cap). Optional feed names scope the
// result to the feeds currently configured by the caller.
func (s *Store) List(limit int, feedNames ...string) ([]feed.Item, error) {
	query := `
		SELECT id, feed, title, url, published, published_at, summary, author, categories, read,
			starred, tags,
			CASE WHEN content IS NOT NULL AND content != '' THEN 1 ELSE 0 END AS cached
		FROM articles
	`
	where, filterArgs := feedFilter(feedNames)
	if where != "" {
		query += " WHERE " + where
	}
	query += " ORDER BY published_at DESC, first_seen DESC, id DESC"
	args := filterArgs
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
		var read, starred, cached int
		var categories, tags string
		if err := rows.Scan(&it.ID, &it.Feed, &it.Title, &it.URL, &it.Published, &it.PublishedAt, &it.Summary, &it.Author, &categories, &read, &starred, &tags, &cached); err != nil {
			return nil, err
		}
		it.Read = read != 0
		it.Starred = starred != 0
		it.Cached = cached != 0
		it.Categories = decodeCategories(categories)
		it.Tags = decodeTags(tags)
		items = append(items, it)
	}
	return items, rows.Err()
}

// Search performs a full-text search across titles, summaries, cached article
// content, authors, categories and user tags. Terms are quoted individually
// so punctuation supplied by a user cannot become FTS5 syntax.
func (s *Store) Search(query string, limit int) ([]feed.Item, error) {
	return s.search(query, limit)
}

// SearchForFeeds is the feed-scoped form of Search used by the panel. The
// separate method keeps the simple two-argument Search API convenient for
// command-line and library callers.
func (s *Store) SearchForFeeds(query string, limit int, feedNames ...string) ([]feed.Item, error) {
	return s.search(query, limit, feedNames...)
}

func (s *Store) search(query string, limit int, feedNames ...string) ([]feed.Item, error) {
	match := ftsMatchQuery(query)
	if match == "" {
		return []feed.Item{}, nil
	}
	sqlQuery := `
		SELECT a.id, a.feed, a.title, a.url, a.published, a.published_at, a.summary, a.author,
			a.categories, a.read, a.starred, a.tags,
			CASE WHEN a.content IS NOT NULL AND a.content != '' THEN 1 ELSE 0 END AS cached
		FROM articles AS a
		JOIN articles_fts AS f ON f.id = a.id
		WHERE articles_fts MATCH ?
	`
	args := []interface{}{match}
	if where, filterArgs := feedFilter(feedNames); where != "" {
		sqlQuery += " AND " + strings.ReplaceAll(where, "feed", "a.feed")
		args = append(args, filterArgs...)
	}
	sqlQuery += " ORDER BY a.published_at DESC, a.first_seen DESC, a.id DESC"
	if limit > 0 {
		sqlQuery += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.Query(sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArticleRows(rows)
}

func ftsMatchQuery(query string) string {
	terms := strings.Fields(strings.TrimSpace(query))
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(strings.ReplaceAll(term, `"`, ""))
		if term == "" {
			continue
		}
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " AND ")
}

func scanArticleRows(rows *sql.Rows) ([]feed.Item, error) {
	var items []feed.Item
	for rows.Next() {
		var it feed.Item
		var read, starred, cached int
		var categories, tags string
		if err := rows.Scan(&it.ID, &it.Feed, &it.Title, &it.URL, &it.Published, &it.PublishedAt, &it.Summary, &it.Author, &categories, &read, &starred, &tags, &cached); err != nil {
			return nil, err
		}
		it.Read = read != 0
		it.Starred = starred != 0
		it.Cached = cached != 0
		it.Categories = decodeCategories(categories)
		it.Tags = decodeTags(tags)
		items = append(items, it)
	}
	return items, rows.Err()
}

// UnreadCounts returns the unread article count and the number of feeds that
// contain unread articles. Supplying feed names scopes the result to the
// currently configured feeds; omitting them counts the whole database.
func (s *Store) UnreadCounts(feedNames ...string) (unread, unreadFeeds int, err error) {
	where := "read = 0"
	args := make([]interface{}, 0, len(feedNames))
	if feedWhere, feedArgs := feedFilter(feedNames); feedWhere != "" {
		where += " AND " + feedWhere
		args = append(args, feedArgs...)
	}
	err = s.db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT feed) FROM articles WHERE `+where, args...).Scan(&unread, &unreadFeeds)
	return unread, unreadFeeds, err
}

// Prune removes the oldest articles until at most maxItems remain. Read and
// unstarred entries are preferred for removal, while unread/starred entries
// survive longer. A non-positive value disables retention.
func (s *Store) Prune(maxItems int) (int, error) {
	if maxItems <= 0 {
		return 0, nil
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM articles`).Scan(&count); err != nil {
		return 0, err
	}
	if count <= maxItems {
		return 0, nil
	}
	remove := count - maxItems
	rows, err := s.db.Query(`
		SELECT id FROM articles
		ORDER BY read DESC, starred ASC, published_at ASC, first_seen ASC, id ASC
		LIMIT ?
	`, remove)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(ids) == 0 {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := tx.Exec(`DELETE FROM articles WHERE id IN (`+placeholders+`)`, args...); err != nil {
		return 0, err
	}
	if err := rebuildFTS(tx); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
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
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE articles SET content = ?, prefetch_attempts = 0, prefetch_next_at = '', prefetch_error = '' WHERE id = ?`, content, id); err != nil {
		return err
	}
	if err := rebuildFTS(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordPrefetchFailure remembers a failed article fetch and delays its next
// attempt exponentially, preventing every panel refresh from issuing the same
// doomed request. The delay starts at one minute and is capped at six hours.
func (s *Store) RecordPrefetchFailure(id, message string) error {
	var attempts int
	if err := s.db.QueryRow(`SELECT prefetch_attempts FROM articles WHERE id = ?`, id).Scan(&attempts); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	if attempts < 16 {
		attempts++
	}
	delay := time.Minute
	for i := 1; i < attempts; i++ {
		if delay >= 6*time.Hour {
			delay = 6 * time.Hour
			break
		}
		delay *= 2
	}
	if delay > 6*time.Hour {
		delay = 6 * time.Hour
	}
	next := time.Now().UTC().Add(delay).Format(time.RFC3339)
	_, err := s.db.Exec(`
		UPDATE articles
		SET prefetch_attempts = ?, prefetch_next_at = ?, prefetch_error = ?
		WHERE id = ?
	`, attempts, next, strings.TrimSpace(message), id)
	return err
}

// SetStarred toggles the saved/favorite state of one article.
func (s *Store) SetStarred(id string, starred bool) error {
	_, err := s.db.Exec(`UPDATE articles SET starred = ? WHERE id = ?`, boolToInt(starred), id)
	return err
}

// SetTags replaces an article's user tags.
func (s *Store) SetTags(id string, tags []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE articles SET tags = ? WHERE id = ?`, encodeTags(tags), id); err != nil {
		return err
	}
	if err := rebuildFTS(tx); err != nil {
		return err
	}
	return tx.Commit()
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
		SELECT id, feed, title, url, published, published_at, summary, author, categories, read, starred, tags
		FROM articles
		WHERE read = 0 AND (content IS NULL OR content = '')
			AND (prefetch_next_at = '' OR prefetch_next_at <= ?)
		ORDER BY published_at DESC, first_seen DESC, id DESC
	`
	args := []interface{}{time.Now().UTC().Format(time.RFC3339)}
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
		var read, starred int
		var categories, tags string
		if err := rows.Scan(&it.ID, &it.Feed, &it.Title, &it.URL, &it.Published, &it.PublishedAt, &it.Summary, &it.Author, &categories, &read, &starred, &tags); err != nil {
			return nil, err
		}
		it.Read = read != 0
		it.Starred = starred != 0
		it.Categories = decodeCategories(categories)
		it.Tags = decodeTags(tags)
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
		publishedAt := item.PublishedAt
		if publishedAt == 0 {
			publishedAt = feed.PublishedTimestamp(item.Published)
		}
		res, err := tx.Exec(`
			INSERT INTO articles (id, feed, title, url, published, published_at, summary, author, categories, read, starred, tags, first_seen)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO NOTHING
		`, item.ID, item.Feed, item.Title, item.URL, item.Published, publishedAt, item.Summary, item.Author, encodeCategories(item.Categories), boolToInt(item.Read), boolToInt(item.Starred), encodeTags(item.Tags), now)
		if err != nil {
			return 0, fmt.Errorf("importing article %s: %w", item.ID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			imported++
		}
	}
	if err := rebuildFTS(tx); err != nil {
		return 0, fmt.Errorf("updating full-text search index: %w", err)
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

// MarkAllRead sets the read flag for every stored article, or only the
// supplied feeds when feed names are provided.
func (s *Store) MarkAllRead(read bool, feedNames ...string) error {
	query := `UPDATE articles SET read = ?`
	args := []interface{}{boolToInt(read)}
	if where, filterArgs := feedFilter(feedNames); where != "" {
		query += " WHERE " + where
		args = append(args, filterArgs...)
	}
	_, err := s.db.Exec(query, args...)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func encodeCategories(categories []string) string {
	if len(categories) == 0 {
		return ""
	}
	data, err := json.Marshal(categories)
	if err != nil {
		return ""
	}
	return string(data)
}

func encodeTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	cleaned := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		tag = strings.Join(strings.Fields(tag), " ")
		key := strings.ToLower(tag)
		if tag == "" || seen[key] {
			continue
		}
		seen[key] = true
		cleaned = append(cleaned, tag)
	}
	if len(cleaned) == 0 {
		return ""
	}
	data, err := json.Marshal(cleaned)
	if err != nil {
		return ""
	}
	return string(data)
}

func decodeCategories(value string) []string {
	if value == "" {
		return []string{}
	}
	var categories []string
	if err := json.Unmarshal([]byte(value), &categories); err != nil || categories == nil {
		return []string{}
	}
	return categories
}

func decodeTags(value string) []string {
	if value == "" {
		return []string{}
	}
	var tags []string
	if err := json.Unmarshal([]byte(value), &tags); err != nil || tags == nil {
		return []string{}
	}
	return tags
}

func feedFilter(feedNames []string) (string, []interface{}) {
	if len(feedNames) == 0 {
		return "", nil
	}
	seen := make(map[string]bool, len(feedNames))
	placeholders := make([]string, 0, len(feedNames))
	args := make([]interface{}, 0, len(feedNames))
	for _, name := range feedNames {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		placeholders = append(placeholders, "?")
		args = append(args, name)
	}
	if len(placeholders) == 0 {
		return "1 = 0", nil
	}
	return "feed IN (" + strings.Join(placeholders, ",") + ")", args
}

func deduplicateItems(items []feed.Item) []feed.Item {
	seen := make(map[string]bool, len(items))
	result := make([]feed.Item, 0, len(items))
	for _, item := range items {
		if item.ID == "" || item.URL == "" || item.Title == "" {
			continue
		}
		key := strings.TrimSpace(item.Feed) + "\x00" + strings.TrimSpace(item.URL)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, item)
	}
	return result
}
