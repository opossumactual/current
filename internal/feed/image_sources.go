package feed

import (
	"math"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

func imageURL(raw string, base *url.URL) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	u.Fragment = ""
	return u.String()
}

type imageCandidate struct {
	url, descriptor string
	width, density  float64
}

// Follow srcset's whitespace-delimited URL tokens: commas inside CDN URLs
// are not separators. Invalid descriptors are ignored, never guessed.
func parseSrcset(raw string, base *url.URL) []imageCandidate {
	var out []imageCandidate
	const space = " \t\n\r\f"
	for raw != "" {
		raw = strings.TrimLeft(raw, space+",")
		if raw == "" {
			break
		}
		i := strings.IndexAny(raw, space)
		if i < 0 {
			i = len(raw)
		}
		src := raw[:i]
		raw = raw[i:]
		descriptor := ""
		if strings.HasSuffix(src, ",") {
			src = strings.TrimRight(src, ",")
		} else {
			// Parenthesized descriptors are invalid here, but consume them as
			// one unit so a comma inside one cannot become a phantom image.
			depth, end := 0, 0
			for end < len(raw) {
				c := raw[end]
				if c == ',' && depth == 0 {
					break
				}
				if c == '(' {
					depth++
				} else if c == ')' && depth > 0 {
					depth--
				}
				end++
			}
			descriptor = strings.TrimSpace(raw[:end])
			raw = raw[end:]
		}
		c := imageCandidate{url: imageURL(src, base), descriptor: descriptor, density: 1}
		if c.url == "" {
			continue
		}
		if descriptor != "" {
			fields := strings.Fields(descriptor)
			if len(fields) != 1 || len(fields[0]) < 2 {
				continue
			}
			d := fields[0]
			switch d[len(d)-1] {
			case 'w':
				n, err := strconv.ParseUint(d[:len(d)-1], 10, 32)
				if err != nil || n == 0 {
					continue
				}
				c.width = float64(n)
			case 'x':
				n, err := strconv.ParseFloat(d[:len(d)-1], 64)
				if err != nil || n <= 0 || math.IsInf(n, 0) || math.IsNaN(n) {
					continue
				}
				c.density = n
			default:
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func rasterType(typ string) bool {
	switch strings.ToLower(typ) {
	case "", "image/jpeg", "image/png", "image/webp", "image/gif":
		return true
	}
	return false
}

// imageSources returns the best advertised source first, followed by its
// aliases. Aliases let old stored lead thumbnails match their larger version.
func imageSources(n *html.Node, base *url.URL) []string {
	var out []string
	seen := map[string]bool{}
	add := func(raw string) {
		u := imageURL(raw, base)
		parsed, _ := url.Parse(u)
		if u == "" || seen[u] {
			return
		}
		switch strings.ToLower(path.Ext(parsed.Path)) {
		case ".avif", ".svg", ".jxl", ".heic": // not decoded by the terminal
			return
		}
		seen[u] = true
		out = append(out, u)
	}
	// Only direct raster-image links qualify as originals, not article links.
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Data == "a" {
			u, err := url.Parse(attr(p, "href"))
			if err == nil {
				switch strings.ToLower(path.Ext(u.Path)) {
				case ".jpg", ".jpeg", ".png", ".webp", ".gif":
					add(u.String())
				}
			}
			break
		}
	}
	addSet := func(node *html.Node) {
		for _, key := range []string{"data-srcset", "data-lazy-srcset", "srcset"} {
			candidates := parseSrcset(attr(node, key), base)
			// For density descriptors, src participates as the 1x fallback.
			widths, densityOne := false, false
			for _, c := range candidates {
				widths = widths || c.width > 0
				densityOne = densityOne || c.density == 1
			}
			if len(candidates) > 0 && !widths && !densityOne && node.Data == "img" {
				if u := imageURL(attr(node, "src"), base); u != "" {
					candidates = append(candidates, imageCandidate{url: u, density: 1})
				}
			}
			sort.SliceStable(candidates, func(i, j int) bool {
				if candidates[i].width != candidates[j].width {
					return candidates[i].width > candidates[j].width
				}
				return candidates[i].density > candidates[j].density
			})
			for _, c := range candidates {
				add(c.url)
			}
		}
	}
	if p := n.Parent; p != nil && p.Data == "picture" {
		for source := p.FirstChild; source != nil && source != n; source = source.NextSibling {
			// Media-qualified sources can be a different crop for mobile.
			// Without a browser viewport, prefer an unconditional source.
			if source.Data == "source" && attr(source, "media") == "" && rasterType(attr(source, "type")) {
				addSet(source)
			}
		}
	}
	addSet(n)
	lazy := false
	for _, key := range []string{"data-original", "data-src", "data-lazy-src"} {
		if u := imageURL(attr(n, key), base); u != "" {
			add(u)
			lazy = true
		}
	}
	if !lazy {
		add(attr(n, "src"))
	}
	return out
}
