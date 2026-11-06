package money

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"testing"
)

func TestParityWithNode(t *testing.T) {
	f, err := os.Open("../export/testdata/lib.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Money []struct {
			C   string  `json:"c"`
			A   float64 `json:"a"`
			Out string  `json:"out"`
		} `json:"money"`
		Long []struct {
			D   string `json:"d"`
			Y   bool   `json:"y"`
			Out string `json:"out"`
		} `json:"long"`
	}
	if err := json.NewDecoder(zr).Decode(&data); err != nil {
		t.Fatal(err)
	}
	if len(data.Money) < 1000 {
		t.Fatalf("too few money vectors: %d", len(data.Money))
	}
	for _, m := range data.Money {
		if got := Money(m.C, m.A); got != m.Out {
			t.Errorf("Money(%q, %v) = %q want %q", m.C, m.A, got, m.Out)
		}
	}
	for _, l := range data.Long {
		if got := FmtLong(l.D, l.Y); got != l.Out {
			t.Errorf("FmtLong(%q,%v) = %q want %q", l.D, l.Y, got, l.Out)
		}
	}
}
