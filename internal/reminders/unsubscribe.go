// Package reminders holds the payday reminder job and its one-click unsubscribe tokens.
package reminders

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
)

const purpose = "reminder-unsubscribe:v1"

// Same construction as ~/dev/fund/src/server/reminder-unsubscribe.ts, so links already in
// people's inboxes keep working: "<fundId>:<userId>.<base64url(HMAC-SHA256(secret, purpose:payload))>".
func sign(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(purpose + ":" + payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func SignUnsubscribe(secret []byte, fundID, userID string) string {
	payload := fundID + ":" + userID
	return payload + "." + sign(secret, payload)
}

// VerifyUnsubscribe returns the target of a valid token.
func VerifyUnsubscribe(secret []byte, token string) (fundID, userID string, ok bool) {
	dot := strings.LastIndex(token, ".")
	if dot <= 0 {
		return "", "", false
	}
	payload, sig := token[:dot], token[dot+1:]
	colon := strings.Index(payload, ":")
	if colon <= 0 || colon == len(payload)-1 || sig == "" {
		return "", "", false
	}
	if !hmac.Equal([]byte(sig), []byte(sign(secret, payload))) {
		return "", "", false
	}
	return payload[:colon], payload[colon+1:], true
}

// UnsubscribeURL is the link in reminder emails; base is the API's public URL.
func UnsubscribeURL(base string, secret []byte, fundID, userID string) string {
	return strings.TrimRight(base, "/") + "/unsubscribe/reminders?token=" + url.QueryEscape(SignUnsubscribe(secret, fundID, userID))
}
