package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtract(t *testing.T) {
	page := `<html><head><title>T</title></head><body><nav>menu menu menu</nav>
	<article><h1>Headline</h1>` + strings.Repeat("<p>This is a long paragraph of real article text that readability should keep around.</p>", 12) +
		`<img src="/pic.jpg"></article><footer>footer</footer></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(page)) }))
	defer srv.Close()
	out, err := Extract(context.Background(), srv.Client(), srv.URL+"/post")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "real article text") || strings.Contains(out, "menu menu") {
		t.Fatalf("bad extraction: %q", out)
	}
	if !strings.Contains(out, srv.URL+"/pic.jpg") {
		t.Fatalf("image not absolutized: %q", out)
	}
}
