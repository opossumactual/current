// Package api serves the JSON API and the embedded frontend.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"current/internal/crawler"
	"current/internal/feed"
	"current/internal/store"
)

// Server holds dependencies for handlers.
type Server struct {
	store    *store.Store
	crawler  *crawler.Crawler
	broker   *Broker
	password string
	logger   *log.Logger
	client   *http.Client
}

// New creates a Server. broker may be nil when events are not needed.
func New(st *store.Store, cr *crawler.Crawler, broker *Broker, password string, logger *log.Logger) *Server {
	if broker == nil {
		broker = NewBroker()
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Server{store: st, crawler: cr, broker: broker, password: password, logger: logger, client: feed.NewClient()}
}

// Handler returns the HTTP handler for API and static files.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Route("/api", func(r chi.Router) {
		r.Get("/auth", s.handleAuthStatus)
		r.Post("/login", s.handleLogin)
		r.Post("/logout", s.handleLogout)
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/feeds", s.handleListFeeds)
			r.Get("/library", s.handleLibrary)
			r.Post("/feeds", s.handleCreateFeed)
			r.Patch("/feeds/{id}", s.handleUpdateFeed)
			r.Delete("/feeds/{id}", s.handleDeleteFeed)
			r.Post("/feeds/{id}/refresh", s.handleRefreshFeed)
			r.Get("/discover", s.handleDiscover)
			r.Get("/categories", s.handleListCategories)
			r.Post("/categories", s.handleCreateCategory)
			r.Patch("/categories/{id}", s.handleUpdateCategory)
			r.Delete("/categories/{id}", s.handleDeleteCategory)
			r.Get("/articles", s.handleListArticles)
			r.Get("/articles/{id}", s.handleGetArticle)
			r.Patch("/articles/{id}", s.handleUpdateArticle)
			r.Post("/articles/{id}/fulltext", s.handleFulltextArticle)
			r.Post("/articles/mark-read", s.handleMarkRead)
			r.Get("/counts", s.handleCounts)
			r.Post("/refresh", s.handleRefreshAll)
			r.Get("/refresh", s.handleRefreshProgress)
			r.Get("/articles/{id}/images", s.handleArticleImages)
			r.Post("/opml/import", s.handleOPMLImport)
			r.Get("/opml/export", s.handleOPMLExport)
			r.Get("/events", s.handleEvents)
		})
		r.NotFound(func(w http.ResponseWriter, r *http.Request) { writeError(w, http.StatusNotFound, "not found") })
	})
	r.NotFound(staticHandler().ServeHTTP)
	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return errors.New("invalid JSON body")
	}
	return nil
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

func storeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrDuplicate):
		writeError(w, http.StatusConflict, "already subscribed")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
