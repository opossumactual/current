package api

import (
	"net/http"
	"strings"
)

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.store.ListCategories()
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cats)
}

func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	body.Title = strings.TrimSpace(body.Title)
	if body.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	c, err := s.store.CreateCategory(body.Title)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad id")
		return
	}
	cur, err := s.store.GetCategory(id)
	if err != nil {
		storeErr(w, err)
		return
	}
	var body struct {
		Title    *string `json:"title"`
		Position *int    `json:"position"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	title, pos := cur.Title, cur.Position
	if body.Title != nil {
		title = strings.TrimSpace(*body.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "title cannot be empty")
			return
		}
	}
	if body.Position != nil {
		pos = *body.Position
	}
	if err := s.store.UpdateCategory(id, title, pos); err != nil {
		storeErr(w, err)
		return
	}
	c, _ := s.store.GetCategory(id)
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.store.DeleteCategory(id); err != nil {
		storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
