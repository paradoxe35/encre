package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

func wikipediaBase(language string) string {
	return "https://" + language + ".wikipedia.org"
}

var languageCode = regexp.MustCompile(`^[a-z]{2,3}(-[a-z]+)?$`)

type wikipediaArgs struct {
	Query    string `json:"query"`
	Language string `json:"language"`
}

func wikipedia(base func(language string) string) Tool {
	return Tool{
		Tool: aiTool("search_wikipedia",
			"Look up a topic on Wikipedia: the summary of the best matching article, and other matches. Name the article when you use it.",
			object([]string{"query"}, map[string]any{
				"query":    stringParam("What to look up."),
				"language": stringParam("Wikipedia language code, such as en or fr. Defaults to en."),
			})),
		Status: func(args json.RawMessage) string {
			parsed, _ := arguments[wikipediaArgs](args)
			return fmt.Sprintf("Looking up “%s” on Wikipedia", parsed.Query)
		},
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			parsed, err := arguments[wikipediaArgs](args)
			if err != nil {
				return "", err
			}
			language := strings.ToLower(parsed.Language)
			if !languageCode.MatchString(language) {
				language = "en"
			}
			return lookUp(ctx, base(language), parsed.Query)
		},
	}
}

func lookUp(ctx context.Context, site, query string) (string, error) {
	var found struct {
		Pages []struct {
			Key   string `json:"key"`
			Title string `json:"title"`
		} `json:"pages"`
	}
	if err := getJSON(ctx, site+"/w/rest.php/v1/search/page?limit=5&q="+url.QueryEscape(query), &found); err != nil {
		return "", err
	}
	if len(found.Pages) == 0 {
		return "No Wikipedia article matches " + query + ".", nil
	}

	var summary struct {
		Title   string `json:"title"`
		Extract string `json:"extract"`
		URLs    struct {
			Desktop struct {
				Page string `json:"page"`
			} `json:"desktop"`
		} `json:"content_urls"`
	}
	if err := getJSON(ctx, site+"/api/rest_v1/page/summary/"+url.PathEscape(found.Pages[0].Key), &summary); err != nil {
		return "", err
	}

	var text strings.Builder
	fmt.Fprintf(&text, "%s\n%s\n\n%s", summary.Title, summary.URLs.Desktop.Page, summary.Extract)
	if len(found.Pages) > 1 {
		others := make([]string, 0, len(found.Pages)-1)
		for _, page := range found.Pages[1:] {
			others = append(others, page.Title)
		}
		fmt.Fprintf(&text, "\n\nOther matching articles: %s", strings.Join(others, "; "))
	}
	return truncate(text.String()), nil
}
