package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const unsubscribedPage = `<!doctype html>
<html lang="en">
  <head><meta charset="utf-8"><title>Unsubscribed</title></head>
  <body>
    <p>Payday reminders are off for you on this fund. You can turn them back on in Settings.</p>
  </body>
</html>`

func (s *Server) registerRoutes() {
	s.handle("GET /healthz", healthz)
	s.handle("GET /readyz", s.readyz)
	s.handle("GET /cron/reminders", s.cronReminders)
	s.handle("POST /cron/reminders", s.cronReminders)
	s.handle("GET /unsubscribe/reminders", s.unsubscribeGet)
	s.handle("POST /unsubscribe/reminders", s.unsubscribePost)
	s.handle("GET /openapi.yaml", serveOpenAPI)
	s.authRoutes()
	s.fundRoutes()
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Pool.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, okBody{false})
		return
	}
	writeJSON(w, http.StatusOK, okBody{true})
}

// authorizeCron is a constant-time check for "Authorization: Bearer <secret>".
func authorizeCron(r *http.Request, secret string) bool {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(h[len("Bearer "):]), []byte(secret)) == 1
}

// cronReminders sends the "payday is tomorrow" emails. Call it hourly with the cron secret.
func (s *Server) cronReminders(w http.ResponseWriter, r *http.Request) {
	if s.CronSecret == "" {
		s.failMsg(w, http.StatusNotFound, "Not enabled.")
		return
	}
	if !authorizeCron(r, s.CronSecret) {
		s.failMsg(w, http.StatusUnauthorized, "Unauthorized.")
		return
	}
	res, err := s.Reminders.Run(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) unsubscribe(w http.ResponseWriter, r *http.Request, token string) {
	if token == "" {
		s.failMsg(w, http.StatusBadRequest, "Missing token.")
		return
	}
	ok, err := s.Reminders.Unsubscribe(r.Context(), token)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		s.failMsg(w, http.StatusBadRequest, "Invalid token.")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(unsubscribedPage))
}

// unsubscribeGet is the manual click from the email footer.
func (s *Server) unsubscribeGet(w http.ResponseWriter, r *http.Request) {
	s.unsubscribe(w, r, r.URL.Query().Get("token"))
}

// unsubscribePost is RFC 8058 one-click: POST with `List-Unsubscribe=One-Click`.
func (s *Server) unsubscribePost(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		if err := r.ParseForm(); err != nil {
			s.failMsg(w, http.StatusBadRequest, "Unsupported body.")
			return
		}
		form, _ := url.ParseQuery(r.PostForm.Encode())
		if form.Get("List-Unsubscribe") != "One-Click" {
			s.failMsg(w, http.StatusBadRequest, "Unsupported body.")
			return
		}
		if token == "" {
			token = form.Get("token")
		}
	}
	s.unsubscribe(w, r, token)
}
