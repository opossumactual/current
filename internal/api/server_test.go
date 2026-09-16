package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"current/internal/crawler"
	"current/internal/store"
)

type env struct {
	t    *testing.T
	srv  *httptest.Server
	feed *httptest.Server
	st   *store.Store
}

func newEnv(t *testing.T, password string) *env {
	t.Helper()
	body, _ := os.ReadFile("../feed/testdata/rss.xml")
	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/bad") {
			w.WriteHeader(500)
			return
		}
		w.Write(body)
	}))
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	cr := crawler.New(st, feedSrv.Client(), crawler.Options{Workers: 2, Logger: log.New(io.Discard, "", 0)})
	s := New(st, cr, nil, password, log.New(io.Discard, "", 0))
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(func() { srv.Close(); feedSrv.Close(); st.Close() })
	return &env{t: t, srv: srv, feed: feedSrv, st: st}
}

func (e *env) do(method, path string, body any, out any) int {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
			e.t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

func TestFeedLifecycle(t *testing.T) {
	e := newEnv(t, "")
	var cat map[string]any
	if st := e.do("POST", "/api/categories", map[string]string{"title": "Tech"}, &cat); st != 201 {
		t.Fatalf("create category: %d", st)
	}
	catID := int64(cat["id"].(float64))
	var f map[string]any
	if st := e.do("POST", "/api/feeds", map[string]any{"url": e.feed.URL + "/rss", "categoryId": catID}, &f); st != 201 {
		t.Fatalf("create feed: %d %v", st, f)
	}
	if f["title"] != "Example RSS" || f["unread"].(float64) != 2 || f["categoryId"].(float64) != float64(catID) {
		t.Fatalf("feed json: %v", f)
	}
	for _, k := range []string{"id", "categoryId", "title", "url", "siteUrl", "iconUrl", "unread", "lastFetchedAt", "lastError", "extractFulltext", "fetchIntervalMin"} {
		if _, ok := f[k]; !ok {
			t.Fatalf("feed json missing %s", k)
		}
	}
	feedID := int64(f["id"].(float64))
	if st := e.do("POST", "/api/feeds", map[string]any{"url": e.feed.URL + "/rss"}, nil); st != 409 {
		t.Fatalf("duplicate: %d", st)
	}
	var feeds []map[string]any
	e.do("GET", "/api/feeds", nil, &feeds)
	if len(feeds) != 1 {
		t.Fatalf("feeds = %d", len(feeds))
	}
	var page struct {
		Items []map[string]any `json:"items"`
		Next  string           `json:"next"`
	}
	e.do("GET", fmt.Sprintf("/api/articles?feed=%d&status=unread", feedID), nil, &page)
	if len(page.Items) != 2 {
		t.Fatalf("articles = %d", len(page.Items))
	}
	for _, k := range []string{"id", "feedId", "title", "url", "author", "summary", "publishedAt", "read", "starred"} {
		if _, ok := page.Items[0][k]; !ok {
			t.Fatalf("article json missing %s", k)
		}
	}
	if _, ok := page.Items[0]["content"]; ok {
		t.Fatalf("list should not include content")
	}
	aid := int64(page.Items[0]["id"].(float64))
	var art map[string]any
	e.do("GET", fmt.Sprintf("/api/articles/%d", aid), nil, &art)
	if art["content"] == nil || art["feedTitle"] != "Example RSS" {
		t.Fatalf("article detail: %v", art)
	}
	var upd map[string]any
	if st := e.do("PATCH", fmt.Sprintf("/api/articles/%d", aid), map[string]bool{"read": true, "starred": true}, &upd); st != 200 || upd["read"] != true || upd["starred"] != true {
		t.Fatalf("patch article: %d %v", st, upd)
	}
	var counts struct {
		Total      int            `json:"total"`
		Starred    int            `json:"starred"`
		Feeds      map[string]int `json:"feeds"`
		Categories map[string]int `json:"categories"`
	}
	e.do("GET", "/api/counts", nil, &counts)
	if counts.Total != 1 || counts.Starred != 1 || counts.Feeds[fmt.Sprint(feedID)] != 1 || counts.Categories[fmt.Sprint(catID)] != 1 {
		t.Fatalf("counts: %+v", counts)
	}
	var marked map[string]int
	e.do("POST", "/api/articles/mark-read", map[string]any{"feedId": feedID}, &marked)
	if marked["marked"] != 1 {
		t.Fatalf("mark-read: %v", marked)
	}
	e.do("GET", "/api/counts", nil, &counts)
	if counts.Total != 0 {
		t.Fatalf("counts after mark all: %+v", counts)
	}
	var search struct{ Items []map[string]any }
	e.do("GET", "/api/articles?q=first", nil, &search)
	if len(search.Items) != 1 {
		t.Fatalf("search = %d", len(search.Items))
	}
	if st := e.do("PATCH", fmt.Sprintf("/api/feeds/%d", feedID), map[string]any{"title": "Renamed", "categoryId": nil, "extractFulltext": true, "fetchIntervalMin": 60}, &f); st != 200 {
		t.Fatalf("patch feed: %d %v", st, f)
	}
	if f["title"] != "Renamed" || f["categoryId"] != nil || f["extractFulltext"] != true || f["fetchIntervalMin"].(float64) != 60 {
		t.Fatalf("patched feed: %v", f)
	}
	if st := e.do("PATCH", fmt.Sprintf("/api/feeds/%d", feedID), map[string]any{"fetchIntervalMin": 1}, nil); st != 400 {
		t.Fatalf("interval validation: %d", st)
	}
	var rf map[string]any
	if st := e.do("POST", fmt.Sprintf("/api/feeds/%d/refresh", feedID), nil, &rf); st != 200 || rf["inserted"].(float64) != 0 {
		t.Fatalf("refresh: %d %v", st, rf)
	}
	var bad map[string]any
	if st := e.do("POST", "/api/feeds", map[string]any{"url": e.feed.URL + "/bad"}, &bad); st != 201 || bad["lastError"] == "" {
		t.Fatalf("failing feed should still be created with lastError: %d %v", st, bad)
	}
	if st := e.do("DELETE", fmt.Sprintf("/api/feeds/%d", feedID), nil, nil); st != 204 {
		t.Fatalf("delete: %d", st)
	}
	if st := e.do("GET", fmt.Sprintf("/api/articles/%d", aid), nil, &art); st != 200 || art["starred"] != true {
		t.Fatalf("saved article must survive unsubscribe: %d", st)
	}
	if st := e.do("DELETE", fmt.Sprintf("/api/categories/%d", catID), nil, nil); st != 204 {
		t.Fatalf("delete category: %d", st)
	}
	var errBody map[string]string
	if st := e.do("GET", "/api/nope", nil, &errBody); st != 404 || errBody["error"] == "" {
		t.Fatalf("api 404 shape: %d %v", st, errBody)
	}
}

func TestOPMLRoundTrip(t *testing.T) {
	e := newEnv(t, "")
	doc := fmt.Sprintf(`<opml version="2.0"><body><outline text="Cat"><outline text="F" type="rss" xmlUrl="%s/rss" htmlUrl="%s/"/></outline></body></opml>`, e.feed.URL, e.feed.URL)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "subs.opml")
	fw.Write([]byte(doc))
	mw.Close()
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/opml/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var res map[string]int
	json.NewDecoder(resp.Body).Decode(&res)
	resp.Body.Close()
	if resp.StatusCode != 200 || res["added"] != 1 {
		t.Fatalf("import: %d %v", resp.StatusCode, res)
	}
	resp, _ = http.Get(e.srv.URL + "/api/opml/export")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `xmlUrl="`+e.feed.URL+`/rss"`) || !strings.Contains(string(b), `text="Cat"`) {
		t.Fatalf("export: %s", b)
	}
}

