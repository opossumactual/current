package feed

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mmcdole/gofeed"
	ext2 "github.com/mmcdole/gofeed/extensions"
	"golang.org/x/net/html"

	"current/internal/store"
)

// ParsedFeed is a normalized feed document.
type ParsedFeed struct {
	Title       string
	SiteURL     string
	Description string
	Items       []store.Article
}

// SummaryLen is the maximum summary length in runes.
const SummaryLen = 300

// Parse parses RSS, Atom or JSON Feed bytes. baseURL resolves relative links.
func Parse(body []byte, baseURL string) (*ParsedFeed, error) {
	gf, err := gofeed.NewParser().Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}
	site := resolveURL(baseURL, gf.Link)
	pf := &ParsedFeed{Title: strings.TrimSpace(gf.Title), SiteURL: site, Description: strings.TrimSpace(gf.Description)}
	contentBase := site
	if contentBase == "" {
		contentBase = baseURL
	}
	for _, it := range gf.Items {
		a := store.Article{
			Title: strings.TrimSpace(it.Title),
			URL:   resolveURL(contentBase, it.Link),
		}
		raw := it.Content
		if strings.TrimSpace(raw) == "" {
			raw = it.Description
		}
		m := extractMedia(it)
		if strings.TrimSpace(raw) == "" {
			raw = mediaFallback(it, a.URL)
		}
		raw += m.players()
		a.Content = Sanitize(raw, a.URL)
		a.Summary = Summarize(a.Content, SummaryLen)
		a.ImageURL = firstImage(a.Content)
		if a.ImageURL == "" {
			a.ImageURL = m.image
		}
		if it.Author != nil {
			a.Author = strings.TrimSpace(it.Author.Name)
		}
		if a.Author == "" && len(it.Authors) > 0 && it.Authors[0] != nil {
			a.Author = strings.TrimSpace(it.Authors[0].Name)
		}
		switch {
		case it.PublishedParsed != nil:
			a.PublishedAt = it.PublishedParsed.UTC()
		case it.UpdatedParsed != nil:
			a.PublishedAt = it.UpdatedParsed.UTC()
		case gf.PublishedParsed != nil:
			// Undated item: the channel date is a better guess than "now",
			// which would float stale entries to the top of every list.
			a.PublishedAt = gf.PublishedParsed.UTC()
		case gf.UpdatedParsed != nil:
			a.PublishedAt = gf.UpdatedParsed.UTC()
		}
		a.GUID = normalizeGUID(it.GUID)
		if a.GUID == "" {
			a.GUID = a.URL
		}
		if a.GUID == "" {
			h := sha1.Sum([]byte(a.Title + "|" + a.PublishedAt.Format(time.RFC3339)))
			a.GUID = "sha1:" + hex.EncodeToString(h[:])
		}
		pf.Items = append(pf.Items, a)
	}
	return pf, nil
}

var revisionFragment = regexp.MustCompile(`#\d+$`)

// normalizeGUID trims a GUID and drops a trailing "#<n>" revision fragment
// from URL-shaped GUIDs. The BBC, among others, bumps that number every time
// a story is edited, which would otherwise surface each edit as a new article.
func normalizeGUID(guid string) string {
	guid = strings.TrimSpace(guid)
	if strings.HasPrefix(guid, "http://") || strings.HasPrefix(guid, "https://") {
		guid = revisionFragment.ReplaceAllString(guid, "")
	}
	return guid
}

var youtubeID = regexp.MustCompile(`^https?://(?:www\.|m\.)?(?:youtube\.com/watch\?(?:.*&)?v=|youtu\.be/|youtube\.com/shorts/)([A-Za-z0-9_-]{6,})`)

// mediaFallback builds HTML for items whose body is empty from Media RSS
// extensions: a YouTube embed for YouTube links, otherwise the thumbnail,
// followed by the media:description as escaped paragraphs.
func mediaFallback(it *gofeed.Item, link string) string {
	var thumb, desc string
	if ext, ok := it.Extensions["media"]; ok {
		for _, g := range ext["group"] {
			thumb = firstAttr(g.Children["thumbnail"], "url")
			for _, d := range g.Children["description"] {
				desc = d.Value
			}
		}
		if thumb == "" {
			thumb = firstAttr(ext["thumbnail"], "url")
		}
		if desc == "" {
			for _, d := range ext["description"] {
				desc = d.Value
			}
		}
	}
	if thumb == "" && it.Image != nil {
		thumb = it.Image.URL
	}
	var sb strings.Builder
	if m := youtubeID.FindStringSubmatch(link); m != nil {
		fmt.Fprintf(&sb, `<iframe src="https://www.youtube-nocookie.com/embed/%s" allowfullscreen></iframe>`, m[1])
	} else if thumb != "" {
		fmt.Fprintf(&sb, `<p><img src="%s" alt=""></p>`, html.EscapeString(thumb))
	}
	for _, para := range strings.Split(strings.TrimSpace(desc), "\n\n") {
		if para = strings.TrimSpace(para); para == "" {
			continue
		}
		sb.WriteString("<p>")
		sb.WriteString(strings.ReplaceAll(html.EscapeString(para), "\n", "<br>"))
		sb.WriteString("</p>")
	}
	return sb.String()
}

