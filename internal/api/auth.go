package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

const sessionCookie = "current_session"

func (s *Server) sessionToken() string {
	m := hmac.New(sha256.New, []byte(s.password))
	m.Write([]byte("current-session-v1"))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) authenticated(r *http.Request) bool {
	if s.password == "" {
		return true
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.sessionToken())) == 1
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.password == "" || s.authenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, "login required")
	})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"required": s.password != "", "authenticated": s.authenticated(r)})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.password == "" {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(body.Password)), []byte(s.password)) != 1 {
		time.Sleep(500 * time.Millisecond)
		writeError(w, http.StatusUnauthorized, "wrong password")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: s.sessionToken(), Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 90 * 24 * 3600})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
