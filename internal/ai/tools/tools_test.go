package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(body, "<") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func run(t *testing.T, tool Tool, args string) string {
	t.Helper()
	result, err := tool.Run(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s: %v", tool.Name, err)
	}
	return result
}

func expect(t *testing.T, result string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(result, fragment) {
			t.Errorf("result lacks %q:\n%s", fragment, result)
		}
	}
}

func TestAskIsOfferedEveryTool(t *testing.T) {
	var names []string
	for _, tool := range Set() {
		names = append(names, tool.Name)
	}
	if got := strings.Join(names, ","); got != "web_search,read_web_page,search_wikipedia,get_weather" {
		t.Errorf("the tools are %s", got)
	}
}

func TestWikipediaSummarisesTheBestMatch(t *testing.T) {
	server := serve(t, map[string]string{
		"/w/rest.php/v1/search/page":                          `{"pages":[{"key":"Go_(programming_language)","title":"Go (programming language)"},{"key":"Go_(game)","title":"Go (game)"}]}`,
		"/api/rest_v1/page/summary/Go_(programming_language)": `{"title":"Go (programming language)","extract":"Go is a statically typed language.","content_urls":{"desktop":{"page":"https://en.wikipedia.org/wiki/Go"}}}`,
	})
	var language string
	tool := wikipedia(func(lang string) string { language = lang; return server.URL })

	result := run(t, tool, `{"query":"golang","language":"not a code"}`)
	expect(t, result, "Go (programming language)", "https://en.wikipedia.org/wiki/Go", "statically typed", "Other matching articles: Go (game)")
	if language != "en" {
		t.Errorf("an invalid language went to %q, want en", language)
	}
	if status := tool.Status(json.RawMessage(`{"query":"golang"}`)); status != "Looking up “golang” on Wikipedia" {
		t.Errorf("status %q", status)
	}
}

func TestWeatherFindsThePlaceThenItsForecast(t *testing.T) {
	server := serve(t, map[string]string{
		"/geocode":  `{"results":[{"name":"Kinshasa","admin1":"Kinshasa","country":"DR Congo","latitude":-4.3,"longitude":15.3}]}`,
		"/forecast": `{"timezone":"Africa/Kinshasa","current":{"time":"2026-10-09T10:00","temperature_2m":27.4,"apparent_temperature":30.1,"relative_humidity_2m":78,"wind_speed_10m":9,"weather_code":2},"daily":{"time":["2026-10-09"],"weather_code":[61],"temperature_2m_max":[31],"temperature_2m_min":[22],"precipitation_probability_max":[60]}}`,
	})
	result := run(t, weather(server.URL+"/geocode", server.URL+"/forecast"), `{"place":"Kinshasa, DR Congo"}`)
	expect(t, result, "Kinshasa, Kinshasa, DR Congo", "partly cloudy, 27°C (feels like 30°C)", "2026-10-09: light rain, 22°C to 31°C, 60% chance of rain")
}

// renderer stands in for the rendering reader, answering with text, or failing when there is none.
func renderer(t *testing.T, text string) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var asked []*http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r)
		if text == "" {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(text))
	}))
	t.Cleanup(server.Close)
	return server, &asked
}

var article = strings.Repeat("Exports are twice as fast in this release. ", 8)

func TestAWebPageIsReadWithoutItsScriptsAndNavigation(t *testing.T) {
	server := serve(t, map[string]string{"/article": `<html><head><title>Release notes</title><script>track()</script></head>
		<body><nav>Home | Blog</nav><h1>Version 2</h1><p>` + article + `</p><ul><li>New dashboard</li></ul><footer>© 2026</footer></body></html>`})
	reader, asked := renderer(t, "rendered")

	result := run(t, webPage(http.DefaultClient, reader.URL+"/"), `{"url":"`+server.URL+`/article"}`)
	expect(t, result, "Release notes", "Version 2", "Exports are twice as fast in this release.", "- New dashboard")
	for _, unwanted := range []string{"track()", "Home | Blog", "© 2026"} {
		if strings.Contains(result, unwanted) {
			t.Errorf("the page text kept %q:\n%s", unwanted, result)
		}
	}
	if len(*asked) != 0 {
		t.Error("a page read here was also sent to the rendering reader")
	}
}

