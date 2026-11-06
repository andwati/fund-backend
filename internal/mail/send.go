package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Sender delivers a rendered message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Resend sends through the Resend REST API, trying twice like the web app. With no API key it
// only logs the message, so local development works without email.
type Resend struct {
	APIKey  string
	From    string
	BaseURL string // default https://api.resend.com
	Client  *http.Client
	Log     *slog.Logger
}

func (r *Resend) Send(ctx context.Context, m Message) error {
	log := r.Log
	if log == nil {
		log = slog.Default()
	}
	if r.APIKey == "" {
		log.Info("email not sent: RESEND_API_KEY is empty", "to", m.To, "subject", m.Subject, "text", m.Text)
		return nil
	}
	if r.From == "" {
		return errors.New("EMAIL_FROM is required")
	}
	base, client := r.BaseURL, r.Client
	if base == "" {
		base = "https://api.resend.com"
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	body, err := json.Marshal(map[string]any{
		"from": r.From, "to": m.To, "subject": m.Subject, "html": m.HTML, "text": m.Text, "headers": m.Headers,
	})
	if err != nil {
		return err
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/emails", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+r.APIKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			last = err
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode/100 == 2 {
			return nil
		}
		last = fmt.Errorf("resend: %s: %s", resp.Status, bytes.TrimSpace(b))
	}
	return last
}
