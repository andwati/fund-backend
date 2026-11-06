package export

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

type AccountUser struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"emailVerified"`
	CreatedAt     string `json:"createdAt"`
}

type Membership struct {
	FundID            string `json:"fundId"`
	Name              string `json:"name"`
	Role              string `json:"role"`
	Paydays           []int  `json:"paydays"`
	Pace              *int   `json:"pace"`
	Currency          string `json:"currency"`
	Timezone          string `json:"timezone"`
	TimezoneConfirmed bool   `json:"timezoneConfirmed"`
	RemindersEnabled  bool   `json:"remindersEnabled"`
	JoinedAt          string `json:"joinedAt"`
}

type OwnedFundInfo struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Paydays           []int  `json:"paydays"`
	Pace              *int   `json:"pace"`
	Currency          string `json:"currency"`
	Timezone          string `json:"timezone"`
	TimezoneConfirmed bool   `json:"timezoneConfirmed"`
	RemindersEnabled  bool   `json:"remindersEnabled"`
	CreatedAt         string `json:"createdAt"`
}

type OwnedMember struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Role   string `json:"role"`
}

type OwnedInvite struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	ExpiresAt string `json:"expiresAt"`
}

type OwnedItem struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Price       int     `json:"price"`
	Deadline    string  `json:"deadline"`
	PlanStart   string  `json:"planStart"`
	Position    int     `json:"position"`
	Saved       int     `json:"saved"`
	ActualPrice *int    `json:"actualPrice"`
	PurchasedOn *string `json:"purchasedOn"`
	Archived    bool    `json:"archived"`
}

type OwnedAllocation struct {
	ID     string `json:"id"`
	ItemID string `json:"itemId"`
	Amount int    `json:"amount"`
}

type OwnedContribution struct {
	ID          string            `json:"id"`
	Date        string            `json:"date"`
	Note        string            `json:"note"`
	Kind        string            `json:"kind"`
	Allocations []OwnedAllocation `json:"allocations"`
}

type OwnedFund struct {
	Folder         string
	Fund           OwnedFundInfo
	Members        []OwnedMember
	PendingInvites []OwnedInvite
	Items          []OwnedItem
	Contributions  []OwnedContribution
}

type AccountBundle struct {
	ExportedAt  string
	Account     AccountUser
	Memberships []Membership
	OwnedFunds  []OwnedFund
}

// jsonLine is JSON.stringify(value, null, 2) plus a newline. HTML characters are not escaped.
func jsonLine(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return buf.String()
}

// fundFile is the spread `{...fund, members, pendingInvites}` from the web app, in order.
type fundFile struct {
	OwnedFundInfo
	Members        []OwnedMember `json:"members"`
	PendingInvites []OwnedInvite `json:"pendingInvites"`
}

// AccountExportFiles returns the files of the account export keyed by path.
func AccountExportFiles(b AccountBundle) map[string]string {
	files := map[string]string{
		"README.txt": "Fund account export. JSON holds structured data; CSV matches per-fund downloads.\n" +
			"Other people's email addresses are omitted on purpose.\n",
		"account.json": jsonLine(struct {
			ExportedAt string      `json:"exportedAt"`
			User       AccountUser `json:"user"`
		}{b.ExportedAt, b.Account}),
		"memberships.json": jsonLine(struct {
			Memberships []Membership `json:"memberships"`
		}{nonNil(b.Memberships)}),
	}
	for _, e := range b.OwnedFunds {
		base := "owned/" + e.Folder
		names := map[string]string{}
		for _, it := range e.Items {
			names[it.ID] = it.Name
		}
		files[base+"/fund.json"] = jsonLine(fundFile{e.Fund, nonNil(e.Members), nonNil(e.PendingInvites)})
		files[base+"/items.json"] = jsonLine(struct {
			Items []OwnedItem `json:"items"`
		}{nonNil(e.Items)})
		rows := make([]ItemRow, 0, len(e.Items))
		for _, it := range e.Items {
			rows = append(rows, ItemRow{it.Name, it.Price, it.Deadline, it.PlanStart, it.Saved, it.ActualPrice, it.PurchasedOn, it.Archived})
		}
		files[base+"/items.csv"] = ItemsCSV(rows)
		contribs := nonNil(e.Contributions)
		for i := range contribs {
			contribs[i].Allocations = nonNil(contribs[i].Allocations)
		}
		files[base+"/contributions.json"] = jsonLine(struct {
			Contributions []OwnedContribution `json:"contributions"`
		}{contribs})
		crow := make([]ContributionRow, 0, len(e.Contributions))
		for _, c := range e.Contributions {
			lines := make([]AllocationLine, 0, len(c.Allocations))
			for _, a := range c.Allocations {
				lines = append(lines, AllocationLine{a.ItemID, a.Amount})
			}
			crow = append(crow, ContributionRow{c.Date, c.Note, c.Kind, lines})
		}
		files[base+"/contributions.csv"] = ContributionsCSV(crow, func(id string) string {
			if n, ok := names[id]; ok {
				return n
			}
			return "Item"
		})
	}
	return files
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// AccountExportZip stores (no compression) every file of the export.
func AccountExportZip(b AccountBundle) ([]byte, error) {
	files := AccountExportFiles(b)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range paths {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: p, Method: zip.Store})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(files[p])); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var _ = strings.TrimSpace