func TestOnlyTheMainContentIsReadWhenThePageMarksIt(t *testing.T) {
	server := serve(t, map[string]string{
		"/main":    `<html><body><div>Accept all cookies</div><main><p>` + article + `</p></main><div>Related stories</div></body></html>`,
		"/article": `<html><body><div>Accept all cookies</div><article><p>` + article + `</p></article></body></html>`,
		"/listing": `<html><body><h1>Latest</h1><article><p>` + article + `</p></article><article><p>Second story.</p></article></body></html>`,
	})
	reader, _ := renderer(t, "")
	read := webPage(http.DefaultClient, reader.URL+"/")

	for _, path := range []string{"/main", "/article"} {
		result := run(t, read, `{"url":"`+server.URL+path+`"}`)
		expect(t, result, "Exports are twice as fast")
		if strings.Contains(result, "cookies") || strings.Contains(result, "Related") {
			t.Errorf("%s kept what surrounds its content:\n%s", path, result)
		}
	}
	expect(t, run(t, read, `{"url":"`+server.URL+`/listing"}`), "Latest", "Second story.")
}

func TestAPageInAnotherCharacterSetReadsCorrectly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=windows-1252")
		w.Write([]byte("<p>Caf\xe9 cr\xe8me, " + article + "</p>"))
	}))
	t.Cleanup(server.Close)
	reader, _ := renderer(t, "")

	expect(t, run(t, webPage(http.DefaultClient, reader.URL+"/"), `{"url":"`+server.URL+`"}`), "Café crème")
}

func TestAPageItsScriptsBuildIsReadThroughTheRenderingReader(t *testing.T) {
	server := serve(t, map[string]string{"/app": `<html><head><title>App</title></head><body><div id="root"></div><script>render()</script></body></html>`})
	reader, asked := renderer(t, "Title: App\n\nMarkdown Content:\nThe rendered dashboard.")

	result := run(t, webPage(http.DefaultClient, reader.URL+"/"), `{"url":"`+server.URL+`/app"}`)
	expect(t, result, "The rendered dashboard.")
	if len(*asked) != 1 || (*asked)[0].URL.Path != "/"+server.URL+"/app" || (*asked)[0].Header.Get("DNT") != "1" {
		t.Fatalf("the rendering reader was asked %d times, last for %v", len(*asked), *asked)
	}
}

func TestABarePageIsKeptWhenTheRenderingReaderFails(t *testing.T) {
	server := serve(t, map[string]string{"/short": `<html><head><title>Short</title></head><body><p>Just a line.</p></body></html>`})
	reader, _ := renderer(t, "")

	expect(t, run(t, webPage(http.DefaultClient, reader.URL+"/"), `{"url":"`+server.URL+`/short"}`), "Short", "Just a line.")
}

func TestOnlyABlockedOrPDFPageGoesToTheRenderingReader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/blocked":
			http.Error(w, "challenge", http.StatusForbidden)
		case "/paper.pdf":
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte("%PDF-1.7"))
		case "/photo.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("png"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	reader, asked := renderer(t, "Rendered through the reader.")
	read := webPage(http.DefaultClient, reader.URL+"/")

	for _, path := range []string{"/blocked", "/paper.pdf"} {
		expect(t, run(t, read, `{"url":"`+server.URL+path+`"}`), "Rendered through the reader.")
	}
	for _, path := range []string{"/missing", "/photo.png"} {
		if _, err := read.Run(context.Background(), json.RawMessage(`{"url":"`+server.URL+path+`"}`)); err == nil {
			t.Errorf("%s was read", path)
		}
	}
	if len(*asked) != 2 {
		t.Fatalf("the rendering reader was asked %d times, want only for the blocked page and the PDF", len(*asked))
	}
}

