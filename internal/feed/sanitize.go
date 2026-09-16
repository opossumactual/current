package feed

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

var policy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("loading").Matching(regexp.MustCompile(`^lazy$`)).OnElements("img")
	p.AllowAttrs("width", "height").Matching(bluemonday.Integer).OnElements("img", "iframe", "video")
	p.AllowAttrs("src").Matching(regexp.MustCompile(`^https://(www\.)?(youtube\.com|youtube-nocookie\.com)/embed/[A-Za-z0-9_-]+`)).OnElements("iframe")
	p.AllowAttrs("allowfullscreen", "frameborder").OnElements("iframe")
	p.AllowElements("figure", "figcaption", "video", "audio", "source", "picture")
	p.AllowAttrs("src", "controls", "poster", "preload").OnElements("video", "audio")
	p.AllowAttrs("src", "type", "srcset", "media").OnElements("source")
	p.AllowAttrs("srcset", "sizes").OnElements("img")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^[a-zA-Z0-9 _-]+$`)).OnElements("code", "pre", "span", "div")
	p.RequireNoFollowOnLinks(false)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return p
}()

// Sanitize cleans untrusted article HTML, resolves relative URLs against base,
// and makes images lazy.
func Sanitize(raw, base string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	raw = absolutize(raw, base)
	out := policy.Sanitize(raw)
	out = strings.ReplaceAll(out, "<img ", `<img loading="lazy" `)
	return strings.TrimSpace(out)
}

// absolutize resolves image candidates before sanitization, including common
// lazy-loading attributes which would otherwise be discarded by the policy.
func absolutize(raw, base string) string {
	b, err := url.Parse(base)
	if err != nil {
		return raw
	}
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return raw
	}
	var body *html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "body" && body == nil {
			body = n
		}
		if n.Type == html.ElementNode {
			if n.Data == "img" || n.Data == "source" {
				set := func(key, val string) {
					for i := range n.Attr {
						if n.Attr[i].Key == key {
							n.Attr[i].Val = val
							return
						}
					}
					n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
				}
				if n.Data == "img" {
					for _, key := range []string{"data-original", "data-src", "data-lazy-src"} {
						if u := imageURL(attr(n, key), b); u != "" {
							set("src", u)
							break
						}
					}
				}
				for _, key := range []string{"data-srcset", "data-lazy-srcset", "srcset"} {
					var candidates []string
					for _, c := range parseSrcset(attr(n, key), b) {
						candidates = append(candidates, strings.TrimSpace(c.url+" "+c.descriptor))
					}
					if len(candidates) > 0 {
						set("srcset", strings.Join(candidates, ", "))
						break
					}
					if key == "srcset" {
						set("srcset", "")
					}
				}
			}
			for i, a := range n.Attr {
				if a.Key == "src" || a.Key == "href" {
					if u, err := url.Parse(strings.TrimSpace(a.Val)); err == nil && !u.IsAbs() && !strings.HasPrefix(a.Val, "#") && !strings.HasPrefix(strings.ToLower(a.Val), "data:") {
						n.Attr[i].Val = b.ResolveReference(u).String()
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if body == nil {
		return raw
	}
	var sb strings.Builder
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		html.Render(&sb, c)
	}
	return sb.String()
}
