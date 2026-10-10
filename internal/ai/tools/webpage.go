package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// Checked once resolved, so a page cannot steer the model into reading a router or local service.
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

// renderingReader loads a page in a browser and returns its text; it is free without a key, at 20 pages a minute.
const renderingReader = "https://r.jina.ai/"

// minPageText is less than any real article has, so a page this bare is waiting on its scripts.
const minPageText = 200

var errBarePage = errors.New("the page has almost no text before its scripts run")

type unreadableError struct{ contentType string }

func (e *unreadableError) Error() string {
	return "the page is " + e.contentType + ", which cannot be read as text"
}

type webPageArgs struct {
	URL string `json:"url"`
}

func webPage(httpClient *http.Client, renderer string) Tool {
	return Tool{
		Tool: aiTool("read_web_page",
			"Read the text of a web page or an online PDF, given its full http or https address.",
			object([]string{"url"}, map[string]any{
				"url": stringParam("The page's or PDF's full address, starting with http:// or https://."),
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
			return read(ctx, httpClient, renderer, parsed.URL)
		},
	}
}

// read stays on this computer unless a browser would get what a plain download could not.
func read(ctx context.Context, httpClient *http.Client, renderer, address string) (string, error) {
	page, err := url.Parse(address)
	if err != nil || (page.Scheme != "http" && page.Scheme != "https") || page.Host == "" {
		return "", fmt.Errorf("%q is not an http or https address", address)
	}

	text, err := readDirectly(ctx, httpClient, page)
	if !renderingHelps(err) {
		return text, err
	}
	if rendered, renderErr := readRendered(ctx, httpClient, renderer, page); renderErr == nil {
		return rendered, nil
	}
	if errors.Is(err, errBarePage) {
		return text, nil
	}
	return "", err
}

func readDirectly(ctx context.Context, httpClient *http.Client, page *url.URL) (string, error) {
	body, contentType, err := get(ctx, httpClient, page.String())
	if err != nil {
		return "", err
	}

	switch {
	case strings.Contains(contentType, "html"):
		title, text := pageText(decoded(body, contentType))
		result := truncate(title + "\n" + page.String() + "\n\n" + text)
		if utf8.RuneCountInString(text) < minPageText {
			return result, errBarePage
		}
		return result, nil
	case strings.HasPrefix(contentType, "text/"), strings.Contains(contentType, "json"):
		return truncate(string(decoded(body, contentType))), nil
	}
	return "", &unreadableError{contentType: contentType}
}

// renderingHelps with text that scripts build, a wall against plain downloads, and PDFs.
func renderingHelps(err error) bool {
	var status *statusError
	var unreadable *unreadableError
	switch {
	case errors.Is(err, errBarePage):
		return true
	case errors.As(err, &status):
		return status.code == http.StatusForbidden || status.code == http.StatusTooManyRequests ||
			status.code == http.StatusServiceUnavailable
	case errors.As(err, &unreadable):
		return strings.Contains(unreadable.contentType, "pdf")
	}
	return false
}

func readRendered(ctx context.Context, httpClient *http.Client, renderer string, page *url.URL) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, renderer+page.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("DNT", "1")
	req.Header.Set("X-Retain-Images", "none")
	body, _, err := fetch(httpClient, req)
	if err != nil {
		return "", err
	}
	return truncate(string(body)), nil
}

// decoded is the body in UTF-8, whatever character set the page declares.
func decoded(body []byte, contentType string) []byte {
	reader, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return body
	}
	text, err := io.ReadAll(reader)
	if err != nil {
		return body
	}
	return text
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
	walk(mainContent(root))

	lines := strings.Split(text.String(), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return pageTitle(root), strings.TrimSpace(blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// mainContent leaves out banners and side panels when the page marks its content; several articles make a listing.
func mainContent(root *html.Node) *html.Node {
	if main := elements(root, "main"); len(main) > 0 {
		return main[0]
	}
	if articles := elements(root, "article"); len(articles) == 1 {
		return articles[0]
	}
	return root
}

func elements(node *html.Node, name string) []*html.Node {
	if node.Type == html.ElementNode && node.Data == name {
		return []*html.Node{node}
	}
	var found []*html.Node
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		found = append(found, elements(child, name)...)
	}
	return found
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
