package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/html"
)

const (
	duckDuckGoURL = "https://html.duckduckgo.com/html/"
	searchResults = 6
	// DuckDuckGo blocks an address after about eight searches in quick succession, for minutes at a time.
	searchesPerAnswer = 3
	searchSpacing     = 3 * time.Second
	searchesKeptFor   = 10 * time.Minute
	searchesKept      = 64
	blockedFor        = 3 * time.Minute
	// browserAgent is what DuckDuckGo's results page expects; it challenges anything else more often.
	browserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"
)

type searchResult struct {
	title, url, snippet string
}

type engine func(ctx context.Context, query string) ([]searchResult, error)

type webSearchArgs struct {
	Query string `json:"query"`
}

func webSearch(search engine) Tool {
	var made atomic.Int32
	return Tool{
		Tool: aiTool("web_search",
			"Search the web for current or specific information: titles, addresses and snippets of the best results. Name the pages you use in your answer.",
			object([]string{"query"}, map[string]any{
				"query": stringParam("A short search query, as typed into a search engine."),
			})),
		Status: func(args json.RawMessage) string {
			parsed, _ := arguments[webSearchArgs](args)
			return fmt.Sprintf("Searching the web for “%s”", parsed.Query)
		},
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			parsed, err := arguments[webSearchArgs](args)
			if err != nil {
				return "", err
			}
			if made.Add(1) > searchesPerAnswer {
				return "No more web searches for this answer. Answer from what was found, and say what could not be checked.", nil
			}
			results, err := search(ctx, parsed.Query)
			if err != nil {
				return "", err
			}
			return formatResults(parsed.Query, results), nil
		},
	}
}

func formatResults(query string, results []searchResult) string {
	if len(results) == 0 {
		return "No results for " + query + "."
	}
	var text strings.Builder
	for i, result := range results {
		if i == searchResults {
			break
		}
		fmt.Fprintf(&text, "%s\n%s\n%s\n\n", result.title, result.url, result.snippet)
	}
	return truncate(strings.TrimSpace(text.String()))
}

var errChallenged = errors.New("DuckDuckGo asked for a check Encre cannot pass - try again in a few minutes")

// searchGuard is shared by every answer, as DuckDuckGo counts searches per address, not per question.
type searchGuard struct {
	now  func() time.Time
	wait func(ctx context.Context, d time.Duration) error

	mu           sync.Mutex
	next         time.Time
	blockedUntil time.Time
	kept         map[string]keptSearch
}

type keptSearch struct {
	results []searchResult
	at      time.Time
}

var guard = &searchGuard{now: time.Now, wait: sleep}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// guarded reuses recent results, spaces the searches it sends, and waits out a block instead of deepening it.
func (g *searchGuard) guarded(search engine) engine {
	return func(ctx context.Context, query string) ([]searchResult, error) {
		key := strings.ToLower(strings.Join(strings.Fields(query), " "))
		if results, ok := g.recall(key); ok {
			return results, nil
		}
		results, err := g.send(ctx, query, search)
		if err == nil && len(results) > 0 {
			g.keep(key, results)
		}
		return results, err
	}
}

func (g *searchGuard) recall(key string) ([]searchResult, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	kept, ok := g.kept[key]
	return kept.results, ok && g.now().Sub(kept.at) < searchesKeptFor
}

func (g *searchGuard) send(ctx context.Context, query string, search engine) ([]searchResult, error) {
	g.mu.Lock()
	now := g.now()
	if now.Before(g.blockedUntil) {
		g.mu.Unlock()
		return nil, errChallenged
	}
	start := now
	if g.next.After(now) {
		start = g.next
	}
	g.next = start.Add(searchSpacing)
	g.mu.Unlock()

	if err := g.wait(ctx, start.Sub(now)); err != nil {
		return nil, err
	}
	results, err := search(ctx, query)
	if errors.Is(err, errChallenged) {
		g.mu.Lock()
		g.blockedUntil = g.now().Add(blockedFor)
		g.mu.Unlock()
	}
	return results, err
}

func (g *searchGuard) keep(key string, results []searchResult) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.kept == nil {
		g.kept = make(map[string]keptSearch)
	}
	if len(g.kept) >= searchesKept {
		oldest := ""
		for k, kept := range g.kept {
			if oldest == "" || kept.at.Before(g.kept[oldest].at) {
				oldest = k
			}
		}
		delete(g.kept, oldest)
	}
	g.kept[key] = keptSearch{results: results, at: g.now()}
}

// duckDuckGo posts its query as DuckDuckGo's own form does: a GET is challenged far more often.
func duckDuckGo(endpoint string) engine {
	return func(ctx context.Context, query string) ([]searchResult, error) {
		form := url.Values{"q": {query}, "kl": {"wt-wt"}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", browserAgent)
		req.Header.Set("Referer", "https://html.duckduckgo.com/html/")
		page, _, err := fetch(client, req)
		if status, ok := errors.AsType[*statusError](err); ok && status.code == http.StatusAccepted {
			return nil, errChallenged
		}
		if err != nil {
			return nil, err
		}
		return resultsOrChallenge(page)
	}
}

// resultsOrChallenge tells DuckDuckGo's check apart from a page that found nothing.
func resultsOrChallenge(page []byte) ([]searchResult, error) {
	results := duckDuckGoResults(page)
	if len(results) == 0 && bytes.Contains(page, []byte("anomaly")) {
		return nil, errChallenged
	}
	return results, nil
}

func duckDuckGoResults(page []byte) []searchResult {
	root, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		return nil
	}
	var results []searchResult
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" {
			switch class := attribute(node, "class"); {
			case strings.Contains(class, "result__a"):
				if link := resultLink(attribute(node, "href")); link != "" {
					results = append(results, searchResult{title: nodeText(node), url: link})
				}
			case strings.Contains(class, "result__snippet") && len(results) > 0:
				results[len(results)-1].snippet = nodeText(node)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return results
}

// resultLink unwraps DuckDuckGo's redirect; ads lead nowhere.
func resultLink(href string) string {
	link, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if strings.HasSuffix(link.Host, "duckduckgo.com") {
		if strings.HasPrefix(link.Path, "/y.js") {
			return ""
		}
		return link.Query().Get("uddg")
	}
	if link.Scheme == "" {
		return ""
	}
	return link.String()
}

func attribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func nodeText(node *html.Node) string {
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			text.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(text.String()), " ")
}
