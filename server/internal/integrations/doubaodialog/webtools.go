package doubaodialog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	WebSearchToolName = "web_search"
	WebFetchToolName  = "web_fetch"

	maxWebToolOutputRunes = 3500
	webHTTPTimeout        = 12 * time.Second
	maxFetchBodyBytes     = 512 << 10 // 512 KiB
)

// WebToolkit runs factual lookup tools for Duplex voice sessions.
type WebToolkit interface {
	Search(ctx context.Context, query string) (string, error)
	Fetch(ctx context.Context, pageURL string) (string, error)
}

// HTTPWebToolkit uses public HTTP endpoints (DuckDuckGo Instant Answer + page GET).
type HTTPWebToolkit struct {
	Client *http.Client
}

func DefaultHTTPWebToolkit() *HTTPWebToolkit {
	return &HTTPWebToolkit{
		Client: &http.Client{Timeout: webHTTPTimeout},
	}
}

func (t *HTTPWebToolkit) client() *http.Client {
	if t != nil && t.Client != nil {
		return t.Client
	}
	return DefaultHTTPWebToolkit().Client
}

func (t *HTTPWebToolkit) Search(ctx context.Context, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("web_search query is required")
	}
	endpoint := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) +
		"&format=json&no_html=1&skip_disambig=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "MulticaDuplexWebTools/1.0")
	resp, err := t.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("web_search request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("web_search status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBodyBytes))
	if err != nil {
		return "", err
	}
	var payload struct {
		Heading        string `json:"Heading"`
		AbstractText   string `json:"AbstractText"`
		AbstractURL    string `json:"AbstractURL"`
		Answer         string `json:"Answer"`
		Definition     string `json:"Definition"`
		RelatedTopics  []any  `json:"RelatedTopics"`
		Results        []any  `json:"Results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("web_search decode: %w", err)
	}

	var parts []string
	if h := strings.TrimSpace(payload.Heading); h != "" {
		parts = append(parts, "标题："+h)
	}
	if a := strings.TrimSpace(payload.Answer); a != "" {
		parts = append(parts, "答案："+stripTags(a))
	}
	if a := strings.TrimSpace(payload.AbstractText); a != "" {
		parts = append(parts, "摘要："+a)
	}
	if d := strings.TrimSpace(payload.Definition); d != "" {
		parts = append(parts, "定义："+d)
	}
	if u := strings.TrimSpace(payload.AbstractURL); u != "" {
		parts = append(parts, "来源："+u)
	}
	for _, item := range append(payload.RelatedTopics, payload.Results...) {
		line := relatedTopicLine(item)
		if line == "" {
			continue
		}
		parts = append(parts, "- "+line)
		if len(parts) >= 8 {
			break
		}
	}
	if len(parts) == 0 {
		return truncateRunes(fmt.Sprintf("未找到关于「%s」的可靠摘要。可改用 web_fetch 打开具体网址。", query), maxWebToolOutputRunes), nil
	}
	out := "搜索「" + query + "」结果：\n" + strings.Join(parts, "\n")
	return truncateRunes(out, maxWebToolOutputRunes), nil
}

func relatedTopicLine(item any) string {
	switch v := item.(type) {
	case map[string]any:
		text, _ := v["Text"].(string)
		firstURL, _ := v["FirstURL"].(string)
		text = strings.TrimSpace(stripTags(text))
		firstURL = strings.TrimSpace(firstURL)
		if text == "" {
			return ""
		}
		if firstURL != "" {
			return text + " (" + firstURL + ")"
		}
		return text
	default:
		return ""
	}
}

func (t *HTTPWebToolkit) Fetch(ctx context.Context, pageURL string) (string, error) {
	pageURL = strings.TrimSpace(pageURL)
	if pageURL == "" {
		return "", fmt.Errorf("web_fetch url is required")
	}
	parsed, err := url.Parse(pageURL)
	if err != nil {
		return "", fmt.Errorf("web_fetch invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("web_fetch only supports http/https")
	}
	if err := rejectPrivateHost(parsed.Hostname()); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "MulticaDuplexWebTools/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.5")

	resp, err := t.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("web_fetch request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("web_fetch status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBodyBytes))
	if err != nil {
		return "", err
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	text := string(body)
	if strings.Contains(ct, "html") || strings.Contains(text, "<html") || strings.Contains(text, "<HTML") {
		text = stripTags(text)
	}
	text = collapseSpace(text)
	if text == "" {
		return "", fmt.Errorf("web_fetch returned empty content")
	}
	out := fmt.Sprintf("已抓取 %s：\n%s", parsed.String(), text)
	return truncateRunes(out, maxWebToolOutputRunes), nil
}

func rejectPrivateHost(host string) error {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "metadata.google.internal" {
		return fmt.Errorf("web_fetch blocked host %q", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("web_fetch blocked address %s", ip)
		}
	}
	return nil
}

var (
	scriptStyleRE = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	htmlTagRE     = regexp.MustCompile(`(?is)<[^>]+>`)
	spaceRE       = regexp.MustCompile(`\s+`)
)

func stripTags(s string) string {
	s = scriptStyleRE.ReplaceAllString(s, " ")
	s = htmlTagRE.ReplaceAllString(s, " ")
	return collapseSpace(s)
}

func collapseSpace(s string) string {
	return strings.TrimSpace(spaceRE.ReplaceAllString(s, " "))
}

func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}

// RecordingWebToolkit is a test double.
type RecordingWebToolkit struct {
	SearchResult string
	FetchResult  string
	SearchErr    error
	FetchErr     error
	Searches     []string
	Fetches      []string
}

func (r *RecordingWebToolkit) Search(_ context.Context, query string) (string, error) {
	r.Searches = append(r.Searches, query)
	if r.SearchErr != nil {
		return "", r.SearchErr
	}
	if strings.TrimSpace(r.SearchResult) != "" {
		return r.SearchResult, nil
	}
	return "搜索结果：" + query, nil
}

func (r *RecordingWebToolkit) Fetch(_ context.Context, pageURL string) (string, error) {
	r.Fetches = append(r.Fetches, pageURL)
	if r.FetchErr != nil {
		return "", r.FetchErr
	}
	if strings.TrimSpace(r.FetchResult) != "" {
		return r.FetchResult, nil
	}
	return "页面内容：" + pageURL, nil
}
