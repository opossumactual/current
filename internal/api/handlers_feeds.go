package api

import (
	"context"
	"current/internal/opml"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"current/internal/crawler"
	"current/internal/feed"
	"current/internal/store"
)

func (s *Server) handleListFeeds(w http.ResponseWriter, r *http.Request) {
	feeds, err := s.store.ListFeeds()
	if r.URL.Query().Get("includeArchived") == "1" {
		feeds, err = s.store.ListAllFeeds()
	}
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, feeds)
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.Library()
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	u := strings.TrimSpace(r.URL.Query().Get("url"))
	if u == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	var err error
	u, err = opml.NormalizeURL(u)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	cands, err := feed.Discover(ctx, feed.NewClient(), u)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cands)
}

func (s *Server) handleCreateFeed(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL        string `json:"url"`
		CategoryID *int64 `json:"categoryId"`
		Title      string `json:"title"`
		Direct     bool   `json:"direct"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var err error
	body.URL, err = opml.NormalizeURL(body.URL)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if body.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	if body.CategoryID != nil {
		if _, err := s.store.GetCategory(*body.CategoryID); err != nil {
			writeError(w, http.StatusBadRequest, "unknown category")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	feedURL := body.URL
	title := body.Title
	var cands []feed.Candidate
	var derr error
	if !body.Direct {
		cands, derr = feed.Discover(ctx, feed.NewClient(), body.URL)
	}
	if derr == nil && len(cands) > 0 {
		feedURL = cands[0].URL
		if title == "" {
			title = cands[0].Title
		}
	}
	if !strings.Contains(feedURL, "://") {
		feedURL = "https://" + feedURL
	}
	f, err := s.store.SubscribeFeed(&store.Feed{URL: feedURL, Title: title, CategoryID: body.CategoryID, IconURL: crawler.IconURL("", feedURL)})
	if err != nil {
		storeErr(w, err)
		return
	}
	if derr != nil {
		s.store.MarkFetched(f.ID, "", "", derr, time.Now().UTC())
	} else if s.crawler != nil {
		s.crawler.RefreshFeed(ctx, f.ID)
	}
	f, err = s.store.GetFeed(f.ID)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) handleUpdateFeed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad id")
		return
	}
	var body struct {
		Title            *string         `json:"title"`
		URL              *string         `json:"url"`
		CategoryID       json.RawMessage `json:"categoryId"`
		ExtractFulltext  *bool           `json:"extractFulltext"`
		FetchIntervalMin *int            `json:"fetchIntervalMin"`
		Paused           *bool           `json:"paused"`
		Unsubscribed     *bool           `json:"unsubscribed"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	u := store.FeedUpdate{Title: body.Title, URL: body.URL, ExtractFulltext: body.ExtractFulltext, FetchIntervalMin: body.FetchIntervalMin, Paused: body.Paused, Unsubscribed: body.Unsubscribed}
	if u.Title != nil {
		*u.Title = strings.TrimSpace(*u.Title)
		if *u.Title == "" {
			writeError(w, 400, "title cannot be empty")
			return
		}
	}
	if u.URL != nil {
		v, err := opml.NormalizeURL(*u.URL)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		u.URL = &v
	}
	if len(body.CategoryID) > 0 {
		var cat *int64
		if err := json.Unmarshal(body.CategoryID, &cat); err != nil {
			writeError(w, 400, "invalid categoryId")
			return
		}
		if cat != nil {
			if _, err := s.store.GetCategory(*cat); err != nil {
				writeError(w, 400, "unknown category")
				return
			}
		}
		u.CategoryID = &cat
	}
	if u.FetchIntervalMin != nil && (*u.FetchIntervalMin < 5 || *u.FetchIntervalMin > 1440) {
		writeError(w, 400, "fetchIntervalMin must be between 5 and 1440")
		return
	}

	if err := s.store.UpdateFeed(id, u); err != nil {
		storeErr(w, err)
		return
	}
	f, err := s.store.GetFeed(id)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.store.UnsubscribeFeed(id); err != nil {
		storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRefreshFeed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad id")
		return
	}
	if s.crawler == nil {
		writeError(w, http.StatusServiceUnavailable, "crawler not running")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	ev, err := s.crawler.RefreshFeed(ctx, id)
	if err != nil {
		storeErr(w, err)
		return
	}
	f, err := s.store.GetFeed(id)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"feed": f, "inserted": ev.Inserted})
}

func (s *Server) handleRefreshAll(w http.ResponseWriter, r *http.Request) {
	if s.crawler == nil {
		writeError(w, http.StatusServiceUnavailable, "crawler not running")
		return
	}
	progress, err := s.crawler.StartRefresh()
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, progress)
}

func (s *Server) handleRefreshProgress(w http.ResponseWriter, r *http.Request) {
	if s.crawler == nil {
		writeError(w, http.StatusServiceUnavailable, "crawler not running")
		return
	}
	writeJSON(w, http.StatusOK, s.crawler.Progress())
}
