package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andwati/fund-backend/internal/apperr"
	"github.com/andwati/fund-backend/internal/auth"
	"github.com/andwati/fund-backend/internal/export"
	"github.com/andwati/fund-backend/internal/fund"
)

type payloadBody struct {
	OK      bool         `json:"ok"`
	Payload fund.Payload `json:"payload"`
}

// action runs a mutation on the fund in the path and answers with the fresh payload, like
// run() in the web app's actions. The mutation may write the request body decode itself.
func (s *Server) action(do func(r *http.Request, w http.ResponseWriter, u auth.User, fundID string) error) http.HandlerFunc {
	return s.verified(func(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
		fid := r.PathValue("fundId")
		if err := do(r, w, sess.User, fid); err != nil {
			s.fail(w, r, err)
			return
		}
		s.writePayload(w, r, sess.User, fid)
	})
}

func (s *Server) writePayload(w http.ResponseWriter, r *http.Request, u auth.User, fundID string) {
	p, err := s.Fund.Payload(r.Context(), u, fundID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payloadBody{true, p})
}

func (s *Server) fundRoutes() {

	f := "/funds/{fundId}"
	s.handle("GET /funds", s.verified(s.listFunds))
	s.handle("POST /funds", s.verified(s.createFund))
	s.handle("GET "+f, s.verified(func(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
		s.writePayload(w, r, sess.User, r.PathValue("fundId"))
	}))
	s.handle("PATCH "+f, s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		var p fund.FundPatch
		if err := decode(w, r, &p, maxBody); err != nil {
			return err
		}
		return s.Fund.UpdateFund(r.Context(), u, id, p)
	}))
	s.handle("DELETE "+f, s.verified(func(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
		if err := s.Fund.DeleteFund(r.Context(), sess.User, r.PathValue("fundId")); err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, okBody{true}) // the fund is gone: nothing to re-read
	}))
	s.handle("POST "+f+"/leave", s.verified(func(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
		if err := s.Fund.LeaveFund(r.Context(), sess.User, r.PathValue("fundId")); err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, okBody{true})
	}))
	s.handle("POST "+f+"/onboarding/complete", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		done := true
		return s.Fund.UpdateFund(r.Context(), u, id, fund.FundPatch{OnboardingCompleted: &done})
	}))
	s.handle("PUT "+f+"/reminders", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		var b struct{ Enabled bool }
		if err := decode(w, r, &b, maxBody); err != nil {
			return err
		}
		return s.Fund.SetReminders(r.Context(), u, id, b.Enabled)
	}))

	// paydays
	s.handle("POST "+f+"/paydays/{date}/skip", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		return s.Fund.SkipPayday(r.Context(), u, id, r.PathValue("date"))
	}))
	s.handle("DELETE "+f+"/paydays/{date}/skip", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		return s.Fund.RestorePayday(r.Context(), u, id, r.PathValue("date"))
	}))
	s.handle("PUT "+f+"/paydays/{date}/amount", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		var b struct{ Amount *float64 }
		if err := decode(w, r, &b, maxBody); err != nil {
			return err
		}
		if b.Amount == nil || *b.Amount != float64(int64(*b.Amount)) {
			return apperr.New(http.StatusBadRequest, "Enter a whole amount.")
		}
		return s.Fund.AdjustPayday(r.Context(), u, id, r.PathValue("date"), int(*b.Amount))
	}))
	s.handle("DELETE "+f+"/paydays/{date}/amount", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		return s.Fund.ClearPaydayAdjustment(r.Context(), u, id, r.PathValue("date"))
	}))

	// items
	s.handle("POST "+f+"/items", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		var b fund.ItemInput
		if err := decode(w, r, &b, maxBody); err != nil {
			return err
		}
		return s.Fund.AddItem(r.Context(), u, id, b)
	}))
	s.handle("PATCH "+f+"/items/{itemId}", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		var b fund.ItemPatch
		if err := decode(w, r, &b, maxBody); err != nil {
			return err
		}
		return s.Fund.UpdateItem(r.Context(), u, id, r.PathValue("itemId"), b)
	}))
	s.handle("DELETE "+f+"/items/{itemId}", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		return s.Fund.DeleteItem(r.Context(), u, id, r.PathValue("itemId"))
	}))
	s.handle("POST "+f+"/items/{itemId}/archive", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		var b struct {
			Archived *bool          `json:"archived"`
			Purchase *fund.Purchase `json:"purchase"`
		}
		if err := decode(w, r, &b, maxBody); err != nil {
			return err
		}
		archived := b.Archived == nil || *b.Archived
		return s.Fund.SetItemArchived(r.Context(), u, id, r.PathValue("itemId"), archived, b.Purchase)
	}))
	s.handle("POST "+f+"/items/{itemId}/move", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		var b struct{ Direction string }
		if err := decode(w, r, &b, maxBody); err != nil {
			return err
		}
		if b.Direction != "up" && b.Direction != "down" {
			return apperr.New(http.StatusBadRequest, "Direction must be up or down.")
		}
		return s.Fund.MoveItem(r.Context(), u, id, r.PathValue("itemId"), b.Direction)
	}))
	s.handle("POST "+f+"/items/{itemId}/reorder", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		var b struct {
			ToIndex *int `json:"toIndex"`
		}
		if err := decode(w, r, &b, maxBody); err != nil {
			return err
		}
		if b.ToIndex == nil {
			return apperr.New(http.StatusBadRequest, "Send toIndex.")
		}
		return s.Fund.ReorderItem(r.Context(), u, id, r.PathValue("itemId"), *b.ToIndex)
	}))

	// contributions
	s.handle("POST "+f+"/contributions", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		var b fund.ContributionInput
		if err := decode(w, r, &b, maxBody); err != nil {
			return err
		}
		return s.Fund.AddContribution(r.Context(), u, id, b)
	}))
	s.handle("PATCH "+f+"/contributions/{contributionId}", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		var b fund.ContributionInput
		if err := decode(w, r, &b, maxBody); err != nil {
			return err
		}
		return s.Fund.UpdateContribution(r.Context(), u, id, r.PathValue("contributionId"), b)
	}))
	s.handle("DELETE "+f+"/contributions/{contributionId}", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		return s.Fund.DeleteContribution(r.Context(), u, id, r.PathValue("contributionId"))
	}))
	s.handle("POST "+f+"/contributions/import", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		var b struct {
			CSV string `json:"csv"`
		}
		if err := decode(w, r, &b, maxImportBody); err != nil {
			return err
		}
		rows, msg := export.ReadContributionsCSV(b.CSV)
		if msg != "" {
			return apperr.New(http.StatusBadRequest, msg)
		}
		in := make([]fund.ImportRow, 0, len(rows))
		for _, row := range rows {
			lines := make([]fund.ImportAlloc, 0, len(row.Allocations))
			for _, a := range row.Allocations {
				lines = append(lines, fund.ImportAlloc{Item: a.Item, Amount: a.Amount})
			}
			in = append(in, fund.ImportRow{Date: row.Date, Note: row.Note, Line: row.Line, Kind: row.Kind, Allocations: lines})
		}
		return s.Fund.ImportContributions(r.Context(), u, id, in)
	}))

	// sharing
	s.handle("POST "+f+"/invites", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		var b struct{ Email, Role string }
		if err := decode(w, r, &b, maxBody); err != nil {
			return err
		}
		return s.Fund.InviteMember(r.Context(), u, id, b.Email, b.Role)
	}))
	s.handle("POST "+f+"/invites/{inviteId}/resend", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		return s.Fund.ResendInvite(r.Context(), u, id, r.PathValue("inviteId"))
	}))
	s.handle("DELETE "+f+"/invites/{inviteId}", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		return s.Fund.RevokeInvite(r.Context(), u, id, r.PathValue("inviteId"))
	}))
	s.handle("DELETE "+f+"/members/{userId}", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		return s.Fund.RemoveMember(r.Context(), u, id, r.PathValue("userId"))
	}))
	s.handle("PATCH "+f+"/members/{userId}", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		var b struct{ Role string }
		if err := decode(w, r, &b, maxBody); err != nil {
			return err
		}
		return s.Fund.SetMemberRole(r.Context(), u, id, r.PathValue("userId"), b.Role)
	}))
	s.handle("POST "+f+"/transfer-ownership", s.action(func(r *http.Request, w http.ResponseWriter, u auth.User, id string) error {
		var b struct {
			UserID string `json:"userId"`
		}
		if err := decode(w, r, &b, maxBody); err != nil {
			return err
		}
		return s.Fund.TransferOwnership(r.Context(), u, id, b.UserID)
	}))

	// downloads
	s.handle("GET "+f+"/export/contributions.csv", s.verified(func(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
		body, err := s.Fund.ExportContributionsCSV(r.Context(), sess.User, r.PathValue("fundId"))
		s.download(w, r, body, err, "text/csv; charset=utf-8", "fund-contributions.csv")
	}))
	s.handle("GET "+f+"/export/items.csv", s.verified(func(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
		body, err := s.Fund.ExportItemsCSV(r.Context(), sess.User, r.PathValue("fundId"))
		s.download(w, r, body, err, "text/csv; charset=utf-8", "fund-items.csv")
	}))
	s.handle("GET "+f+"/export/schedule.ics", s.verified(func(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
		body, err := s.Fund.ExportScheduleICS(r.Context(), sess.User, r.PathValue("fundId"))
		s.download(w, r, body, err, "text/calendar; charset=utf-8", "fund-paydays.ics")
	}))
	s.handle("GET /account/export", s.verified(func(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
		body, err := s.Fund.ExportAccountZip(r.Context(), sess.User)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="fund-export-%s.zip"`, time.Now().UTC().Format("2006-01-02")))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))

	// invites (public lookups; accepting needs a session)
	s.handle("GET /invites/{token}", func(w http.ResponseWriter, r *http.Request) {
		info, err := s.Fund.ReadInvite(r.Context(), r.PathValue("token"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "invite": info})
	})
	s.handle("POST /invites/{token}/accept", s.verified(func(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
		fundID, err := s.Fund.AcceptInvite(r.Context(), sess.User, r.PathValue("token"), s.clientIP(r))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.writePayload(w, r, sess.User, fundID)
	}))
	s.handle("POST /invites/{token}/accept-signup", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Name, Password string }
		if err := decode(w, r, &b, maxBody); err != nil {
			s.fail(w, r, err)
			return
		}
		sess, fundID, err := s.Fund.AcceptInviteSignup(r.Context(), r.PathValue("token"), b.Name, b.Password, s.clientIP(r), r.UserAgent())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out := sessionResponse(sess)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "fundId": fundID, "token": out.Token, "expiresAt": out.ExpiresAt, "user": out.User})
	})
}

func (s *Server) listFunds(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	funds, err := s.Fund.ListFunds(r.Context(), sess.User)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "funds": funds})
}

func (s *Server) createFund(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	var b struct{ Name string }
	if err := decode(w, r, &b, maxBody); err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := s.Fund.CreateFund(r.Context(), sess.User, b.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writePayload(w, r, sess.User, id)
}

func (s *Server) download(w http.ResponseWriter, r *http.Request, body string, err error, contentType, filename string) {
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, strings.ReplaceAll(filename, `"`, "")))
	_, _ = w.Write([]byte(body))
}
