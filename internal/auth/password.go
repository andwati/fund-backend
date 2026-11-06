package auth

import (
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"golang.org/x/crypto/scrypt"
	"golang.org/x/text/unicode/norm"

	"github.com/andwati/fund-backend/internal/id"
)

// Password hashing is better-auth's format so existing `account.password` values keep working:
//
//	"<saltHex>:<keyHex>"  scrypt N=16384 r=16 p=1 dkLen=64 over NFKC(password)
//
// Gotcha: the salt passed to scrypt is the 32-character hex STRING's UTF-8 bytes, not the
// 16 decoded bytes.
const (
	scryptN   = 16384
	scryptR   = 16
	scryptP   = 1
	scryptLen = 64
)

func derive(password, salt string) ([]byte, error) {
	return scrypt.Key([]byte(norm.NFKC.String(password)), []byte(salt), scryptN, scryptR, scryptP, scryptLen)
}

// HashPassword returns a new hash in better-auth's format.
func HashPassword(password string) (string, error) {
	salt := id.Hex(16)
	key, err := derive(password, salt)
	if err != nil {
		return "", err
	}
	return salt + ":" + hex.EncodeToString(key), nil
}

// VerifyPassword checks password against a stored hash in constant time.
func VerifyPassword(password, stored string) bool {
	salt, keyHex, ok := strings.Cut(stored, ":")
	if !ok || salt == "" || keyHex == "" {
		return false
	}
	want, err := hex.DecodeString(keyHex)
	if err != nil {
		return false
	}
	got, err := derive(password, salt)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified against when a user does not exist so sign-in timing does not reveal
// whether an email is registered.
var dummyHash = func() string {
	h, err := HashPassword("not-a-real-password")
	if err != nil {
		panic(err)
	}
	return h
}()
