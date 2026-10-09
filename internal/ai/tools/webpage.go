package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/html"
)

// guardedClient refuses addresses on the user's own machine and network, checked once resolved, so
// a page cannot steer the model into reading a router or a local service.
var guardedClient = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		DialContext: (&net.Dialer{Timeout: 10 * time.Second, Control: refuseLocal}).DialContext,
	},
}

// localRanges are local addresses netip does not already know as private, loopback or link-local.
var localRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("64:ff9b::/96"),
}

func refuseLocal(_, address string, _ syscall.RawConn) error {
	if isLocal(address) {
		return errors.New("pages on this computer or its local network cannot be read")
	}
	return nil
}

func isLocal(address string) bool {
	hostPort, err := netip.ParseAddrPort(address)
	if err != nil {
		return true
	}
	ip := hostPort.Addr().Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, local := range localRanges {
		if local.Contains(ip) {
			// NAT64 carries an IPv4 address in its last four bytes, which may itself be local.
			if local.Bits() == 96 {
				embedded := ip.As16()
				return isLocal(netip.AddrPortFrom(netip.AddrFrom4([4]byte(embedded[12:])), 0).String())
			}
			return true
		}
	}
	return false
}

type webPageArgs struct {
	URL string `json:"url"`
}

func webPage(httpClient *http.Client) Tool {
	return Tool{
		Tool: aiTool("read_web_page",
			"Read the text of a web page, given its full http or https address.",
			object([]string{"url"}, map[string]any{
				"url": stringParam("The page's full address, starting with http:// or https://."),
			})),
		Status: func(args json.RawMessage) string {
			parsed, _ := arguments[webPageArgs](args)
			if page, err := url.Parse(parsed.URL); err == nil && page.Host != "" {
				return "Reading " + strings.TrimPrefix(page.Host, "www.")
			}
			return "Reading a web page"
		},
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			parsed, err := arguments[webPageArgs](args)
			if err != nil {
				return "", err
			}
			return read(ctx, httpClient, parsed.URL)
		},
	}
}

func read(ctx context.Context, httpClient *http.Client, address string) (string, error) {
	page, err := url.Parse(address)
	if err != nil || (page.Scheme != "http" && page.Scheme != "https") || page.Host == "" {
		return "", fmt.Errorf("%q is not an http or https address", address)
	}
	body, contentType, err := get(ctx, httpClient, page.String())
	if err != nil {
		return "", err
	}

	switch {
	case strings.Contains(contentType, "html"):
		title, text := pageText(body)
		return truncate(title + "\n" + page.String() + "\n\n" + text), nil
	case strings.HasPrefix(contentType, "text/"), strings.Contains(contentType, "json"):
		return truncate(string(body)), nil
	}
	return "", fmt.Errorf("the page is %s, which cannot be read as text", contentType)
}

var (
	skipped = map[string]bool{
		"script": true, "style": true, "noscript": true, "template": true, "svg": true,
		"nav": true, "footer": true, "aside": true, "form": true, "iframe": true, "head": true,
	}
	blocks = map[string]bool{
		"p": true, "div": true, "br": true, "li": true, "tr": true, "section": true, "article": true,
		"header": true, "main": true, "blockquote": true, "pre": true, "table": true, "ul": true, "ol": true,
		"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	}
	blankLines = regexp.MustCompile(`\n{3,}`)
	spaces     = regexp.MustCompile(`[ \t\r\f\v]+`)
)

func pageText(body []byte) (string, string) {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", string(body)
	}

	var text strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			if skipped[node.Data] {
				return
			}
			if blocks[node.Data] {
				text.WriteString("\n")
			}
			if node.Data == "li" {
				text.WriteString("- ")
			}
		}
		if node.Type == html.TextNode {
			text.WriteString(spaces.ReplaceAllString(node.Data, " "))
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if node.Type == html.ElementNode && blocks[node.Data] {
			text.WriteString("\n")
		}
	}
	walk(root)

	lines := strings.Split(text.String(), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return pageTitle(root), strings.TrimSpace(blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

func pageTitle(node *html.Node) string {
	if node.Type == html.ElementNode && node.Data == "title" && node.FirstChild != nil {
		return strings.TrimSpace(node.FirstChild.Data)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if title := pageTitle(child); title != "" {
			return title
		}
	}
	return ""
}
