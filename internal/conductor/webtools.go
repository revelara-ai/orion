package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/tools"
)

// registerWebTools gives the Conductor web reach (or-5j1 slice 2): fetch a URL and search the web.
// Both are read-only. web_search is keyless (scrapes DuckDuckGo's HTML endpoint) — best-effort, no
// provider key needed. web_fetch refuses link-local / cloud-metadata addresses (a minimal SSRF
// guard so a prompt-injected fetch can't reach 169.254.169.254 to lift cloud credentials).
func registerWebTools(r *tools.Registry) {
	httpc := &http.Client{Timeout: 20 * time.Second}

	r.Register(tools.Tool{
		Name:        "web_fetch",
		Description: "Fetch an http(s) URL and return its content as readable text (HTML is stripped; large pages truncated). Use to read docs/APIs/pages. Read-only.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`),
		Safety:      tools.Safety{ReadOnly: true, ParallelSafe: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			return fetchURL(ctx, httpc, p.URL)
		},
	})

	r.Register(tools.Tool{
		Name:        "web_search",
		Description: "Search the web (DuckDuckGo) and return the top results as title + URL + snippet. Keyless + best-effort. Read-only.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
		Safety:      tools.Safety{ReadOnly: true, ParallelSafe: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if strings.TrimSpace(p.Query) == "" {
				return "", fmt.Errorf("web_search: query is required")
			}
			return ddgSearch(ctx, httpc, p.Query)
		},
	})
}

// fetchURL GETs an http(s) URL and returns its text (HTML stripped), refusing link-local/metadata
// hosts.
func fetchURL(ctx context.Context, httpc *http.Client, raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("web_fetch: url must be a valid http(s) URL")
	}
	if isBlockedHost(u.Hostname()) {
		return "", fmt.Errorf("web_fetch: refusing to fetch a link-local / cloud-metadata address (%s)", u.Hostname())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Orion/1.0 (+https://revelara.ai)")
	req.Header.Set("Accept", "text/html,text/plain,application/json;q=0.9,*/*;q=0.8")
	resp, err := httpc.Do(req)
	if err != nil {
		return "", fmt.Errorf("web_fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	text := string(body)
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "html") || strings.Contains(text, "<html") {
		text = stripHTML(text)
	}
	return fmt.Sprintf("%s (%d)\n\n%s", u.String(), resp.StatusCode, boundOutput(text)), nil
}

// isBlockedHost refuses IP-literal link-local addresses (169.254.0.0/16 / fe80::/10 — the cloud
// metadata range) and the GCP metadata hostname. A hostname that RESOLVES to link-local (DNS
// rebinding) is not caught here — a deeper SSRF guard is a follow-on.
func isBlockedHost(host string) bool {
	if strings.EqualFold(host, "metadata.google.internal") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
	}
	return false
}

// ddgSearch queries DuckDuckGo's HTML endpoint and formats the top results.
func ddgSearch(ctx context.Context, httpc *http.Client, query string) (string, error) {
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Orion/1.0; +https://revelara.ai)")
	resp, err := httpc.Do(req)
	if err != nil {
		return "", fmt.Errorf("web_search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	results := ddgParse(string(body))
	if len(results) == 0 {
		return "(no results)", nil
	}
	var b strings.Builder
	for i, r := range results {
		if i >= 8 {
			break
		}
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", r.Snippet)
		}
	}
	return strings.TrimSpace(b.String()), nil
}

type ddgResult struct{ Title, URL, Snippet string }

var (
	ddgResultRe  = regexp.MustCompile(`(?s)class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnippetRe = regexp.MustCompile(`(?s)class="result__snippet"[^>]*>(.*?)</a>`)
	htmlBlockRe  = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	htmlTagRe    = regexp.MustCompile(`(?s)<[^>]+>`)
	wsRe         = regexp.MustCompile(`[ \t]+`)
	blankLineRe  = regexp.MustCompile(`\n[ \t]*\n[ \t\n]*`)
)

// ddgParse extracts search results from DuckDuckGo's HTML (best-effort, tolerant of markup drift).
func ddgParse(body string) []ddgResult {
	links := ddgResultRe.FindAllStringSubmatch(body, -1)
	snips := ddgSnippetRe.FindAllStringSubmatch(body, -1)
	out := make([]ddgResult, 0, len(links))
	for i, m := range links {
		res := ddgResult{Title: stripHTML(m[2]), URL: ddgDecodeURL(m[1])}
		if i < len(snips) {
			res.Snippet = stripHTML(snips[i][1])
		}
		out = append(out, res)
	}
	return out
}

// ddgDecodeURL unwraps DuckDuckGo's redirect (//duckduckgo.com/l/?uddg=<encoded-url>) to the real URL.
func ddgDecodeURL(href string) string {
	if i := strings.Index(href, "uddg="); i >= 0 {
		enc := href[i+len("uddg="):]
		if amp := strings.IndexByte(enc, '&'); amp >= 0 {
			enc = enc[:amp]
		}
		if dec, err := url.QueryUnescape(enc); err == nil && dec != "" {
			return dec
		}
	}
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	return href
}

// stripHTML reduces an HTML document to readable text: scripts/styles removed, tags stripped,
// entities decoded, whitespace collapsed.
func stripHTML(s string) string {
	s = htmlBlockRe.ReplaceAllString(s, " ")
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = wsRe.ReplaceAllString(s, " ")
	s = blankLineRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
