package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Email-link tokens are HS256 JWTs signed with the shared secret, like better-auth's
// verification tokens (so the `requestType` claim keeps its meaning).
type claims struct {
	Email       string `json:"email"`
	UpdateTo    string `json:"updateTo,omitempty"`
	RequestType string `json:"requestType,omitempty"`
	Iat         int64  `json:"iat"`
	Exp         int64  `json:"exp"`
}

const (
	reqChangeEmailConfirm = "change-email-confirmation"
	reqChangeEmailVerify  = "change-email-verification"
)

var b64 = base64.RawURLEncoding

func signJWT(secret []byte, c claims) string {
	header := b64.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, _ := json.Marshal(c)
	signing := header + "." + b64.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))
	return signing + "." + b64.EncodeToString(mac.Sum(nil))
}

var errBadToken = errors.New("invalid or expired token")

func parseJWT(secret []byte, token string, now time.Time) (claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims{}, errBadToken
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	sig, err := b64.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return claims{}, errBadToken
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	hb, err := b64.DecodeString(parts[0])
	if err != nil || json.Unmarshal(hb, &hdr) != nil || hdr.Alg != "HS256" {
		return claims{}, errBadToken
	}
	pb, err := b64.DecodeString(parts[1])
	if err != nil {
		return claims{}, errBadToken
	}
	var c claims
	if json.Unmarshal(pb, &c) != nil || c.Exp <= now.Unix() {
		return claims{}, errBadToken
	}
	return c, nil
}
