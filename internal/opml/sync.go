package opml

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"current/internal/store"
)

// Import subscribes to every feed in the OPML document, creating categories as needed.
// Feeds already subscribed are skipped.
func Import(s *store.Store, r io.Reader) (added, skipped int, err error) {
	outlines, err := Parse(r)
	if err != nil {
		return 0, 0, err
	}
	for i := range outlines {
		if outlines[i].XMLURL != "" {
			u, e := NormalizeURL(outlines[i].XMLURL)
			if e != nil {
				return 0, 0, e
			}
			outlines[i].XMLURL = u
		}
	}
	cats := map[string]int64{}
	for _, o := range outlines {
		if o.XMLURL == "" {
			continue
		}
		f := &store.Feed{Title: o.Title, URL: o.XMLURL, SiteURL: o.HTMLURL}
		if o.Category != "" {
			id, ok := cats[o.Category]
			if !ok {
				c, err := s.FindOrCreateCategory(o.Category)
				if err != nil {
					return added, skipped, err
				}
				id = c.ID
				cats[o.Category] = id
			}
			f.CategoryID = &id
		}
		if _, err := s.SubscribeFeed(f); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				skipped++
				continue
			}
			return added, skipped, err
		}
		added++
	}
	return added, skipped, nil
}

// Export renders the current subscriptions as OPML.
func Export(s *store.Store) ([]byte, error) {
	cats, err := s.ListCategories()
	if err != nil {
		return nil, err
	}
	feeds, err := s.ListFeeds()
	if err != nil {
		return nil, err
	}
	byID := map[int64]string{}
	var order []string
	for _, c := range cats {
		byID[c.ID] = c.Title
		order = append(order, c.Title)
	}
	groups := map[string][]Feed{}
	for _, f := range feeds {
		key := ""
		if f.CategoryID != nil {
			key = byID[*f.CategoryID]
		}
		groups[key] = append(groups[key], Feed{Title: f.Title, XMLURL: f.URL, HTMLURL: f.SiteURL})
	}
	return Render(order, groups)
}

// NormalizeURL accepts HTTP(S) subscriptions, including local network sources.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("URL is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("use a valid http:// or https:// feed URL")
	}
	u.Fragment = ""
	return u.String(), nil
}
