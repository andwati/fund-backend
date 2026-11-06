package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/andwati/fund-backend/internal/apperr"
	"github.com/andwati/fund-backend/internal/auth"
	"github.com/andwati/fund-backend/internal/ratelimit"
)

type sessionBody struct {
	OK        bool      `json:"ok"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	User      auth.User `json:"user"`
}

func sessionResponse(s auth.Session) sessionBody {
	return sessionBody{OK: true, Token: s.Token, ExpiresAt: s.ExpiresAt, User: s.User}
}

type okBody struct {
	OK bool `json:"ok"`
}

func (s *Server) authRoutes() {
	// Per-route rules from the web app: sign-in 10/15min, sign-up 5/h, email actions 3/h, else 120/min.
	def := func(name string, h http.HandlerFunc) http.HandlerFunc {
		return s.limited("auth-default:"+name, ratelimit.Default, h)
	}

	s.handle("POST /auth/sign-up", s.limited("sign-up", ratelimit.SignUp, s.signUp))
	s.handle("POST /auth/sign-in", s.limited("sign-in", ratelimit.SignIn, s.signIn))
	s.handle("POST /auth/sign-out", def("sign-out", s.authed(s.signOut)))
	s.handle("GET /auth/session", def("session", s.authed(s.getSession)))
	s.handle("GET /auth/sessions", def("sessions", s.authed(s.listSessions)))
	s.handle("DELETE /auth/sessions/{id}", def("revoke", s.authed(s.revokeSession)))
	s.handle("POST /auth/sessions/revoke-others", def("revoke-others", s.authed(s.revokeOthers)))
	s.handle("POST /auth/verify-email", def("verify-email", s.verifyEmail))
	s.handle("POST /auth/send-verification-email", s.limited("send-verification-email", ratelimit.EmailAction, s.sendVerification))
	s.handle("POST /auth/request-password-reset", s.limited("request-password-reset", ratelimit.EmailAction, s.requestReset))
	s.handle("POST /auth/reset-password", def("reset-password", s.resetPassword))
	s.handle("POST /auth/change-password", def("change-password", s.authed(s.changePassword)))
	s.handle("POST /auth/change-email", def("change-email", s.authed(s.changeEmail)))
	s.handle("PATCH /auth/profile", def("profile", s.authed(s.updateProfile)))
	s.handle("DELETE /auth/account", def("delete-account", s.authed(s.deleteAccount)))
}

func (s *Server) signUp(w http.ResponseWriter, r *http.Request) {
	var b struct{ Name, Email, Password string }
	if err := decode(w, r, &b, maxBody); err != nil {
		s.fail(w, r, err)
		return
	}
	sess, err := s.Auth.SignUp(r.Context(), b.Name, b.Email, b.Password, s.clientIP(r), r.UserAgent())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse(sess))
}

func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	var b struct{ Email, Password string }
	if err := decode(w, r, &b, maxBody); err != nil {
		s.fail(w, r, err)
		return
	}
	sess, err := s.Auth.SignIn(r.Context(), b.Email, b.Password, s.clientIP(r), r.UserAgent())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse(sess))
}

func (s *Server) signOut(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	if err := s.Auth.SignOut(r.Context(), sess.Token); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody{true})
}

func (s *Server) getSession(w http.ResponseWriter, _ *http.Request, sess *auth.Session) {
	writeJSON(w, http.StatusOK, sessionResponse(*sess))
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	list, err := s.Auth.ListSessions(r.Context(), sess.User.ID, sess.Token)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sessions": list})
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	if err := s.Auth.RevokeSession(r.Context(), sess.User.ID, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody{true})
}

func (s *Server) revokeOthers(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	if err := s.Auth.RevokeOtherSessions(r.Context(), sess.User.ID, sess.Token); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody{true})
}

func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var b struct{ Token string }
	if err := decode(w, r, &b, maxBody); err != nil {
		s.fail(w, r, err)
		return
	}
	res, err := s.Auth.VerifyEmail(r.Context(), b.Token, s.clientIP(r), r.UserAgent())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"ok": true, "status": res.Status, "user": res.User}
	if res.Session != nil {
		out["token"], out["expiresAt"] = res.Session.Token, res.Session.ExpiresAt
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) sendVerification(w http.ResponseWriter, r *http.Request) {
	var b struct{ Email string }
	if err := decode(w, r, &b, maxBody); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Auth.SendVerificationEmail(r.Context(), b.Email); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody{true})
}

func (s *Server) requestReset(w http.ResponseWriter, r *http.Request) {
	var b struct{ Email string }
	if err := decode(w, r, &b, maxBody); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Auth.RequestPasswordReset(r.Context(), b.Email); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody{true}) // same answer whether or not the address exists
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var b struct{ Token, Password string }
	if err := decode(w, r, &b, maxBody); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Auth.ResetPassword(r.Context(), b.Token, b.Password); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody{true})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	var b struct {
		CurrentPassword     string `json:"currentPassword"`
		NewPassword         string `json:"newPassword"`
		RevokeOtherSessions bool   `json:"revokeOtherSessions"`
	}
	if err := decode(w, r, &b, maxBody); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Auth.ChangePassword(r.Context(), sess.User, sess.Token, b.CurrentPassword, b.NewPassword, b.RevokeOtherSessions); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody{true})
}

func (s *Server) changeEmail(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	var b struct {
		NewEmail string `json:"newEmail"`
	}
	if err := decode(w, r, &b, maxBody); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Auth.ChangeEmail(r.Context(), sess.User, b.NewEmail); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody{true})
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	var b struct {
		Name  *string `json:"name"`
		Image *string `json:"image"`
	}
	if err := decode(w, r, &b, maxBody); err != nil {
		s.fail(w, r, err)
		return
	}
	u, err := s.Auth.UpdateProfile(r.Context(), sess.User.ID, b.Name, b.Image)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": u})
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	var b struct{ Password string }
	if err := decode(w, r, &b, maxBody); err != nil {
		s.fail(w, r, err)
		return
	}
	if strings.TrimSpace(b.Password) == "" {
		s.fail(w, r, apperr.New(http.StatusBadRequest, "Enter your password to delete your account."))
		return
	}
	if err := s.Auth.DeleteAccount(r.Context(), sess.User, b.Password); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody{true})
}