func TestPagesOnThisComputerAreRefused(t *testing.T) {
	server := serve(t, map[string]string{"/admin": "<p>secret</p>"})
	reader, asked := renderer(t, "rendered")
	read := webPage(guardedClient, reader.URL+"/")

	_, err := read.Run(context.Background(), json.RawMessage(`{"url":"`+server.URL+`/admin"}`))
	if err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Fatalf("a page on this computer was read, or refused unclearly: %v", err)
	}
	if _, err := read.Run(context.Background(), json.RawMessage(`{"url":"file:///etc/passwd"}`)); err == nil {
		t.Fatal("a file address was read")
	}
	if len(*asked) != 0 {
		t.Fatal("a page on this computer was sent to the rendering reader")
	}
}

const duckDuckGoPage = `<div class="result results_links web-result result--ad">
	<a class="result__a" href="https://duckduckgo.com/y.js?ad_domain=shop.example">Buy Go now</a>
	<a class="result__snippet" href="https://duckduckgo.com/y.js?ad_domain=shop.example">An advert.</a></div>
<div class="result results_links web-result">
	<h2 class="result__title"><a rel="nofollow" class="result__a" href="https://go.dev/doc/devel/release">Release History - The <b>Go</b> Programming Language</a></h2>
	<a class="result__snippet" href="https://go.dev/doc/devel/release"><b>Go</b> 1.26 is the latest release.</a></div>
<div class="result results_links web-result">
	<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fblog%2F&amp;rut=abc">The Go Blog</a>
	<a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fblog%2F">News from the Go team.</a></div>`

func TestDuckDuckGoResultsLeaveOutAdsAndUnwrapRedirects(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		query = r.PostForm.Get("q")
		w.Write([]byte(duckDuckGoPage))
	}))
	defer server.Close()

	result := run(t, webSearch(duckDuckGo(server.URL)), `{"query":"go release"}`)
	if query != "go release" {
		t.Errorf("searched for %q", query)
	}
	expect(t, result,
		"Release History - The Go Programming Language\nhttps://go.dev/doc/devel/release\nGo 1.26 is the latest release.",
		"The Go Blog\nhttps://go.dev/blog/\nNews from the Go team.")
	if strings.Contains(result, "advert") || strings.Contains(result, "y.js") {
		t.Errorf("an ad was kept:\n%s", result)
	}
}

func TestADuckDuckGoChallengeIsExplained(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"accepted": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) },
		"anomaly":  func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`<div class="anomaly-modal">`)) },
	} {
		server := httptest.NewServer(handler)
		_, err := webSearch(duckDuckGo(server.URL)).Run(context.Background(), json.RawMessage(`{"query":"go"}`))
		server.Close()
		if !errors.Is(err, errChallenged) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestWeatherKeepsTheRegionAfterTheComma(t *testing.T) {
	server := serve(t, map[string]string{
		"/geocode":  `{"results":[{"name":"Paris","country":"France","country_code":"FR","latitude":48.8,"longitude":2.3},{"name":"Paris","admin1":"Texas","country":"United States","country_code":"US","latitude":33.6,"longitude":-95.5}]}`,
		"/forecast": `{"timezone":"America/Chicago","current":{"weather_code":0},"daily":{}}`,
	})
	expect(t, run(t, weather(server.URL+"/geocode", server.URL+"/forecast"), `{"place":"Paris, Texas"}`), "Paris, Texas, United States")
	expect(t, run(t, weather(server.URL+"/geocode", server.URL+"/forecast"), `{"place":"Paris"}`), "Paris, France")
}

func TestLocalAddressesAreRecognised(t *testing.T) {
	for address, local := range map[string]bool{
		"127.0.0.1:80":               true,
		"192.168.1.1:80":             true,
		"10.0.0.5:443":               true,
		"100.100.100.100:80":         true,
		"169.254.169.254:80":         true,
		"0.1.2.3:80":                 true,
		"198.18.0.1:80":              true,
		"[::1]:80":                   true,
		"[fd00::1]:80":               true,
		"[::ffff:192.168.1.1]:80":    true,
		"[64:ff9b::c0a8:101]:80":     true,
		"[64:ff9b::808:808]:80":      false,
		"8.8.8.8:443":                false,
		"[2606:4700:4700::1111]:443": false,
	} {
		if got := isLocal(address); got != local {
			t.Errorf("%s: local %v, want %v", address, got, local)
		}
	}
}
