// Package config loads and validates process configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// Config holds validated runtime settings. Secrets are never logged.
type Config struct {
	Port        string
	DatabaseURL string
	AuthSecret  string
	PublicURL   string // the client app: links in emails
	APIURL      string // this API: unsubscribe links (defaults to PUBLIC_URL)
	ResendKey   string
	EmailFrom   string
	CronSecret  string
	LogLevel    slog.Level
	// CORSOrigins are browser origins allowed to call the API (the Expo web app).
	CORSOrigins []string
	// TrustProxy reads the client IP from X-Forwarded-For / X-Real-IP (set false if not behind a proxy).
	TrustProxy bool
	// RateLimit enables the per-IP auth route limits (off in tests).
	RateLimit bool
	// MigrateOnStart applies pending migrations at boot. Leave off until `fund-migrate baseline` has
	// been run on a database created by the web app.
	MigrateOnStart bool
}

// Load reads configuration using getenv (os.Getenv in production).
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Port:        orDefault(getenv("PORT"), "4000"),
		DatabaseURL: getenv("DATABASE_URL"),
		AuthSecret:  getenv("AUTH_SECRET"),
		PublicURL:   strings.TrimRight(orDefault(getenv("PUBLIC_URL"), "http://localhost:4000"), "/"),
		ResendKey:   getenv("RESEND_API_KEY"),
		EmailFrom:   getenv("EMAIL_FROM"),
		CronSecret:  getenv("CRON_SECRET"),
	}

	c.APIURL = strings.TrimRight(orDefault(getenv("API_URL"), c.PublicURL), "/")
	for _, o := range strings.Split(getenv("CORS_ORIGINS"), ",") {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			c.CORSOrigins = append(c.CORSOrigins, o)
		}
	}
	c.TrustProxy = boolEnv(getenv("TRUST_PROXY"), true)
	c.RateLimit = boolEnv(getenv("RATE_LIMIT"), true)
	c.MigrateOnStart = boolEnv(getenv("MIGRATE_ON_START"), false)

	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if len(c.AuthSecret) < 16 {
		errs = append(errs, errors.New("AUTH_SECRET must be at least 16 characters"))
	}
	if c.CronSecret != "" && len(c.CronSecret) < 16 {
		errs = append(errs, errors.New("CRON_SECRET must be at least 16 characters when set"))
	}
	if c.ResendKey != "" && c.EmailFrom == "" {
		errs = append(errs, errors.New("EMAIL_FROM is required when RESEND_API_KEY is set"))
	}
	if err := c.LogLevel.UnmarshalText([]byte(orDefault(getenv("LOG_LEVEL"), "info"))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
	}
	return c, errors.Join(errs...)
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func boolEnv(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}
