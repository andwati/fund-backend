package auth

import (
	"encoding/json"
	"os"
	"testing"
)

type pwVector struct {
	Password string `json:"password"`
	Hash     string `json:"hash"`
}

func TestVerifiesBetterAuthHashes(t *testing.T) {
	b, err := os.ReadFile("testdata/passwords.json")
	if err != nil {
		t.Fatal(err)
	}
	var vs []pwVector
	if err := json.Unmarshal(b, &vs); err != nil {
		t.Fatal(err)
	}
	if len(vs) < 10 {
		t.Fatalf("too few vectors: %d", len(vs))
	}
	for _, v := range vs {
		if !VerifyPassword(v.Password, v.Hash) {
			t.Errorf("failed to verify better-auth hash for %q", v.Password)
		}
		if VerifyPassword(v.Password+"x", v.Hash) {
			t.Errorf("accepted wrong password for %q", v.Password)
		}
	}
}

func TestHashRoundTripAndFormat(t *testing.T) {
	h, err := HashPassword("hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 32+1+128 || h[32] != ':' {
		t.Fatalf("unexpected format %q", h)
	}
	if !VerifyPassword("hunter2hunter2", h) || VerifyPassword("hunter2hunter3", h) {
		t.Fatal("round trip failed")
	}
	// NFC vs NFD forms of the same password are equivalent after NFKC.
	h2, _ := HashPassword("éclair-12345")
	if !VerifyPassword("éclair-12345", h2) {
		t.Fatal("NFKC normalisation not applied")
	}
}

func TestMalformedHashesAreRejected(t *testing.T) {
	for _, bad := range []string{"", "nocolon", ":", "abc:", ":abc", "salt:zz-not-hex"} {
		if VerifyPassword("x", bad) {
			t.Errorf("accepted %q", bad)
		}
	}
}

// Writes Go-made hashes for the Node side to verify (make parity-auth).
func TestWriteGoHashesForNode(t *testing.T) {
	out := os.Getenv("GO_HASHES_OUT")
	if out == "" {
		t.Skip("GO_HASHES_OUT not set")
	}
	var vs []pwVector
	for _, p := range []string{"correct horse battery staple", "ünïcödé-pässwörd", "éclair-12345", "ﬁnal-ﬂag-12345", "日本語のパスワード", "🙂emoji🙂pass"} {
		h, err := HashPassword(p)
		if err != nil {
			t.Fatal(err)
		}
		vs = append(vs, pwVector{p, h})
	}
	b, _ := json.Marshal(vs)
	if err := os.WriteFile(out, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
