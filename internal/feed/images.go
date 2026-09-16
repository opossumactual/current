package feed

import (
	"net/url"
	"strings"

	"current/internal/store"
	"golang.org/x/net/html"
)

type ArticleImage struct {
	URL     string `json:"url"`
	Caption string `json:"caption"`
}

// ArticleImages is also the allowlist for the image proxy. Callers can select an
// index, never an arbitrary URL. Keep the lead image first, upgrading it when HTML advertises a larger source.
func ArticleImages(a *store.Article) []ArticleImage {
	out := []ArticleImage{}
	seen := map[string]int{}
	base, _ := url.Parse(a.URL)
	quality := map[int]int{}
	removed := map[int]bool{}
	add := func(sources []string, caption string, fromHTML bool) {
		if len(sources) == 0 {
			return
		}
		caption = strings.Join(strings.Fields(caption), " ")
		if len(caption) > 2000 {
			caption = string([]rune(caption)[:min(500, len([]rune(caption)))])
		}
		rank := 0
		if fromHTML {
			rank = 1
			if len(sources) > 1 {
				rank = 2
			}
		}
		index := -1
		matches := map[int]bool{}
		for _, src := range sources {
			if i, ok := seen[src]; ok {
				matches[i] = true
				if index < 0 || i < index {
					index = i
				}
			}
		}
		if index < 0 {
			if len(out) >= 64 {
				return
			}
			index = len(out)
			out = append(out, ArticleImage{URL: sources[0], Caption: caption})
		} else {
			// A later srcset can connect a bare full-text image and a stored
			// lead which appeared unrelated until their aliases were known.
			for i := index + 1; i < len(out); i++ {
				if !matches[i] {
					continue
				}
				if quality[i] > quality[index] {
					out[index].URL, quality[index] = out[i].URL, quality[i]
				}
				if out[index].Caption == "" {
					out[index].Caption = out[i].Caption
				}
				removed[i] = true
				for alias, owner := range seen {
					if owner == i {
						seen[alias] = index
					}
				}
			}
			if rank > quality[index] {
				out[index].URL = sources[0]
			}
			if out[index].Caption == "" {
				out[index].Caption = caption
			}
		}
		quality[index] = max(quality[index], rank)
		for _, src := range sources {
			seen[src] = index
		}
	}
	if lead := imageURL(a.ImageURL, base); lead != "" {
		add([]string{lead}, "", false)
	}
	var text func(*html.Node) string
	text = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		var s strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			s.WriteString(text(c))
			s.WriteByte(' ')
		}
		return s.String()
	}
	for _, content := range []string{a.Fulltext, a.Content, a.Summary} {
		doc, err := html.Parse(strings.NewReader(content))
		if err != nil {
			continue
		}
		var walk func(*html.Node, string)
		walk = func(n *html.Node, caption string) {
			if n.Type == html.ElementNode && n.Data == "figure" {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Data == "figcaption" {
						caption = text(c)
					}
				}
			}
			if n.Type == html.ElementNode && n.Data == "img" {
				if caption == "" {
					caption = attr(n, "alt")
				}
				add(imageSources(n, base), caption, true)
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c, caption)
			}
		}
		walk(doc, "")
	}
	result := make([]ArticleImage, 0, len(out))
	for i, img := range out {
		if !removed[i] {
			result = append(result, img)
		}
	}
	return result
}
