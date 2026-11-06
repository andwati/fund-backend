// Package mail renders and sends the transactional emails. Copy, subjects and structure mirror
// ~/dev/fund/emails/* (React Email) so recipients see the same messages.
package mail

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
)

// Part is a run of paragraph text; Bold marks addresses and similar emphasis.
type Part struct {
	Text string
	Bold bool
}

func T(s string) Part { return Part{Text: s} }
func B(s string) Part { return Part{Text: s, Bold: true} }

// Para is one paragraph made of parts.
type Para []Part

func P(parts ...Part) Para { return Para(parts) }

// Content is everything a message shows.
type Content struct {
	Preview    string
	Kicker     string // small uppercase line above the heading; empty hides it
	Heading    string
	Paragraphs []Para
	Button     string // empty hides the button
	URL        string
	Rule       bool // horizontal rule before the footnotes
	Footnotes  []Para
	// FootnoteLink renders "label" as a link inside the last footnote line (unsubscribe).
	FootLabel string
	FootURL   string
}

const (
	colBrand  = "#0A6847"
	colCanvas = "#F2F5F3"
	colInk    = "#12201B"
	colMuted  = "#5A6B64"
	colLine   = "#D3DCD7"
)

var htmlTmpl = template.Must(template.New("layout").Funcs(template.FuncMap{
	"para": func(p Para) template.HTML {
		var b strings.Builder
		for _, part := range p {
			esc := template.HTMLEscapeString(part.Text)
			if part.Bold {
				b.WriteString("<strong>" + esc + "</strong>")
			} else {
				b.WriteString(esc)
			}
		}
		return template.HTML(b.String())
	},
}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;background:` + colCanvas + `;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0">{{.Preview}}</div>
<div style="max-width:576px;margin:32px auto;background:#ffffff;border:1px solid ` + colLine + `;padding:32px;">
{{if .Kicker}}<p style="margin:0;font-size:12px;letter-spacing:.1em;text-transform:uppercase;color:` + colMuted + `;">{{.Kicker}}</p>{{end}}
<h1 style="margin:8px 0 16px;font-size:24px;font-weight:600;color:` + colInk + `;">{{.Heading}}</h1>
{{range .Paragraphs}}<p style="margin:16px 0;font-size:16px;line-height:28px;color:` + colInk + `;">{{para .}}</p>
{{end}}{{if .Button}}<a href="{{.URL}}" style="display:block;background:` + colBrand + `;color:#ffffff;text-align:center;font-weight:600;text-decoration:none;padding:12px 20px;">{{.Button}}</a>
{{end}}{{if .Rule}}<hr style="margin:24px 0;border:0;border-top:1px solid ` + colLine + `;">{{end}}
{{range .Footnotes}}<p style="margin:16px 0 0;font-size:14px;line-height:24px;color:` + colMuted + `;">{{para .}}</p>
{{end}}{{if .FootURL}}<p style="margin:16px 0 0;font-size:14px;line-height:24px;color:` + colMuted + `;"><a href="{{.FootURL}}" style="color:` + colMuted + `;text-decoration:underline;">{{.FootLabel}}</a></p>
{{end}}</div></body></html>`))

func plain(p Para) string {
	var b strings.Builder
	for _, part := range p {
		b.WriteString(part.Text)
	}
	return b.String()
}

// Render returns the HTML and plain-text bodies for c.
func Render(c Content) (htmlBody, textBody string, err error) {
	var buf bytes.Buffer
	if err := htmlTmpl.Execute(&buf, c); err != nil {
		return "", "", err
	}
	var t strings.Builder
	if c.Kicker != "" {
		fmt.Fprintf(&t, "%s\n\n", strings.ToUpper(c.Kicker))
	}
	fmt.Fprintf(&t, "%s\n\n", c.Heading)
	for _, p := range c.Paragraphs {
		fmt.Fprintf(&t, "%s\n\n", plain(p))
	}
	if c.Button != "" {
		fmt.Fprintf(&t, "%s: %s\n\n", c.Button, c.URL)
	}
	for _, p := range c.Footnotes {
		fmt.Fprintf(&t, "%s\n\n", plain(p))
	}
	if c.FootURL != "" {
		fmt.Fprintf(&t, "%s: %s\n", c.FootLabel, c.FootURL)
	}
	return buf.String(), strings.TrimRight(t.String(), "\n") + "\n", nil
}
