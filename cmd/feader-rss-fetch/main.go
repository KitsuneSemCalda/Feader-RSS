// Command feader-rss-fetch fetches RSS 2.0 / Atom feeds, extracts article
// text, and persists everything in a local SQLite database for the
// Quickshell UI. Usage:
//
//	feader-rss-fetch fetch --db PATH [--limit N] [--retention N] name1 url1 name2 url2 ...
//	feader-rss-fetch list --db PATH [--limit N] [--feed NAME ...]
//	feader-rss-fetch search --db PATH --query QUERY [--limit N] [--feed NAME ...]
//	feader-rss-fetch article [--db PATH] URL
//	feader-rss-fetch prefetch --db PATH [--limit N] [--concurrency N]
//	feader-rss-fetch mark-read --db PATH ID
//	feader-rss-fetch mark-all --db PATH [--unread] [--feed NAME ...]
//	feader-rss-fetch star --db PATH [--value true|false] ID
//	feader-rss-fetch set-tags --db PATH [--tags tag1,tag2] ID
//	feader-rss-fetch opml-export --config PATH [--output PATH]
//	feader-rss-fetch opml-import --config PATH --input PATH [--merge]
//	feader-rss-fetch migrate --db PATH --json PATH
//	feader-rss-fetch backup --db PATH --output PATH
//	feader-rss-fetch restore --db PATH --input PATH
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/KitsuneSemCalda/feader-rss/internal/article"
	"github.com/KitsuneSemCalda/feader-rss/internal/feed"
	"github.com/KitsuneSemCalda/feader-rss/internal/opml"
	"github.com/KitsuneSemCalda/feader-rss/internal/store"
)

type feedError struct {
	Feed  string `json:"feed"`
	URL   string `json:"url"`
	Error string `json:"error"`
}

type snapshot struct {
	Items           []feed.Item `json:"items"`
	Errors          []feedError `json:"errors"`
	NewItems        []feed.Item `json:"newItems"`
	UnreadCount     int         `json:"unreadCount"`
	UnreadFeedCount int         `json:"unreadFeedCount"`
}

type stringList []string

// Keep the network boundaries replaceable in tests. The production defaults
// remain the concrete fetchers used by the CLI.
var (
	fetchFeedWithRetry    = feed.FetchWithRetry
	fetchArticle          = article.Fetch
	fetchArticleWithRetry = article.FetchWithRetry
)

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: feader-rss-fetch <fetch|list|search|article|prefetch|mark-read|mark-all|star|set-tags|opml-export|opml-import|migrate|backup|restore> ...")
		return 2
	}
	switch args[0] {
	case "fetch":
		return cmdFetch(args[1:])
	case "list":
		return cmdList(args[1:])
	case "search":
		return cmdSearch(args[1:])
	case "article":
		return cmdArticle(args[1:])
	case "prefetch":
		return cmdPrefetch(args[1:])
	case "mark-read":
		return cmdMarkRead(args[1:])
	case "mark-all":
		return cmdMarkAll(args[1:])
	case "star":
		return cmdStar(args[1:])
	case "set-tags":
		return cmdSetTags(args[1:])
	case "opml-export":
		return cmdOPMLExport(args[1:])
	case "opml-import":
		return cmdOPMLImport(args[1:])
	case "migrate":
		return cmdMigrate(args[1:])
	case "backup":
		return cmdBackup(args[1:])
	case "restore":
		return cmdRestore(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", args[0])
		return 2
	}
}