func TestAuth(t *testing.T) {
	e := newEnv(t, "secret")
	if st := e.do("GET", "/api/feeds", nil, nil); st != 401 {
		t.Fatalf("unauthenticated: %d", st)
	}
	var status map[string]bool
	e.do("GET", "/api/auth", nil, &status)
	if !status["required"] || status["authenticated"] {
		t.Fatalf("auth status: %v", status)
	}
	if st := e.do("POST", "/api/login", map[string]string{"password": "nope"}, nil); st != 401 {
		t.Fatalf("wrong password: %d", st)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/login", strings.NewReader(`{"password":"secret"}`))
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(resp.Cookies()) == 0 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest("GET", e.srv.URL+"/api/feeds", nil)
	req.AddCookie(resp.Cookies()[0])
	resp2, _ := http.DefaultClient.Do(req)
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("with cookie: %d", resp2.StatusCode)
	}
}

func TestStaticFallback(t *testing.T) {
	e := newEnv(t, "")
	resp, _ := http.Get(e.srv.URL + "/some/deep/link")
	resp.Body.Close()
	// build dir only has .gitkeep in tests; fallback to index.html yields 404 from FileServer, but must not hit the API 404 JSON
	if strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("static route returned API json")
	}
}

func TestFulltextExtractsOnceAndCaches(t *testing.T) {
	hits := 0
	pageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>Long story</title></head><body><nav>menu</nav><article>` +
			strings.Repeat("<p>The full text of the story, paragraph after paragraph, keeps going here.</p>", 12) +
			`</article></body></html>`))
	}))
	defer pageSrv.Close()
	e := newEnv(t, "")
	f, err := e.st.CreateFeed(&store.Feed{URL: e.feed.URL + "/rss", Title: "F"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.UpsertArticles(f.ID, []store.Article{{GUID: "g1", Title: "Teaser", URL: pageSrv.URL + "/story", Content: "<p>teaser</p>"}, {GUID: "g2", Title: "No link"}}); err != nil {
		t.Fatal(err)
	}
	items, _, _ := e.st.ListArticles(store.ArticleQuery{})
	var withURL, noURL int64
	for _, a := range items {
		if a.URL == "" {
			noURL = a.ID
		} else {
			withURL = a.ID
		}
	}
	var got store.Article
	if code := e.do("POST", fmt.Sprintf("/api/articles/%d/fulltext", withURL), nil, &got); code != 200 {
		t.Fatalf("status %d", code)
	}
	if !strings.Contains(got.Fulltext, "paragraph after paragraph") || strings.Contains(got.Fulltext, "menu") || got.Content != "<p>teaser</p>" {
		t.Fatalf("fulltext=%q content=%q", got.Fulltext, got.Content)
	}
	if code := e.do("POST", fmt.Sprintf("/api/articles/%d/fulltext", withURL), nil, &got); code != 200 || hits != 1 {
		t.Fatalf("second call: status %d hits %d", code, hits)
	}
	if code := e.do("GET", fmt.Sprintf("/api/articles/%d", withURL), nil, &got); code != 200 || got.Fulltext == "" {
		t.Fatalf("GET should include cached fulltext: %d %q", code, got.Fulltext)
	}
	if code := e.do("POST", fmt.Sprintf("/api/articles/%d/fulltext", noURL), nil, nil); code != 422 {
		t.Fatalf("no link: status %d", code)
	}
}
