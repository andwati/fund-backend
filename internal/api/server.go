// Package api is the HTTP layer: routing, auth middleware, rate limits and JSON responses.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andwati/fund-backend/internal/apperr"
	"github.com/andwati/fund-backend/internal/auth"
	"github.com/andwati/fund-backend/internal/fund"
	"github.com/andwati/fund-backend/internal/ratelimit"
	"github.com/andwati/fund-backend/internal/reminders"
)

// Deps is everything the handlers need.
type Deps struct {
	Pool      *pgxpool.Pool
	Auth      *auth.Service
	Fund      *fund.Service
	Reminders *reminders.Runner
	Limiter   *ratelimit.Limiter
	Log       *slog.Logger

	CronSecret       string   // empty disables /cron/reminders (404)
	CORSOrigins      []string // allowed browser origins (Expo web)
	TrustProxy       bool     // read the client IP from X-Forwarded-For / X-Real-IP
	RateLimitEnabled bool
}

type Server struct {
	Deps
	mux    *http.ServeMux
	routes []string // registered "METHOD /path" patterns (checked against openapi.yaml in tests)
}

const (
	maxBody       = 1 << 20 // 1 MiB
	maxImportBody = 4 << 20 // CSV import
)

// New builds the router. Use Handler for the full middleware chain.
func New(d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Limiter == nil {
		d.Limiter = ratelimit.New()
	}
	s := &Server{Deps: d, mux: http.NewServeMux()}
	s.registerRoutes()
	return s
}

func (s *Server) handle(pattern string, h http.HandlerFunc) {
	s.routes = append(s.routes, pattern)
	s.mux.HandleFunc(pattern, h)
}

// Handler wraps the routes with recovery, CORS, request logging and body limits.
func (s *Server) Handler() http.Handler {
	return s.recoverer(s.cors(s.logRequests(s.mux)))
}

// ---- responses -------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errBody struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error"`
	Status int    `json:"status"`
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if ae, ok := apperr.As(err); ok {
		writeJSON(w, ae.Status, errBody{false, ae.Message, ae.Status})
		return
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeJSON(w, http.StatusRequestEntityTooLarge, errBody{false, "That request is too large.", http.StatusRequestEntityTooLarge})
		return
	}
	s.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeJSON(w, http.StatusInternalServerError, errBody{false, "Something went wrong. Try again.", http.StatusInternalServerError})
}

func (s *Server) failMsg(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errBody{false, msg, status})
}

// decode reads a JSON body into v. An empty body is fine for endpoints with all-optional fields.
func decode(w http.ResponseWriter, r *http.Request, v any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		if ae, ok := apperr.As(err); ok {
			return ae
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return err
		}
		return apperr.New(http.StatusBadRequest, "Send a valid JSON body.")
	}
	return nil
}

// ---- client info -----------------------------------------------------------------------

func (s *Server) clientIP(r *http.Request) string {
	if s.TrustProxy {
		if f := r.Header.Get("X-Forwarded-For"); f != "" {
			if first := strings.TrimSpace(strings.Split(f, ",")[0]); first != "" {
				return first
			}
		}
		if rip := strings.TrimSpace(r.Header.Get("X-Real-IP")); rip != "" {
			return rip
		}
		return "unknown"
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// ---- auth middleware -------------------------------------------------------------------

type authedHandler func(w http.ResponseWriter, r *http.Request, sess *auth.Session)

// authed requires a valid session.
func (s *Server) authed(h authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.Auth.GetSession(r.Context(), bearer(r))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if sess == nil {
			s.failMsg(w, http.StatusUnauthorized, "Sign in required.")
			return
		}
		h(w, r, sess)
	}
}

// verified requires a session whose email is verified (every fund action does, like requireUser).
func (s *Server) verified(h authedHandler) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
		if !sess.User.EmailVerified {
			s.failMsg(w, http.StatusForbidden, "Verify your email before using your fund.")
			return
		}
		h(w, r, sess)
	})
}

// limited applies a per-IP fixed-window rule to a route.
func (s *Server) limited(name string, rule ratelimit.Rule, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.RateLimitEnabled {
			if ok, retry := s.Limiter.Take(name+":"+s.clientIP(r), rule.Limit, rule.Window); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(retry))
				s.failMsg(w, http.StatusTooManyRequests, "Too many requests. Wait a little and try again.")
				return
			}
		}
		h(w, r)
	}
}

// ---- middleware ------------------------------------------------------------------------

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.Log.Error("panic", "path", r.URL.Path, "panic", v)
				s.failMsg(w, http.StatusInternalServerError, "Something went wrong. Try again.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if r.URL.Path != "/healthz" {
			s.Log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
		}
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range s.CORSOrigins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && allowed[origin] {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Max-Age", "600")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

var _ = context.Background
