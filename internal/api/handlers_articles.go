package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"current/internal/feed"
	"current/internal/store"
)

func queryInt64(r *http.Request, key string) (*int64, bool) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return nil, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil, false
	}
	return &n, true
}

func (s *Server) handleListArticles(w http.ResponseWriter, r *http.Request) {
	q := store.ArticleQuery{Status: store.StatusAll, Query: r.URL.Query().Get("q"), Cursor: r.URL.Query().Get("cursor")}
	var ok bool
	if q.FeedID, ok = queryInt64(r, "feed"); !ok {
		writeError(w, http.StatusBadRequest, "bad feed")
		return
	}
	if q.CategoryID, ok = queryInt64(r, "category"); !ok {
		writeError(w, http.StatusBadRequest, "bad category")
		return
	}
	switch st := r.URL.Query().Get("status"); st {
	case "", "all":
	case "unread":
		q.Status = store.StatusUnread
	case "starred":
		q.Status = store.StatusStarred
	default:
		writeError(w, http.StatusBadRequest, "bad status")
		return
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "bad limit")
			return
		}
		q.Limit = n
	}
	items, next, err := s.store.ListArticles(q)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	for i := range items {
		items[i].URL = feed.SafeURL(items[i].URL)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next": next})
}

func (s *Server) handleGetArticle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad id")
		return
	}
	a, err := s.store.GetArticle(id)
	if err != nil {
		storeErr(w, err)
		return
	}
	a.URL = feed.SafeURL(a.URL)
	a.Content = feed.Sanitize(a.Content, a.URL)
	a.Fulltext = feed.Sanitize(a.Fulltext, a.URL)
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleUpdateArticle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad id")
		return
	}
	var body struct {
		Read    *bool `json:"read"`
		Starred *bool `json:"starred"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.store.GetArticle(id); err != nil {
		storeErr(w, err)
		return
	}
	if body.Read != nil {
		if err := s.store.SetRead([]int64{id}, *body.Read); err != nil {
			storeErr(w, err)
			return
		}
	}
	if body.Starred != nil {
		if err := s.store.SetStarred(id, *body.Starred); err != nil {
			storeErr(w, err)
			return
		}
	}
	a, err := s.store.GetArticle(id)
	if err != nil {
		storeErr(w, err)
		return
	}
	a.Content = ""
	a.URL = feed.SafeURL(a.URL)
	a.Content = feed.Sanitize(a.Content, a.URL)
	a.Fulltext = feed.Sanitize(a.Fulltext, a.URL)
	writeJSON(w, http.StatusOK, a)
}

// handleFulltextArticle extracts the readable body of the article's web page
// on demand, caches it, and returns the article with both versions.
func (s *Server) handleFulltextArticle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad id")
		return
	}
	a, err := s.store.GetArticle(id)
	if err != nil {
		storeErr(w, err)
		return
	}
	if a.Fulltext == "" {
		if feed.SafeURL(a.URL) == "" {
			writeError(w, http.StatusUnprocessableEntity, "article has no link to fetch")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		text, err := feed.Extract(ctx, s.client, a.URL)
		if err != nil {
			writeError(w, http.StatusBadGateway, "could not extract article: "+err.Error())
			return
		}
		if err := s.store.SetFulltext(id, text); err != nil {
			storeErr(w, err)
			return
		}
		a.Fulltext = text
	}
	a.URL = feed.SafeURL(a.URL)
	a.Content = feed.Sanitize(a.Content, a.URL)
	a.Fulltext = feed.Sanitize(a.Fulltext, a.URL)
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FeedID     *int64     `json:"feedId"`
		CategoryID *int64     `json:"categoryId"`
		OlderThan  *time.Time `json:"olderThan"`
		IDs        []int64    `json:"ids"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(body.IDs) > 0 {
		if err := s.store.SetRead(body.IDs, true); err != nil {
			storeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"marked": len(body.IDs)})
		return
	}
	n, err := s.store.MarkAllRead(body.FeedID, body.CategoryID, body.OlderThan)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"marked": n})
}

func (s *Server) handleCounts(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.Counts()
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}
