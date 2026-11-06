package reminders

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"testing"
)

type golden struct {
	Secret string `json:"secret"`
	Tokens []struct {
		F, U, Token string
		OK          *struct{ FundID, UserID string }
	} `json:"tokens"`
	Bad []struct {
		T   string
		Out *struct{ FundID, UserID string }
	} `json:"bad"`
	URL string `json:"url"`
}

func TestUnsubscribeTokensMatchTheWebApp(t *testing.T) {
	f, err := os.Open("../mail/testdata/mail.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, _ := gzip.NewReader(f)
	var g golden
	if err := json.NewDecoder(zr).Decode(&g); err != nil {
		t.Fatal(err)
	}
	secret := []byte(g.Secret)
	for _, tk := range g.Tokens {
		// Byte-for-byte equal, so links already in inboxes keep working.
		if got := SignUnsubscribe(secret, tk.F, tk.U); got != tk.Token {
			t.Errorf("sign(%q,%q) = %q want %q", tk.F, tk.U, got, tk.Token)
		}
		fid, uid, ok := VerifyUnsubscribe(secret, tk.Token)
		if !ok || fid != tk.OK.FundID || uid != tk.OK.UserID {
			t.Errorf("verify(%q) = %q %q %v", tk.Token, fid, uid, ok)
		}
	}
	for _, b := range g.Bad {
		if _, _, ok := VerifyUnsubscribe(secret, b.T); ok != (b.Out != nil) {
			t.Errorf("verify(%q) ok=%v, web app says %v", b.T, ok, b.Out != nil)
		}
	}
	if _, _, ok := VerifyUnsubscribe([]byte("another-secret-123456"), g.Tokens[0].Token); ok {
		t.Error("token verified under a different secret")
	}
	// Same token as the web app's URL helper (the path differs: the API drops "/api").
	want := "https://api.test/api/unsubscribe/reminders?token="
	if len(g.URL) <= len(want) {
		t.Fatalf("unexpected golden url %q", g.URL)
	}
	if got := UnsubscribeURL("https://api.test/", secret, "fund-1", "user-1"); got != "https://api.test/unsubscribe/reminders?token="+g.URL[len(want):] {
		t.Errorf("url %q", got)
	}
}
