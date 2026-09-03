package article

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/KitsuneSemCalda/feader-rss/internal/safefetch"
)

const samplePage = `<!doctype html>
<html>
<head><title>  My   Article Title  </title></head>
<body>
  <nav>Site nav should be skipped</nav>
  <header>Header should be skipped</header>
  <article>
    <h1>Headline</h1>
    <p>First paragraph with <b>bold</b> text.</p>
    <p>Second paragraph , with weird   spacing .</p>
  </article>
  <script>console.log("skip me")</script>
  <footer>Footer should be skipped</footer>
</body>
</html>`

func TestExtract(t *testing.T) {
	title, content, err := Extract(strings.NewReader(samplePage))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if title != "My Article Title" {
		t.Errorf("title = %q", title)
	}
	if strings.Contains(content, "Site nav") {
		t.Errorf("nav content leaked into output: %q", content)
	}
	if strings.Contains(content, "Header should be skipped") {
		t.Errorf("header content leaked into output: %q", content)
	}
	if strings.Contains(content, "Footer should be skipped") {
		t.Errorf("footer content leaked into output: %q", content)
	}
	if strings.Contains(content, "console.log") {
		t.Errorf("script content leaked into output: %q", content)
	}
	if !strings.Contains(content, "First paragraph with bold text.") {
		t.Errorf("expected paragraph text, got: %q", content)
	}
	if !strings.Contains(content, "Second paragraph, with weird spacing.") {
		t.Errorf("expected punctuation-normalized text, got: %q", content)
	}
}

func TestExtractFallsBackToFirstLineWhenNoTitleTag(t *testing.T) {
	title, _, err := Extract(strings.NewReader(`<html><body><p>Just a paragraph.</p></body></html>`))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if title != "Just a paragraph." {
		t.Errorf("title = %q", title)
	}
}

func TestExtractEmptyDocument(t *testing.T) {
	title, content, err := Extract(strings.NewReader(``))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if title != "Article" {
		t.Errorf("title = %q, want fallback 'Article'", title)
	}
	if content != "" {
		t.Errorf("content = %q, want empty", content)
	}
}

func TestFetchParsesHTMLAndFallsBackFromInvalidCharset(t *testing.T) {
	oldGet := get
	t.Cleanup(func() { get = oldGet })
	calls := 0
	get = func(url string, timeout time.Duration) (*http.Response, error) {
		calls++
		contentType := "text/html; charset=utf-8"
		if calls == 2 {
			contentType = "text/html; charset=not-a-real-charset"
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{contentType}},
			Body:       io.NopCloser(strings.NewReader(`<html><head><title>Fetched title</title></head><body><p>Fetched body.</p></body></html>`)),
		}, nil
	}

	first, err := Fetch("https://example.test/article")
	if err != nil {
		t.Fatalf("Fetch valid charset: %v", err)
	}
	if first.Title != "Fetched title" || !strings.Contains(first.Content, "Fetched body.") {
		t.Fatalf("first article = %+v", first)
	}
	second, err := Fetch("https://example.test/fallback")
	if err != nil {
		t.Fatalf("Fetch invalid charset: %v", err)
	}
	if second.Title != "Fetched title" || !strings.Contains(second.Content, "Fetched body.") {
		t.Fatalf("fallback article = %+v", second)
	}
}

func TestFetchPropagatesGetterAndResponseErrors(t *testing.T) {
	oldGet := get
	t.Cleanup(func() { get = oldGet })
	get = func(string, time.Duration) (*http.Response, error) {
		return nil, errors.New("network unavailable")
	}
	if _, err := Fetch("https://example.test/article"); err == nil || err.Error() != "network unavailable" {
		t.Errorf("getter error = %v", err)
	}

	get = func(string, time.Duration) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", MaxResponseBytes+1))),
		}, nil
	}
	if _, err := Fetch("https://example.test/large"); err == nil {
		t.Fatal("expected oversized response error")
	}
}

func TestFetchWithRetryRetriesTransientFailuresAndNormalizesOptions(t *testing.T) {
	oldFetch, oldSleep := fetchArticle, sleep
	t.Cleanup(func() {
		fetchArticle = oldFetch
		sleep = oldSleep
	})
	var delays []time.Duration
	sleep = func(delay time.Duration) { delays = append(delays, delay) }
	calls := 0
	fetchArticle = func(url string) (*Article, error) {
		calls++
		if calls <= 5 {
			return nil, &safefetch.HTTPStatusError{Code: http.StatusServiceUnavailable, Status: "503 Service Unavailable"}
		}
		return &Article{URL: url, Title: "Recovered", Content: "body"}, nil
	}
	result, err := FetchWithRetry("https://example.test/retry", 6, time.Second)
	if err != nil {
		t.Fatalf("FetchWithRetry: %v", err)
	}
	if result.Title != "Recovered" || calls != 6 {
		t.Fatalf("retry result=%+v calls=%d", result, calls)
	}
	wantDelays := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	if len(delays) != len(wantDelays) {
		t.Fatalf("retry delays = %v, want %v", delays, wantDelays)
	}
	for i := range delays {
		if delays[i] != wantDelays[i] {
			t.Errorf("delay %d = %s, want %s", i, delays[i], wantDelays[i])
		}
	}

	calls = 0
	delays = nil
	fetchArticle = func(string) (*Article, error) {
		calls++
		return nil, errors.New("invalid URL")
	}
	if result, err := FetchWithRetry("bad", 0, -time.Second); err == nil || result != nil || calls != 1 {
		t.Fatalf("permanent retry result=%+v err=%v calls=%d", result, err, calls)
	}
}
