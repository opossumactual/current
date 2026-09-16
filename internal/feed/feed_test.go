package feed

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"

	"current/internal/store"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseRSS(t *testing.T) {
	pf, err := Parse(fixture(t, "rss.xml"), "https://example.com/rss.xml")
	if err != nil {
		t.Fatal(err)
	}
	if pf.Title != "Example RSS" || pf.SiteURL != "https://example.com/" || pf.Description != "An example feed" {
		t.Fatalf("meta: %+v", pf)
	}
	if len(pf.Items) != 2 {
		t.Fatalf("items = %d", len(pf.Items))
	}
	a := pf.Items[0]
	if a.GUID != "post-1" || a.Title != "First post" || a.Author != "Alice" || a.URL != "https://example.com/first" {
		t.Fatalf("item0: %+v", a)
	}
	if !a.PublishedAt.Equal(time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("date: %v", a.PublishedAt)
	}
	if !strings.Contains(a.Content, "<b>content</b>") || strings.Contains(a.Content, "<script") {
		t.Fatalf("content not sanitized: %q", a.Content)
	}
	if !strings.Contains(a.Content, `src="https://example.com/img.png"`) {
		t.Fatalf("relative img not resolved: %q", a.Content)
	}
	if a.Summary != "Full content here." {
		t.Fatalf("summary = %q", a.Summary)
	}
	b := pf.Items[1]
	if b.GUID != "https://example.com/second" {
		t.Fatalf("guid fallback to link: %q", b.GUID)
	}
	if strings.Contains(b.Content, "javascript:") {
		t.Fatalf("js href survived: %q", b.Content)
	}
	if !strings.Contains(b.Content, "Only a description") {
		t.Fatalf("description fallback: %q", b.Content)
	}
	if !b.PublishedAt.IsZero() {
		t.Fatalf("missing date should be zero, got %v", b.PublishedAt)
	}
}

func TestParseAtomAndJSON(t *testing.T) {
	pf, err := Parse(fixture(t, "atom.xml"), "https://atom.example/feed.xml")
	if err != nil || pf.Title != "Example Atom" || len(pf.Items) != 1 || pf.Items[0].Author != "Bob" || pf.Items[0].GUID != "urn:uuid:1" {
		t.Fatalf("atom: %+v %v", pf, err)
	}
	if pf.SiteURL != "https://atom.example/" {
		t.Fatalf("atom site url = %q", pf.SiteURL)
	}
	pj, err := Parse(fixture(t, "feed.json"), "https://json.example/feed.json")
	if err != nil || pj.Title != "Example JSON" || len(pj.Items) != 1 || pj.Items[0].Content != "<p>json body</p>" {
		t.Fatalf("json: %+v %v", pj, err)
	}
	if _, err := Parse([]byte("<html><body>nope</body></html>"), "https://x/"); err == nil {
		t.Fatalf("html should not parse as feed")
	}
}

func TestGUIDFallbackHash(t *testing.T) {
	pf, _ := Parse([]byte(`<rss version="2.0"><channel><title>t</title><item><title>A</title></item><item><title>B</title></item></channel></rss>`), "https://x/")
	if len(pf.Items) != 2 || pf.Items[0].GUID == "" || pf.Items[0].GUID == pf.Items[1].GUID {
		t.Fatalf("hash guids: %+v", pf.Items)
	}
}

func TestSanitize(t *testing.T) {
	out := Sanitize(`<p onclick="x">a</p><img src="https://x/i.png"><iframe src="https://www.youtube.com/embed/abc"></iframe><iframe src="https://evil/"></iframe><style>x</style>`, "")
	if strings.Contains(out, "onclick") || strings.Contains(out, "evil") || strings.Contains(out, "<style") {
		t.Fatalf("bad: %q", out)
	}
	if !strings.Contains(out, `loading="lazy"`) || !strings.Contains(out, "youtube.com/embed/abc") {
		t.Fatalf("missing lazy or youtube: %q", out)
	}
}

