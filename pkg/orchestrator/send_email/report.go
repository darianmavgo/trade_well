package send_email

import (
	"fmt"
	"html"
	"strings"
)

// Row is one line of a job-run report email: what strategy/check produced
// what signal, and what happened to it. Every job renders its run through
// BuildJobReport into this same table shape, so an operator never has to
// relearn a different email format per job.
type Row struct {
	Account  string
	Strategy string
	Symbol   string
	Signal   string
	Status   string
	Detail   string
}

// Summary is the optional header of a job report: a subject suffix and a few
// lines of totals shown above the table, so the inbox line and the first
// screen of the email answer "did it work?" without opening the rows.
type Summary struct {
	SubjectSuffix string   // appended to the job name: "<job> -- <suffix>"
	Lines         []string // one totals line each, rendered above the table
}

// BuildJobReport renders one job run as a single table: one row per
// strategy/signal/outcome. subject is exactly jobName -- no emoji, no
// editorializing -- so the inbox subject line is predictable and every
// job's email has the same shape. rows should include every strategy/check
// evaluated that run, not only the ones that fired, so silence is never
// mistaken for "nothing happened."
func BuildJobReport(jobName string, rows []Row) (subject, htmlBody string) {
	return BuildJobReportSummary(jobName, Summary{}, rows)
}

// BuildJobReportSummary is BuildJobReport with a factual totals header. The
// subject still starts with the bare job name, so inbox filters on it keep
// working; an empty Summary renders exactly what BuildJobReport always did.
func BuildJobReportSummary(jobName string, sum Summary, rows []Row) (subject, htmlBody string) {
	subject = jobName
	if sum.SubjectSuffix != "" {
		subject = jobName + " -- " + sum.SubjectSuffix
	}

	var sb strings.Builder
	sb.WriteString(`<html><body style="margin:0;background:#fff;color:#1c1f24;font:15px/1.55 -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Arial,sans-serif;">`)
	sb.WriteString(`<div style="max-width:1100px;margin:0 auto;padding:24px 16px;">`)
	fmt.Fprintf(&sb, `<h1 style="font-size:26px;margin:0 0 4px;">%s</h1>`, html.EscapeString(jobName))
	if sum.SubjectSuffix != "" {
		fmt.Fprintf(&sb, `<p style="color:#5b6470;margin:0 0 16px;">%s</p>`, html.EscapeString(sum.SubjectSuffix))
	}
	lines := sum.Lines
	if len(rows) > 0 {
		lines = append(append([]string{}, lines...), fmt.Sprintf("%d row(s)", len(rows)))
	}
	if len(lines) > 0 {
		sb.WriteString(`<div style="background:#f6f8fa;border:1px solid #dde1e6;border-radius:8px;padding:12px 16px;margin:12px 0;">`)
		for i, l := range lines {
			if i > 0 {
				sb.WriteString(`<br>`)
			}
			sb.WriteString(html.EscapeString(l))
		}
		sb.WriteString(`</div>`)
	}

	if len(rows) == 0 {
		sb.WriteString(`<p>No rows to report for this run.</p></div></body></html>`)
		return subject, sb.String()
	}

	sb.WriteString(`<table style="width:100%;border-collapse:collapse;margin:8px 0 4px;font-size:14px;">`)
	sb.WriteString(`<tr>`)
	for _, h := range []string{"Account", "Strategy", "Symbol", "Signal", "Status", "Detail"} {
		fmt.Fprintf(&sb, `<th style="text-align:left;padding:7px 10px;border-bottom:1px solid #dde1e6;color:#5b6470;font-weight:600;">%s</th>`, h)
	}
	sb.WriteString(`</tr>`)

	const td = `<td style="padding:7px 10px;border-bottom:1px solid #dde1e6;vertical-align:top;">`
	for _, r := range rows {
		sb.WriteString(`<tr>`)
		for _, v := range []string{r.Account, r.Strategy, r.Symbol, r.Signal} {
			sb.WriteString(td + html.EscapeString(v) + `</td>`)
		}
		fmt.Fprintf(&sb, `%s<span style="display:inline-block;font-size:12px;font-weight:600;padding:1px 8px;border-radius:10px;border:1px solid %[2]s;color:%[2]s;white-space:nowrap;">%s</span></td>`,
			td, statusColor(r.Status), html.EscapeString(r.Status))
		sb.WriteString(td + html.EscapeString(r.Detail) + `</td></tr>`)
	}
	sb.WriteString(`</table></div></body></html>`)
	return subject, sb.String()
}

// statusColor is the text colour of a status pill, using the same palette as the /ready page:
// red for anything that failed or needs attention, green for a clean success, amber for routine
// skips and warnings, grey for a plain "nothing happened" row.
func statusColor(status string) string {
	switch rowColor(status) {
	case "#e6f4ea":
		return "#1a7f37"
	case "#fdecea":
		return "#c62828"
	case "#fff8e1":
		return "#9a6700"
	}
	return "#5b6470"
}

// rowColor gives a quick visual triage cue without relying on an
// emoji-laden subject line: red for anything that failed or needs
// attention, green for a clean success, amber for routine skips/warnings,
// white for a plain "nothing happened" row.
func rowColor(status string) string {
	switch strings.ToUpper(status) {
	case "SUBMITTED", "STAGED", "CLOSED_PROFIT", "HEALTHY":
		return "#e6f4ea"
	case "ERROR", "REJECTED", "DUPLICATE_REJECTED", "SAFETY_REJECTED", "FAILED", "FAILED_SUBMIT",
		"FAILED_CLAIM", "CANCELED_UNFILLED", "ERROR_BUILD_ORDER", "NO_DATA", "CRITICAL", "ACTION_REQUIRED",
		"RISK_UNREADABLE", "PARTIAL_FILL_ACTION_NEEDED":
		return "#fdecea"
	case "WARNING", "SKIPPED", "UNTRACKED", "CLOSED_TIME_EXPIRATION",
		"SETTLED_LATE", "SUBMITTED_PENDING_FILL", "SUBMITTING", "PENDING", "CLAIM_LOST",
		"RISK_HALTED", "RISK_LIMIT":
		return "#fff8e1"
	default:
		return "#ffffff"
	}
}
