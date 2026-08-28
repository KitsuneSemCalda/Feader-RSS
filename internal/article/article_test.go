package article

import (
	"strings"
	"testing"
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