func TestFetchConditional(t *testing.T) {
	var gotIfNone, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIfNone = r.Header.Get("If-None-Match")
		gotUA = r.Header.Get("User-Agent")
		if gotIfNone == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Mon, 07 Sep 2026 10:00:00 GMT")
		w.Write(fixture(t, "rss.xml"))
	}))
	defer srv.Close()
	res, err := Fetch(context.Background(), srv.Client(), srv.URL, "", "")
	if err != nil || res.NotModified || len(res.Body) == 0 || res.ETag != `"v1"` || res.LastModified == "" {
		t.Fatalf("first fetch: %+v %v", res, err)
	}
	if !strings.HasPrefix(gotUA, "Current/") {
		t.Fatalf("UA = %q", gotUA)
	}
	res, err = Fetch(context.Background(), srv.Client(), srv.URL, `"v1"`, res.LastModified)
	if err != nil || !res.NotModified {
		t.Fatalf("second fetch: %+v %v", res, err)
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv2.Close()
	if _, err := Fetch(context.Background(), srv2.Client(), srv2.URL, "", ""); err == nil {
		t.Fatalf("500 should error")
	}
}

func TestDiscover(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write(fixture(t, "page.html"))
	})
	mux.HandleFunc("/rss.xml", func(w http.ResponseWriter, r *http.Request) { w.Write(fixture(t, "rss.xml")) })
	mux.HandleFunc("/bare/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html><body>nothing</body></html>")) })
	mux.HandleFunc("/nope", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	mux.HandleFunc("/bare/feed", func(w http.ResponseWriter, r *http.Request) { w.Write(fixture(t, "atom.xml")) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := Discover(context.Background(), srv.Client(), srv.URL+"/rss.xml")
	if err != nil || len(c) != 1 || c[0].URL != srv.URL+"/rss.xml" || c[0].Title != "Example RSS" {
		t.Fatalf("direct: %+v %v", c, err)
	}
	c, err = Discover(context.Background(), srv.Client(), srv.URL+"/")
	if err != nil || len(c) != 2 || c[0].URL != srv.URL+"/rss.xml" || c[0].Title != "Site RSS" || c[1].URL != "https://other.example/atom.xml" {
		t.Fatalf("link tags: %+v %v", c, err)
	}
	c, err = Discover(context.Background(), srv.Client(), srv.URL+"/bare/")
	if err != nil || len(c) != 1 || c[0].URL != srv.URL+"/bare/feed" {
		t.Fatalf("common paths: %+v %v", c, err)
	}
	if _, err := Discover(context.Background(), srv.Client(), srv.URL+"/nope"); err == nil {
		t.Fatalf("404 should error")
	}
}

func TestParseMediaFallback(t *testing.T) {
	pf, err := Parse(fixture(t, "youtube.xml"), "https://www.youtube.com/feeds/videos.xml?channel_id=UCabc")
	if err != nil {
		t.Fatal(err)
	}
	if len(pf.Items) != 2 {
		t.Fatalf("items = %d", len(pf.Items))
	}
	a := pf.Items[0]
	if !strings.Contains(a.Content, `<iframe src="https://www.youtube-nocookie.com/embed/EIhAelYTna8"`) {
		t.Fatalf("no youtube embed: %q", a.Content)
	}
	if !strings.Contains(a.Content, "First line &lt;b&gt;not html&lt;/b&gt;<br/>Second line") {
		t.Fatalf("description not escaped/paragraphed: %q", a.Content)
	}
	if !strings.HasPrefix(a.Summary, "First line <b>not html</b> Second line") {
		t.Fatalf("summary = %q", a.Summary)
	}
	b := pf.Items[1]
	if !strings.Contains(b.Content, "<iframe") || strings.Contains(b.Content, "<p></p>") {
		t.Fatalf("item without description: %q", b.Content)
	}
	// Non-YouTube link with a thumbnail gets the image instead.
	if got := mediaFallback(&gofeed.Item{Image: &gofeed.Image{URL: "https://x.test/t.jpg"}}, "https://x.test/post"); got != `<p><img src="https://x.test/t.jpg" alt=""></p>` {
		t.Fatalf("thumbnail fallback = %q", got)
	}
}

func TestRetryAfter(t *testing.T) {
	h := func(kv ...string) http.Header {
		hd := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			hd.Set(kv[i], kv[i+1])
		}
		return hd
	}
	cases := []struct {
		name   string
		hdr    http.Header
		status int
		want   time.Duration
	}{
		{"plain 200", h(), 200, 0},
		{"429 no headers", h(), 429, DefaultRetryAfter},
		{"503 no headers", h(), 503, DefaultRetryAfter},
		{"503 retry-after", h("Retry-After", "120"), 503, 2 * time.Minute},
		{"429 retry-after seconds", h("Retry-After", "7"), 429, 7 * time.Second},
		{"429 ratelimit reset", h("X-RateLimit-Reset", "29"), 429, 29 * time.Second},
		{"200 budget exhausted", h("X-RateLimit-Remaining", "0.0", "X-RateLimit-Reset", "29"), 200, 29 * time.Second},
		{"200 budget left", h("X-RateLimit-Remaining", "5", "X-RateLimit-Reset", "29"), 200, 0},
	}
	for _, c := range cases {
		if got := retryAfter(c.hdr, c.status); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestFetchErrorCarriesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	res, err := Fetch(context.Background(), srv.Client(), srv.URL, "", "")
	if err == nil || res == nil || res.RetryAfter != 3*time.Second {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestUndatedItemUsesChannelDate(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>T</title><link>https://t.test/</link>
<pubDate>Tue, 18 Apr 2023 21:25:59 GMT</pubDate>
<item><title>old</title><link>https://t.test/old</link></item>
<item><title>new</title><link>https://t.test/new</link><pubDate>Mon, 07 Sep 2026 10:00:00 GMT</pubDate></item>
</channel></rss>`)
	pf, err := Parse(body, "https://t.test/rss")
	if err != nil {
		t.Fatal(err)
	}
	if got := pf.Items[0].PublishedAt; !got.Equal(time.Date(2023, 4, 18, 21, 25, 59, 0, time.UTC)) {
		t.Fatalf("undated item date = %v", got)
	}
	if got := pf.Items[1].PublishedAt; !got.Equal(time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("dated item date = %v", got)
	}
}

func TestFetchCurl(t *testing.T) {
	if CurlPath == "" {
		t.Skip("curl not installed")
	}
	body := fixture(t, "rss.xml")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("User-Agent"), "Current/") {
			t.Errorf("curl did not send our User-Agent: %q", r.Header.Get("User-Agent"))
		}
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/feed", http.StatusFound)
		case "/forbidden":
			w.Header().Set("Retry-After", "9")
			w.WriteHeader(403)
		default:
			if r.Header.Get("If-None-Match") == `"c1"` {
				w.WriteHeader(304)
				return
			}
			w.Header().Set("ETag", `"c1"`)
			w.Header().Set("Last-Modified", "Tue, 08 Sep 2026 00:00:00 GMT")
			w.Header().Set("Content-Type", "application/rss+xml")
			w.Write(body)
		}
	}))
	defer srv.Close()
	res, err := fetchCurl(context.Background(), srv.URL+"/redirect", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.NotModified || !bytes.Equal(res.Body, body) || res.ETag != `"c1"` || res.LastModified == "" || res.ContentType != "application/rss+xml" || res.FinalURL != srv.URL+"/feed" {
		t.Fatalf("curl result: %+v", res)
	}
	res, err = fetchCurl(context.Background(), srv.URL+"/feed", `"c1"`, res.LastModified)
	if err != nil || !res.NotModified || res.ETag != `"c1"` {
		t.Fatalf("conditional: %+v %v", res, err)
	}
	res, err = fetchCurl(context.Background(), srv.URL+"/forbidden", "", "")
	if err == nil || res == nil || res.RetryAfter != 9*time.Second {
		t.Fatalf("403 via curl: %+v %v", res, err)
	}
}

// A 403 from the Go client is retried once through curl.
func TestFetchFallsBackToCurlOn403(t *testing.T) {
	if CurlPath == "" {
		t.Skip("curl not installed")
	}
	body := fixture(t, "rss.xml")
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(403)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	res, err := Fetch(context.Background(), srv.Client(), srv.URL, "", "")
	if err != nil || !bytes.Equal(res.Body, body) || hits != 2 {
		t.Fatalf("fallback: err=%v hits=%d", err, hits)
	}
	// A 403 from both paths stays an error.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer srv2.Close()
	if _, err := Fetch(context.Background(), srv2.Client(), srv2.URL, "", ""); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("double 403 should error, got %v", err)
	}
}

func TestParseMediaImages(t *testing.T) {
	pf, err := Parse(fixture(t, "media.xml"), "https://media.test/rss")
	if err != nil {
		t.Fatal(err)
	}
	byGUID := map[string]store.Article{}
	for _, a := range pf.Items {
		byGUID[a.GUID] = a
	}
	if got := byGUID["bbc"].ImageURL; got != "https://media.test/large.jpg" {
		t.Fatalf("largest media:thumbnail should win, got %q", got)
	}
	if strings.Contains(byGUID["bbc"].Content, "<img") {
		t.Fatalf("thumbnail must not be injected into the body: %q", byGUID["bbc"].Content)
	}
	pod := byGUID["pod"]
	if pod.ImageURL != "https://media.test/cover.jpg" {
		t.Fatalf("image enclosure: %q", pod.ImageURL)
	}
	if !strings.Contains(pod.Content, `<audio controls="" preload="none" src="https://media.test/ep1.mp3">`) {
		t.Fatalf("audio enclosure not rendered: %q", pod.Content)
	}
	if pod.Summary != "Show notes." {
		t.Fatalf("players must not leak into summary: %q", pod.Summary)
	}
	if got := byGUID["inline"].ImageURL; got != "https://media.test/inline.png" {
		t.Fatalf("inline img: %q", got)
	}
	mc := byGUID["mc"]
	if mc.ImageURL != "https://media.test/mc.jpg" || strings.Contains(mc.Content, "<video") {
		t.Fatalf("media:content: image=%q content=%q", mc.ImageURL, mc.Content)
	}
}

func TestNormalizeGUID(t *testing.T) {
	cases := map[string]string{
		"https://www.bbc.co.uk/news/articles/c3v4zgzr1kxo#0":  "https://www.bbc.co.uk/news/articles/c3v4zgzr1kxo",
		"https://www.bbc.co.uk/news/articles/c3v4zgzr1kxo#12": "https://www.bbc.co.uk/news/articles/c3v4zgzr1kxo",
		"https://example.com/page#section":                    "https://example.com/page#section",
		"  tag:example.com,2026:post#1  ":                     "tag:example.com,2026:post#1",
		"post-1":                                              "post-1",
	}
	for in, want := range cases {
		if got := normalizeGUID(in); got != want {
			t.Errorf("normalizeGUID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestArticleNavigationOnlyUsesWebURLs(t *testing.T) {
	for _, bad := range []string{"javascript:alert(1)", "data:text/html,test", "file:///etc/passwd"} {
		if got := resolveURL("https://example.com/feed", bad); got != "" {
			t.Fatalf("unsafe URL retained: %q", got)
		}
	}
	if got := resolveURL("https://example.com/feed", "/story"); got != "https://example.com/story" {
		t.Fatal(got)
	}
}
