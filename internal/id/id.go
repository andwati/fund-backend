// Package id generates identifiers and opaque tokens from crypto/rand.
package id

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// New returns a random UUID v4, like crypto.randomUUID() in the web app.
func New() string {
	var b [16]byte
	mustRead(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}

// Token returns n random bytes, base64url-encoded without padding.
func Token(n int) string {
	b := make([]byte, n)
	mustRead(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Hex returns n random bytes as lowercase hex.
func Hex(n int) string {
	b := make([]byte, n)
	mustRead(b)
	return hex.EncodeToString(b)
}

func mustRead(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
}
