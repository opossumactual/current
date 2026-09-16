package api

import (
	"io"
	"net/http"
	"strings"

	"current/internal/opml"
)

func (s *Server) handleOPMLImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	var src io.Reader
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "bad multipart form")
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "missing file field")
			return
		}
		defer f.Close()
		src = f
	} else {
		src = http.MaxBytesReader(w, r.Body, 8<<20)
	}
	added, skipped, err := opml.Import(s.store, src)
	if err != nil {
		writeError(w, http.StatusBadRequest, "import failed: "+err.Error())
		return
	}
	// Imported feeds are due immediately; the scheduler processes them.
	writeJSON(w, http.StatusOK, map[string]int{"added": added, "skipped": skipped})
}

func (s *Server) handleOPMLExport(w http.ResponseWriter, r *http.Request) {
	b, err := opml.Export(s.store)
	if err != nil {
		storeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="current.opml"`)
	w.Write(b)
}
