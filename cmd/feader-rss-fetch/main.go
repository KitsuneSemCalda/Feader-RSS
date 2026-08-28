// Command feader-rss-fetch fetches RSS 2.0 / Atom feeds, extracts article
// text, and persists everything in a local SQLite database for the
// Quickshell UI. Usage:
//
//	feader-rss-fetch fetch --db PATH [--limit N] name1 url1 name2 url2 ...
//	feader-rss-fetch list --db PATH [--limit N]
//	feader-rss-fetch article [--db PATH] URL
//	feader-rss-fetch prefetch --db PATH [--limit N] [--concurrency N]
//	feader-rss-fetch mark-read --db PATH ID
//	feader-rss-fetch mark-all --db PATH [--unread]
//	feader-rss-fetch migrate --db PATH --json PATH
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sync"

	"github.com/KitsuneSemCalda/feader-rss/internal/article"
	"github.com/KitsuneSemCalda/feader-rss/internal/feed"
	"github.com/KitsuneSemCalda/feader-rss/internal/store"
)

type feedError struct {
	Feed  string `json:"feed"`
	URL   string `json:"url"`
	Error string `json:"error"`
}

type snapshot struct {
	Items    []feed.Item `json:"items"`
	Errors   []feedError `json:"errors"`
	NewItems []feed.Item `json:"newItems"`
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: feader-rss-fetch <fetch|list|article|prefetch|mark-read|mark-all|migrate> ...")
		return 2
	}
	switch args[0] {
	case "fetch":
		return cmdFetch(args[1:])
	case "list":
		return cmdList(args[1:])
	case "article":
		return cmdArticle(args[1:])
	case "prefetch":
		return cmdPrefetch(args[1:])
	case "mark-read":
		return cmdMarkRead(args[1:])
	case "mark-all":
		return cmdMarkAll(args[1:])
	case "migrate":
		return cmdMigrate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", args[0])
		return 2
	}
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
	fs.Parse(args)

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "fetch: --db is required")
		return 2
	}
	feedArgs := fs.Args()

	var fetched []feed.Item
	var errs []feedError
	for i := 0; i+1 < len(feedArgs); i += 2 {
		name, url := feedArgs[i], feedArgs[i+1]
		items, err := feed.Fetch(name, url)
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

	newItems, err := db.Upsert(fetched)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	items, err := db.List(*limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	return printJSON(snapshot{
		Items:    nonNilItems(items),
		Errors:   nonNilErrors(errs),
		NewItems: nonNilItems(newItems),
	})
}

func cmdList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the SQLite state database")
	limit := fs.Int("limit", 200, "maximum number of items to return")
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

	items, err := db.List(*limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return printJSON(snapshot{Items: nonNilItems(items), Errors: []feedError{}, NewItems: []feed.Item{}})
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

	result, err := article.Fetch(url)
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

	for _, item := range pending {
		wg.Add(1)
		sem <- struct{}{}
		go func(item feed.Item) {
			defer wg.Done()
			defer func() { <-sem }()

			result, err := article.Fetch(item.URL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "prefetch %s: %s\n", item.URL, err)
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

	return printJSON(map[string]int{"prefetched": prefetched, "candidates": len(pending)})
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
	if err := db.MarkAllRead(!*unread); err != nil {
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
