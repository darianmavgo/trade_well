package appserve

import (
	"bytes"
	"context"
	"regexp"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/cronclient"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/ready"
)

// readyEmailHTML renders the /ready page for an email: the same template and the same data, so
// the message looks like the page. Email clients (Gmail above all) ignore CSS variables and
// dark-mode media queries, so emailCSS bakes the light palette into literal colours, and the
// page's relative links are pointed at the deployed service.
func readyEmailHTML(ctx context.Context, rep ready.Report) (string, error) {
	var buf bytes.Buffer
	err := readyPage.Execute(&buf, pageData{
		Title: "Ready: " + strings.ToUpper(rep.Status),
		Sub:   "Will tonight's scan and tomorrow's orders work?",
		D:     rep,
		Email: true,
	})
	if err != nil {
		return "", err
	}
	out := buf.String()
	if base, berr := cronclient.BaseURL(ctx); berr == nil {
		out = absoluteLinks(out, base)
	}
	return out, nil
}

// absoluteLinks points every root-relative href ("/ops") at base.
func absoluteLinks(page, base string) string {
	return strings.ReplaceAll(page, `href="/`, `href="`+strings.TrimRight(base, "/")+`/`)
}

var (
	darkMode = regexp.MustCompile(`(?s)@media \(prefers-color-scheme: dark\) \{.*?\n  \}`)
	cssVar   = regexp.MustCompile(`--([a-z]+):\s*([^;]+);`)
	cssRef   = regexp.MustCompile(`var\(--([a-z]+)\)`)
)

// emailCSS turns the page stylesheet into one an email client renders: the dark-mode block is
// dropped and every var(--name) becomes the light palette's literal value.
func emailCSS(css string) string {
	css = darkMode.ReplaceAllString(css, "")
	vals := map[string]string{}
	for _, m := range cssVar.FindAllStringSubmatch(css, -1) {
		vals[m[1]] = strings.TrimSpace(m[2])
	}
	return cssRef.ReplaceAllStringFunc(css, func(ref string) string {
		if v, ok := vals[cssRef.FindStringSubmatch(ref)[1]]; ok {
			return v
		}
		return ref
	})
}
