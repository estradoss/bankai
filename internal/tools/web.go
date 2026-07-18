package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ---------- WebFetch ----------

type WebFetchTool struct{}

func (WebFetchTool) Name() string { return "WebFetch" }

func (WebFetchTool) Description() string {
	return "Fetch a URL over HTTP(S) and return its content as text. HTML is stripped to readable text. Use for reading docs, articles, or API responses. Follows redirects; 15s timeout; truncates very large pages."
}

func (WebFetchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {"type": "string", "description": "Absolute http(s) URL"}
		},
		"required": ["url"]
	}`)
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

func (WebFetchTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	u, err := url.Parse(in.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return Result{IsError: true, Output: "url must be an absolute http(s) URL"}, nil
	}
	body, ctype, err := httpGet(ctx, in.URL)
	if err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	text := body
	if strings.Contains(ctype, "text/html") || looksHTML(body) {
		text = htmlToText(body)
	}
	if len(text) > 100_000 {
		text = text[:100_000] + "\n[truncated]"
	}
	return Result{Output: text}, nil
}

func httpGet(ctx context.Context, u string) (body, ctype string, err error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("user-agent", "Mozilla/5.0 (compatible; bankai/0.1)")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("http %d fetching %s", resp.StatusCode, u)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return "", "", err
	}
	return string(b), resp.Header.Get("content-type"), nil
}

var (
	reScript      = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle       = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reTag         = regexp.MustCompile(`(?s)<[^>]+>`)
	reWS          = regexp.MustCompile(`[ \t]+`)
	reBlankLines  = regexp.MustCompile(`\n{3,}`)
)

func looksHTML(s string) bool {
	head := s
	if len(head) > 512 {
		head = head[:512]
	}
	l := strings.ToLower(head)
	return strings.Contains(l, "<html") || strings.Contains(l, "<!doctype html")
}

func htmlToText(s string) string {
	s = reScript.ReplaceAllString(s, "")
	s = reStyle.ReplaceAllString(s, "")
	s = reTag.ReplaceAllString(s, "")
	s = htmlUnescape(s)
	s = reWS.ReplaceAllString(s, " ")
	s = reBlankLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func htmlUnescape(s string) string {
	repl := strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`,
		"&#39;", "'", "&#x27;", "'", "&nbsp;", " ",
	)
	return repl.Replace(s)
}

// ---------- WebSearch ----------

type WebSearchTool struct{}

func (WebSearchTool) Name() string { return "WebSearch" }

func (WebSearchTool) Description() string {
	return "Search the web and return result titles, URLs, and snippets. Use to find current information, docs, or pages to WebFetch. Returns the top results for the query."
}

func (WebSearchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Search query"}
		},
		"required": ["query"]
	}`)
}

var reDDGResult = regexp.MustCompile(`(?s)<a rel="nofollow" class="result__a" href="([^"]+)">(.*?)</a>.*?<a class="result__snippet"[^>]*>(.*?)</a>`)

func (WebSearchTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if strings.TrimSpace(in.Query) == "" {
		return Result{IsError: true, Output: "query is required"}, nil
	}
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(in.Query)
	body, _, err := httpGet(ctx, endpoint)
	if err != nil {
		return Result{IsError: true, Output: "search failed: " + err.Error()}, nil
	}
	matches := reDDGResult.FindAllStringSubmatch(body, 10)
	if len(matches) == 0 {
		return Result{Output: "No results found."}, nil
	}
	var b strings.Builder
	for i, m := range matches {
		title := strings.TrimSpace(htmlUnescape(reTag.ReplaceAllString(m[2], "")))
		link := decodeDDGLink(m[1])
		snippet := strings.TrimSpace(htmlUnescape(reTag.ReplaceAllString(m[3], "")))
		fmt.Fprintf(&b, "%d. %s\n   %s\n   %s\n", i+1, title, link, snippet)
	}
	return Result{Output: strings.TrimRight(b.String(), "\n")}, nil
}

// DuckDuckGo wraps result URLs like //duckduckgo.com/l/?uddg=<encoded>&...
func decodeDDGLink(raw string) string {
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if v := u.Query().Get("uddg"); v != "" {
		return v
	}
	return raw
}