// itemMedia is what Media RSS extensions and enclosures contribute to an item.
type itemMedia struct {
	image  string   // best still image: largest media:thumbnail or media:content image, else an image enclosure
	audio  []string // audio enclosure URLs
	videos []string // video enclosure URLs
}

// players renders audio/video enclosures as HTML players to append to the body.
func (m itemMedia) players() string {
	var sb strings.Builder
	for _, u := range m.audio {
		fmt.Fprintf(&sb, `<figure><audio controls preload="none" src="%s"></audio></figure>`, html.EscapeString(u))
	}
	for _, u := range m.videos {
		fmt.Fprintf(&sb, `<figure><video controls preload="none" src="%s"></video></figure>`, html.EscapeString(u))
	}
	return sb.String()
}

var imageExt = regexp.MustCompile(`(?i)\.(jpe?g|png|gif|webp|avif)(\?.*)?$`)

func isImage(typ, medium, u string) bool {
	return strings.HasPrefix(typ, "image/") || medium == "image" || (typ == "" && medium == "" && imageExt.MatchString(u))
}

// extractMedia picks the best still image and the playable enclosures of an item.
func extractMedia(it *gofeed.Item) itemMedia {
	var m itemMedia
	bestW := -1
	consider := func(u string, width string) {
		u = strings.TrimSpace(u)
		if u == "" {
			return
		}
		w, _ := strconv.Atoi(strings.TrimSpace(width))
		if w > bestW {
			bestW, m.image = w, u
		}
	}
	if ext, ok := it.Extensions["media"]; ok {
		groups := [][]ext2.Extension{ext["thumbnail"], ext["content"]}
		for _, g := range ext["group"] {
			groups = append(groups, g.Children["thumbnail"], g.Children["content"])
		}
		for _, list := range groups {
			for _, e := range list {
				u := e.Attrs["url"]
				if e.Name == "thumbnail" || isImage(e.Attrs["type"], e.Attrs["medium"], u) {
					consider(u, e.Attrs["width"])
				}
			}
		}
	}
	for _, en := range it.Enclosures {
		if en == nil {
			continue
		}
		switch {
		case strings.HasPrefix(en.Type, "audio/"):
			m.audio = append(m.audio, en.URL)
		case strings.HasPrefix(en.Type, "video/"):
			m.videos = append(m.videos, en.URL)
		case isImage(en.Type, "", en.URL):
			if m.image == "" {
				m.image = strings.TrimSpace(en.URL)
			}
		}
	}
	if m.image == "" && it.Image != nil {
		m.image = strings.TrimSpace(it.Image.URL)
	}
	return m
}

// firstImage returns the best source for the first image in sanitized HTML.
func firstImage(htmlText string) string {
	images := ArticleImages(&store.Article{Content: htmlText})
	if len(images) > 0 {
		return images[0].URL
	}
	return ""
}

func firstAttr(exts []ext2.Extension, name string) string {
	for _, e := range exts {
		if v := strings.TrimSpace(e.Attrs[name]); v != "" {
			return v
		}
	}
	return ""
}

// SafeURL restricts article navigation and media links to web addresses.
func SafeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.String()
}

func resolveURL(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	b, err := url.Parse(base)
	if err != nil || base == "" {
		return SafeURL(ref)
	}
	return SafeURL(b.ResolveReference(r).String())
}

// Summarize strips tags and truncates to n runes.
func Summarize(htmlText string, n int) string {
	var sb strings.Builder
	tok := html.NewTokenizer(strings.NewReader(htmlText))
	for {
		tt := tok.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt == html.TextToken {
			sb.WriteString(string(tok.Text()))
			sb.WriteByte(' ')
		}
	}
	s := strings.Join(strings.Fields(sb.String()), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n])) + "…"
}
