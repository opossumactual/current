// Package opml reads and writes OPML subscription lists.
package opml

import (
	"encoding/xml"
	"io"
	"strings"
	"time"
)

// Outline is a flattened feed entry with its folder path.
type Outline struct {
	Title    string
	XMLURL   string
	HTMLURL  string
	Category string // first-level folder title, "" if none
}

type opmlDoc struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr"`
	Head    opmlHead `xml:"head"`
	Body    opmlBody `xml:"body"`
}

type opmlHead struct {
	Title       string `xml:"title"`
	DateCreated string `xml:"dateCreated,omitempty"`
}

type opmlBody struct {
	Outlines []outline `xml:"outline"`
}

type outline struct {
	Text     string    `xml:"text,attr"`
	Title    string    `xml:"title,attr,omitempty"`
	Type     string    `xml:"type,attr,omitempty"`
	XMLURL   string    `xml:"xmlUrl,attr,omitempty"`
	HTMLURL  string    `xml:"htmlUrl,attr,omitempty"`
	Outlines []outline `xml:"outline"`
}

// Parse reads an OPML document and flattens nested folders. Feeds inside nested
// folders are assigned to the top-level folder.
func Parse(r io.Reader) ([]Outline, error) {
	var doc opmlDoc
	dec := xml.NewDecoder(r)
	dec.Strict = false
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	var out []Outline
	var walk func(nodes []outline, category string)
	walk = func(nodes []outline, category string) {
		for _, n := range nodes {
			if n.XMLURL != "" {
				title := strings.TrimSpace(n.Title)
				if title == "" {
					title = strings.TrimSpace(n.Text)
				}
				out = append(out, Outline{Title: title, XMLURL: strings.TrimSpace(n.XMLURL), HTMLURL: strings.TrimSpace(n.HTMLURL), Category: category})
				continue
			}
			name := strings.TrimSpace(n.Text)
			if name == "" {
				name = strings.TrimSpace(n.Title)
			}
			if category == "" {
				walk(n.Outlines, name)
			} else {
				walk(n.Outlines, category)
			}
		}
	}
	walk(doc.Body.Outlines, "")
	return out, nil
}

// Feed is a feed to export.
type Feed struct {
	Title   string
	XMLURL  string
	HTMLURL string
}

// Render produces an OPML document. groups maps category title to feeds;
// uncategorized feeds go under the "" key. order lists category titles in order.
func Render(order []string, groups map[string][]Feed) ([]byte, error) {
	doc := opmlDoc{Version: "2.0", Head: opmlHead{Title: "Current subscriptions", DateCreated: time.Now().UTC().Format(time.RFC1123)}}
	for _, f := range groups[""] {
		doc.Body.Outlines = append(doc.Body.Outlines, feedOutline(f))
	}
	for _, cat := range order {
		if cat == "" {
			continue
		}
		o := outline{Text: cat, Title: cat}
		for _, f := range groups[cat] {
			o.Outlines = append(o.Outlines, feedOutline(f))
		}
		doc.Body.Outlines = append(doc.Body.Outlines, o)
	}
	b, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(b, '\n')...), nil
}

func feedOutline(f Feed) outline {
	return outline{Text: f.Title, Title: f.Title, Type: "rss", XMLURL: f.XMLURL, HTMLURL: f.HTMLURL}
}
