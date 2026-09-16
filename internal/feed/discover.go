package feed

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// Candidate is a feed URL found by discovery.
type Candidate struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

var commonPaths = []string{"feed", "rss", "rss.xml", "atom.xml", "index.xml", "feed.xml", "feed/", "feeds/posts/default"}

// Discover returns feed candidates for a URL: the URL itself if it is a feed,
// otherwise feeds advertised by <link rel="alternate"> or found at common paths.
func Discover(ctx context.Context, client *http.Client, rawURL string) ([]Candidate, error) {
	rawURL = strings.TrimSpace(rawURL)
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}
	res, err := Fetch(ctx, client, rawURL, "", "")
	if err != nil {
		return nil, err
	}
	if pf, err := Parse(res.Body, res.FinalURL); err == nil {
		return []Candidate{{URL: rawURL, Title: pf.Title}}, nil
	}
	base, err := url.Parse(res.FinalURL)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	seen := map[string]bool{}
	doc, err := html.Parse(strings.NewReader(string(res.Body)))
	if err == nil {
		var walk func(n *html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "link" {
				var rel, typ, href, title string
				for _, a := range n.Attr {
					switch strings.ToLower(a.Key) {
					case "rel":
						rel = strings.ToLower(a.Val)
					case "type":
						typ = strings.ToLower(a.Val)
					case "href":
						href = a.Val
					case "title":
						title = a.Val
					}
				}
				if strings.Contains(rel, "alternate") && href != "" &&
					(strings.Contains(typ, "rss") || strings.Contains(typ, "atom") || strings.Contains(typ, "feed+json") || strings.Contains(typ, "json")) {
					if u, err := url.Parse(href); err == nil {
						abs := base.ResolveReference(u).String()
						if !seen[abs] {
							seen[abs] = true
							out = append(out, Candidate{URL: abs, Title: title})
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
	}
	if len(out) > 0 {
		return out, nil
	}
	for _, p := range commonPaths {
		u := base.ResolveReference(&url.URL{Path: p})
		r, err := Fetch(ctx, client, u.String(), "", "")
		if err != nil {
			continue
		}
		if pf, err := Parse(r.Body, u.String()); err == nil {
			return []Candidate{{URL: u.String(), Title: pf.Title}}, nil
		}
	}
	return nil, errors.New("no feed found at " + rawURL)
}
