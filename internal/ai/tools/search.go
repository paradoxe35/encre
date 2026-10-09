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

	"golang.org/x/net/html"
)

const (
	duckDuckGoURL = "https://html.duckduckgo.com/html/"
	searchResults = 6
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

var errChallenged = errors.New("DuckDuckGo asked for a check Encre cannot pass - try again in a while")

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
		page, _, err := fetch(client, req)
		if status, ok := errors.AsType[*statusError](err); ok && status.code == http.StatusAccepted {
			return nil, errChallenged
		}
		if err != nil {
			return nil, err
		}
		results := duckDuckGoResults(page)
		if len(results) == 0 && bytes.Contains(page, []byte("anomaly")) {
			return nil, errChallenged
		}
		return results, nil
	}
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
