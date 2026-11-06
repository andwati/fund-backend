package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaultsAndRequired(t *testing.T) {
	_, err := Load(env(nil))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "AUTH_SECRET") {
		t.Fatalf("expected required-field errors, got %v", err)
	}

	c, err := Load(env(map[string]string{
		"DATABASE_URL": "postgres://x",
		"AUTH_SECRET":  "0123456789abcdef",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != "4000" || c.PublicURL != "http://localhost:4000" {
		t.Fatalf("defaults wrong: %+v", c)
	}
}

func TestLoadValidatesSecretsAndEmail(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x", "AUTH_SECRET": "0123456789abcdef"}

	short := map[string]string{"CRON_SECRET": "short"}
	for k, v := range base {
		short[k] = v
	}
	if _, err := Load(env(short)); err == nil {
		t.Fatal("short CRON_SECRET should fail")
	}

	noFrom := map[string]string{"RESEND_API_KEY": "re_x"}
	for k, v := range base {
		noFrom[k] = v
	}
	if _, err := Load(env(noFrom)); err == nil {
		t.Fatal("RESEND_API_KEY without EMAIL_FROM should fail")
	}
}
