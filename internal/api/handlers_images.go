package api

import (
	"current/internal/feed"
	"net/http"
)

func (s *Server) handleArticleImages(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, feed.ArticleImages(a))
}
