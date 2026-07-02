package conductor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/tools"
)

// TestWebFetch (or-5j1 slice 2): web_fetch GETs a URL and returns HTML stripped to readable text,
// and refuses link-local / cloud-metadata addresses (the SSRF guard).
func TestWebFetch(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><style>.x{color:red}</style><title>t</title></head>` +
			`<body><h1>Hello &amp; welcome</h1><script>var x=1;</script><p>orion body text</p></body></html>`))
	}))
	defer srv.Close()

	r := tools.NewRegistry()
	registerWebTools(r)

	wf, ok := r.Get("web_fetch")
	if !ok {
		t.Fatal("web_fetch should be registered")
	}
	if !wf.Safety.ReadOnly {
		t.Error("web_fetch should be ReadOnly")
	}
	out, err := wf.Run(ctx, json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("web_fetch: %v", err)
	}
	if !strings.Contains(out, "orion body text") || !strings.Contains(out, "Hello & welcome") {
		t.Errorf("web_fetch should return readable text with entities decoded, got %q", out)
	}
	if strings.Contains(out, "var x=1") || strings.Contains(out, "color:red") {
		t.Errorf("web_fetch should strip <script>/<style>, got %q", out)
	}

	// SSRF guard: link-local / cloud-metadata address is refused.
	if _, err := wf.Run(ctx, json.RawMessage(`{"url":"http://169.254.169.254/latest/meta-data/"}`)); err == nil {
		t.Error("web_fetch should refuse the cloud-metadata address")
	}
	// A non-http(s) scheme is refused.
	if _, err := wf.Run(ctx, json.RawMessage(`{"url":"file:///etc/passwd"}`)); err == nil {
		t.Error("web_fetch should refuse a non-http(s) scheme")
	}
}

// TestDDGParse (or-5j1 slice 2): the DuckDuckGo HTML parser extracts title + real URL (unwrapped
// from the uddg redirect) + snippet.
func TestDDGParse(t *testing.T) {
	body := `<div class="results">
	  <div class="result">
	    <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fdoc&rut=abc">Example &amp; Docs</a>
	    <a class="result__snippet" href="//duckduckgo.com/l/?uddg=x">The <b>example</b> documentation site.</a>
	  </div>
	  <div class="result">
	    <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Forion.dev%2F">Orion</a>
	    <a class="result__snippet">Second snippet.</a>
	  </div>
	</div>`
	res := ddgParse(body)
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(res), res)
	}
	if res[0].Title != "Example & Docs" {
		t.Errorf("title: got %q", res[0].Title)
	}
	if res[0].URL != "https://example.com/doc" {
		t.Errorf("url should be unwrapped from uddg, got %q", res[0].URL)
	}
	if !strings.Contains(res[0].Snippet, "example documentation site") {
		t.Errorf("snippet: got %q", res[0].Snippet)
	}
	if res[1].URL != "https://orion.dev/" {
		t.Errorf("second url: got %q", res[1].URL)
	}
}

// TestWebSearchFormatsResults (or-5j1 slice 2): web_search hits its endpoint and formats results as
// numbered title + URL + snippet. Points ddgSearch at a mock DDG server via the injected client is
// not possible (fixed endpoint), so this drives the formatter through ddgSearch with a stub round
// tripper.
func TestWebSearchFormatsResults(t *testing.T) {
	ctx := context.Background()
	stub := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		html := `<a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fa.example%2F1">First</a>` +
			`<a class="result__snippet">snippet one</a>`
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(html)),
			Header:     make(http.Header),
		}, nil
	})}
	out, err := ddgSearch(ctx, stub, "anything")
	if err != nil {
		t.Fatalf("ddgSearch: %v", err)
	}
	if !strings.Contains(out, "1. First") || !strings.Contains(out, "https://a.example/1") || !strings.Contains(out, "snippet one") {
		t.Errorf("web_search formatting: got %q", out)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestStripHTML (or-5j1 slice 2): tag strip + entity decode + whitespace collapse.
func TestStripHTML(t *testing.T) {
	got := stripHTML("<p>a  &amp;  b</p>\n\n\n<div>c</div>")
	if strings.Contains(got, "<p>") || strings.Contains(got, "&amp;") {
		t.Errorf("stripHTML left markup/entities: %q", got)
	}
	if !strings.Contains(got, "a & b") {
		t.Errorf("stripHTML: got %q", got)
	}
}
