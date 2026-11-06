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
	PublicURL   string
	ResendKey   string
	EmailFrom   string
	CronSecret  string
	LogLevel    slog.Level
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
