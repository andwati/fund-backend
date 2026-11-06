package export

import (
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type libFile struct {
	ItemRows []struct {
		Name        string  `json:"name"`
		Price       int     `json:"price"`
		Deadline    string  `json:"deadline"`
		PlanStart   string  `json:"planStart"`
		Saved       int     `json:"saved"`
		ActualPrice *int    `json:"actualPrice"`
		PurchasedOn *string `json:"purchasedOn"`
		Archived    bool    `json:"archived"`
	} `json:"itemRows"`
	Items       string `json:"items"`
	ContribRows []struct {
		Date        string `json:"date"`
		Note        string `json:"note"`
		Kind        string `json:"kind"`
		Allocations []struct {
			ItemID string `json:"itemId"`
			Amount int    `json:"amount"`
		} `json:"allocations"`
	} `json:"contribRows"`
	ItemNames map[string]string `json:"itemNames"`
	Contribs  string            `json:"contribs"`
	Reads     []struct {
		Text string `json:"text"`
		Out  struct {
			OK    bool                   `json:"ok"`
			Error string                 `json:"error"`
			Rows  []ImportedContribution `json:"rows"`
		} `json:"out"`
	} `json:"reads"`
	Parses []struct {
		Text string     `json:"text"`
		Out  [][]string `json:"out"`
	} `json:"parses"`
	ICS []struct {
		FundName string   `json:"fundName"`
		Paydays  []string `json:"paydays"`
		Stamp    string   `json:"stamp"`
		Out      string   `json:"out"`
	} `json:"ics"`
	Folders []struct {
		N   string `json:"n"`
		ID  string `json:"id"`
		Out string `json:"out"`
	} `json:"folders"`
	Account struct {
		Bundle struct {
			ExportedAt  string       `json:"exportedAt"`
			Account     AccountUser  `json:"account"`
			Memberships []Membership `json:"memberships"`
			OwnedFunds  []struct {
				Folder         string              `json:"folder"`
				Fund           OwnedFundInfo       `json:"fund"`
				Members        []OwnedMember       `json:"members"`
				PendingInvites []OwnedInvite       `json:"pendingInvites"`
				Items          []OwnedItem         `json:"items"`
				Contributions  []OwnedContribution `json:"contributions"`
			} `json:"ownedFunds"`
		} `json:"bundle"`
		Files map[string]string `json:"files"`
	} `json:"account"`
}

func loadLib(t *testing.T) libFile {
	t.Helper()
	f, err := os.Open("testdata/lib.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var out libFile
	if err := json.NewDecoder(zr).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestParityCSVBuilders(t *testing.T) {
	l := loadLib(t)
	rows := make([]ItemRow, 0, len(l.ItemRows))
	for _, r := range l.ItemRows {
		rows = append(rows, ItemRow{r.Name, r.Price, r.Deadline, r.PlanStart, r.Saved, r.ActualPrice, r.PurchasedOn, r.Archived})
	}
	if got := ItemsCSV(rows); got != l.Items {
		t.Fatalf("itemsCsv differs\nwant %q\ngot  %q", l.Items, got)
	}
	crow := make([]ContributionRow, 0, len(l.ContribRows))
	for _, r := range l.ContribRows {
		lines := make([]AllocationLine, 0)
		for _, a := range r.Allocations {
			lines = append(lines, AllocationLine{a.ItemID, a.Amount})
		}
		crow = append(crow, ContributionRow{r.Date, r.Note, r.Kind, lines})
	}
	got := ContributionsCSV(crow, func(id string) string {
		if n, ok := l.ItemNames[id]; ok {
			return n
		}
		return "Item"
	})
	if got != l.Contribs {
		t.Fatalf("contributionsCsv differs\nwant %q\ngot  %q", l.Contribs, got)
	}
}

func TestParityParseAndRead(t *testing.T) {
	l := loadLib(t)
	for i, p := range l.Parses {
		got := ParseCSV(p.Text)
		if len(got) == 0 && len(p.Out) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, p.Out) {
			t.Fatalf("parseCsv %d differs\nwant %q\ngot  %q", i, p.Out, got)
		}
	}
	for i, r := range l.Reads {
		rows, errMsg := ReadContributionsCSV(r.Text)
		if r.Out.OK {
			if errMsg != "" {
				t.Fatalf("read %d: unexpected error %q (text %.60q)", i, errMsg, r.Text)
			}
			if !reflect.DeepEqual(rows, r.Out.Rows) {
				t.Fatalf("read %d rows differ\nwant %+v\ngot  %+v", i, r.Out.Rows, rows)
			}
		} else if errMsg != r.Out.Error {
			t.Fatalf("read %d: error %q want %q (text %.60q)", i, errMsg, r.Out.Error, r.Text)
		}
	}
}

func TestParityICSAndFolders(t *testing.T) {
	l := loadLib(t)
	for i, c := range l.ICS {
		stamp, err := time.Parse(time.RFC3339, c.Stamp)
		if err != nil {
			t.Fatal(err)
		}
		got := ScheduleICS(c.FundName, c.Paydays, func(d string) string { return "Payday: " + d + `, set aside; "x"` + "\nnext" }, stamp)
		if got != c.Out {
			t.Fatalf("ics %d differs\nwant %q\ngot  %q", i, c.Out, got)
		}
	}
	for _, f := range l.Folders {
		if got := FundExportFolder(f.N, f.ID); got != f.Out {
			t.Errorf("folder(%q,%q) = %q want %q", f.N, f.ID, got, f.Out)
		}
	}
}

func TestParityAccountExport(t *testing.T) {
	l := loadLib(t)
	b := AccountBundle{ExportedAt: l.Account.Bundle.ExportedAt, Account: l.Account.Bundle.Account, Memberships: l.Account.Bundle.Memberships}
	for _, o := range l.Account.Bundle.OwnedFunds {
		b.OwnedFunds = append(b.OwnedFunds, OwnedFund{o.Folder, o.Fund, o.Members, o.PendingInvites, o.Items, o.Contributions})
	}
	files := AccountExportFiles(b)
	if len(files) != len(l.Account.Files) {
		t.Fatalf("file count %d want %d", len(files), len(l.Account.Files))
	}
	for path, want := range l.Account.Files {
		if got := files[path]; got != want {
			t.Errorf("%s differs\nwant %q\ngot  %q", path, want, got)
		}
	}

	data, err := AccountExportZip(b)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(strings.NewReader(string(data)), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != len(files) {
		t.Fatalf("zip has %d files want %d", len(zr.File), len(files))
	}
	for _, f := range zr.File {
		if f.Method != zip.Store {
			t.Errorf("%s not stored", f.Name)
		}
		rc, _ := f.Open()
		body, _ := io.ReadAll(rc)
		rc.Close()
		if string(body) != files[f.Name] {
			t.Errorf("zip content for %s differs", f.Name)
		}
	}
}