func cmdBackup(args []string) int {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the SQLite state database")
	outputPath := fs.String("output", "", "path for the consistent SQLite snapshot")
	fs.Parse(args)

	if *dbPath == "" || *outputPath == "" {
		fmt.Fprintln(os.Stderr, "backup: --db and --output are required")
		return 2
	}
	if err := store.Backup(*dbPath, *outputPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func cmdRestore(args []string) int {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the active SQLite state database")
	inputPath := fs.String("input", "", "path to the SQLite snapshot")
	fs.Parse(args)

	if *dbPath == "" || *inputPath == "" {
		fmt.Fprintln(os.Stderr, "restore: --db and --input are required")
		return 2
	}
	if err := store.Restore(*dbPath, *inputPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func cmdOPMLExport(args []string) int {
	fs := flag.NewFlagSet("opml-export", flag.ExitOnError)
	configPath := fs.String("config", "", "path to the Feader JSON configuration")
	outputPath := fs.String("output", "", "path for the OPML document; stdout when omitted")
	fs.Parse(args)
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "opml-export: --config is required")
		return 2
	}
	data, err := os.ReadFile(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var config struct {
		Feeds []opml.Feed `json:"feeds"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		fmt.Fprintln(os.Stderr, "opml-export: invalid JSON configuration:", err)
		return 1
	}

	var output io.Writer = os.Stdout
	var file *os.File
	if *outputPath != "" {
		file, err = os.OpenFile(*outputPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer file.Close()
		output = file
	}
	if err := opml.Export(output, config.Feeds); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func cmdOPMLImport(args []string) int {
	fs := flag.NewFlagSet("opml-import", flag.ExitOnError)
	configPath := fs.String("config", "", "path to the Feader JSON configuration")
	inputPath := fs.String("input", "", "path to the OPML document")
	merge := fs.Bool("merge", false, "append feeds instead of replacing the current list")
	fs.Parse(args)
	if *configPath == "" || *inputPath == "" {
		fmt.Fprintln(os.Stderr, "opml-import: --config and --input are required")
		return 2
	}
	input, err := os.Open(*inputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	feeds, err := opml.Import(input)
	input.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	config, current, err := readConfigForOPML(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	importedCount := len(feeds)
	if *merge {
		feeds = mergeOPMLFeeds(current, feeds)
	}
	feeds, truncated := opml.Truncate(feeds)
	encodedFeeds, err := json.Marshal(feeds)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	config["feeds"] = encodedFeeds
	if err := writeJSONConfig(*configPath, config); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return printJSON(map[string]interface{}{
		"imported":  importedCount,
		"total":     len(feeds),
		"merged":    *merge,
		"truncated": truncated,
		"config":    *configPath,
	})
}

func readConfigForOPML(path string) (map[string]json.RawMessage, []opml.Feed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, nil, err
		}
		return map[string]json.RawMessage{
			"maxItems":       json.RawMessage("200"),
			"refreshMinutes": json.RawMessage("5"),
		}, []opml.Feed{}, nil
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(data, &config); err != nil || config == nil {
		return nil, nil, fmt.Errorf("opml-import: invalid JSON configuration")
	}
	var feeds []opml.Feed
	if raw, ok := config["feeds"]; ok {
		if err := json.Unmarshal(raw, &feeds); err != nil {
			return nil, nil, fmt.Errorf("opml-import: invalid feeds configuration: %w", err)
		}
	}
	return config, feeds, nil
}

func mergeOPMLFeeds(current, imported []opml.Feed) []opml.Feed {
	result := append([]opml.Feed{}, current...)
	seen := make(map[string]bool, len(result))
	for _, feed := range result {
		seen[strings.ToLower(strings.TrimRight(strings.TrimSpace(feed.URL), "/"))] = true
	}
	for _, feed := range imported {
		key := strings.ToLower(strings.TrimRight(strings.TrimSpace(feed.URL), "/"))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, feed)
	}
	return result
}

func writeJSONConfig(path string, config map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".rss-reader-config-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func printJSON(v interface{}) int {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func cmdFetch(args []string) int {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the SQLite state database")
	limit := fs.Int("limit", 200, "maximum number of items to return")
	retention := fs.Int("retention", 1000, "maximum number of stored items; 0 disables retention")
	attempts := fs.Int("attempts", 3, "maximum attempts for transient feed failures")
	backoff := fs.Duration("retry-backoff", 500*time.Millisecond, "initial retry delay")
	fs.Parse(args)

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "fetch: --db is required")
		return 2
	}
	feedArgs := fs.Args()
	if len(feedArgs)%2 != 0 {
		fmt.Fprintln(os.Stderr, "fetch: feed arguments must be name/url pairs")
		return 2
	}

	var fetched []feed.Item
	var errs []feedError
	feedNames := make([]string, 0, len(feedArgs)/2)
	feedURLs := make(map[string]string, len(feedArgs)/2)
	for i := 0; i+1 < len(feedArgs); i += 2 {
		name, url := feedArgs[i], feedArgs[i+1]
		feedNames = append(feedNames, name)
		feedURLs[name] = url
		items, err := fetchFeedWithRetry(name, url, *attempts, *backoff)
		if err != nil {
			errs = append(errs, feedError{Feed: name, URL: url, Error: err.Error()})
			fmt.Fprintf(os.Stderr, "%s: %s\n", name, err)
			continue
		}
		fetched = append(fetched, items...)
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()

	if _, err := db.MigrateArticleIdentity(feedURLs); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	newItems, err := db.Upsert(fetched)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, err := db.Prune(*retention); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	items, err := db.List(*limit, feedNames...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	result, err := makeSnapshot(db, items, errs, newItems, feedNames...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return printJSON(result)
}

func cmdList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the SQLite state database")
	limit := fs.Int("limit", 200, "maximum number of items to return")
	var feedNames stringList
	fs.Var(&feedNames, "feed", "only include this feed (repeatable)")
	fs.Parse(args)

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "list: --db is required")
		return 2
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()

	items, err := db.List(*limit, feedNames...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	result, err := makeSnapshot(db, items, nil, nil, feedNames...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return printJSON(result)
}

func cmdSearch(args []string) int {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the SQLite state database")
	query := fs.String("query", "", "full-text query")
	limit := fs.Int("limit", 200, "maximum number of items to return")
	var feedNames stringList
	fs.Var(&feedNames, "feed", "only include this feed (repeatable)")
	fs.Parse(args)

	if *dbPath == "" || strings.TrimSpace(*query) == "" {
		fmt.Fprintln(os.Stderr, "search: --db and --query are required")
		return 2
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()

	items, err := db.SearchForFeeds(*query, *limit, feedNames...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	result, err := makeSnapshot(db, items, nil, nil, feedNames...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return printJSON(result)
}

func makeSnapshot(db *store.Store, items []feed.Item, errs []feedError, newItems []feed.Item, feedNames ...string) (snapshot, error) {
	unread, unreadFeeds, err := db.UnreadCounts(feedNames...)
	if err != nil {
		return snapshot{}, fmt.Errorf("counting unread articles: %w", err)
	}
	return snapshot{
		Items:           nonNilItems(items),
		Errors:          nonNilErrors(errs),
		NewItems:        nonNilItems(newItems),
		UnreadCount:     unread,
		UnreadFeedCount: unreadFeeds,
	}, nil
}

func cmdArticle(args []string) int {
	fs := flag.NewFlagSet("article", flag.ExitOnError)
	dbPath := fs.String("db", "", "optional path to read/write cached content")
	fs.Parse(args)

	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "article: expected exactly one URL argument")
		return 2
	}
	url := fs.Arg(0)

	var db *store.Store
	if *dbPath != "" {
		opened, err := store.Open(*dbPath)
		if err == nil {
			db = opened
			defer db.Close()
		}
	}

	// Serve instantly from cache when the background prefetcher already
	// downloaded this article, instead of hitting the network again.
	if db != nil {
		if cached, ok, err := db.ContentForURL(url); err == nil && ok {
			return printJSON(article.Article{URL: url, Title: "", Content: cached})
		}
	}

	result, err := fetchArticle(url)
	if err != nil {
		printJSON(map[string]string{"error": err.Error(), "url": url})
		return 1
	}
	if db != nil {
		if id, err := db.IDForURL(url); err == nil && id != "" {
			_ = db.SetContent(id, result.Content)
		}
	}
	return printJSON(result)
}

func cmdPrefetch(args []string) int {
	fs := flag.NewFlagSet("prefetch", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the SQLite state database")
	limit := fs.Int("limit", 20, "maximum number of articles to prefetch")
	concurrency := fs.Int("concurrency", 3, "number of articles to fetch in parallel")
	fs.Parse(args)

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "prefetch: --db is required")
		return 2
	}
	if *concurrency < 1 {
		*concurrency = 1
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()

	pending, err := db.PendingPrefetch(*limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	var wg sync.WaitGroup
	var mu sync.Mutex // guards db writes; modernc.org/sqlite serializes on one *DB anyway
	sem := make(chan struct{}, *concurrency)
	prefetched := 0
	failed := 0

	for _, item := range pending {
		wg.Add(1)
		sem <- struct{}{}
		go func(item feed.Item) {
			defer wg.Done()
			defer func() { <-sem }()

			result, err := fetchArticleWithRetry(item.URL, 3, 500*time.Millisecond)
			if err != nil {
				fmt.Fprintf(os.Stderr, "prefetch %s: %s\n", item.URL, err)
				mu.Lock()
				recordErr := db.RecordPrefetchFailure(item.ID, err.Error())
				failed++
				mu.Unlock()
				if recordErr != nil {
					fmt.Fprintf(os.Stderr, "prefetch %s: recording failure: %s\n", item.URL, recordErr)
				}
				return
			}
			mu.Lock()
			setErr := db.SetContent(item.ID, result.Content)
			mu.Unlock()
			if setErr != nil {
				fmt.Fprintf(os.Stderr, "prefetch %s: caching content: %s\n", item.URL, setErr)
				return
			}
			mu.Lock()
			prefetched++
			mu.Unlock()
		}(item)
	}
	wg.Wait()

	return printJSON(map[string]int{"prefetched": prefetched, "failed": failed, "candidates": len(pending)})
}

func cmdMigrate(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the SQLite state database")
	jsonPath := fs.String("json", "", "path to the legacy items.json state file")
	fs.Parse(args)

	if *dbPath == "" || *jsonPath == "" {
		fmt.Fprintln(os.Stderr, "migrate: --db and --json are required")
		return 2
	}

	data, err := os.ReadFile(*jsonPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	var legacy struct {
		Items []feed.Item `json:"items"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		fmt.Fprintln(os.Stderr, "migrate: invalid legacy JSON:", err)
		return 1
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()

	imported, err := db.Import(legacy.Items)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return printJSON(map[string]int{"imported": imported, "candidates": len(legacy.Items)})
}

func cmdMarkRead(args []string) int {
	fs := flag.NewFlagSet("mark-read", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the SQLite state database")
	unread := fs.Bool("unread", false, "mark as unread instead of read")
	fs.Parse(args)

	if *dbPath == "" || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "mark-read: --db and exactly one article ID are required")
		return 2
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()
	if err := db.MarkRead(fs.Arg(0), !*unread); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func cmdMarkAll(args []string) int {
	fs := flag.NewFlagSet("mark-all", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the SQLite state database")
	unread := fs.Bool("unread", false, "mark all as unread instead of read")
	var feedNames stringList
	fs.Var(&feedNames, "feed", "only update this feed (repeatable)")
	fs.Parse(args)

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "mark-all: --db is required")
		return 2
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()
	if err := db.MarkAllRead(!*unread, feedNames...); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func cmdStar(args []string) int {
	fs := flag.NewFlagSet("star", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the SQLite state database")
	starred := fs.Bool("value", true, "saved/favorite state")
	fs.Parse(args)
	if *dbPath == "" || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "star: --db and exactly one article ID are required")
		return 2
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()
	if err := db.SetStarred(fs.Arg(0), *starred); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func cmdSetTags(args []string) int {
	fs := flag.NewFlagSet("set-tags", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the SQLite state database")
	tags := fs.String("tags", "", "comma-separated user tags")
	fs.Parse(args)
	if *dbPath == "" || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "set-tags: --db and exactly one article ID are required")
		return 2
	}
	values := make([]string, 0)
	for _, tag := range strings.Split(*tags, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			values = append(values, tag)
		}
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()
	if err := db.SetTags(fs.Arg(0), values); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func nonNilItems(items []feed.Item) []feed.Item {
	if items == nil {
		return []feed.Item{}
	}
	return items
}

func nonNilErrors(errs []feedError) []feedError {
	if errs == nil {
		return []feedError{}
	}
	return errs
}
