package dashboard

import (
	"fmt"
	"html"
	"io"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_state"
)

const schwabStatePageCSS = `
:root {
    --bg-primary: #0a0e17;
    --bg-card: #111827;
    --border-color: #243247;
    --text-primary: #f1f5f9;
    --text-secondary: #94a3b8;
    --text-muted: #64748b;
    --accent-cyan: #38bdf8;
    --accent-red: #ef4444;
    --font-mono: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
    --font-sans: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
}
* { box-sizing: border-box; margin: 0; padding: 0; }
body {
    background-color: var(--bg-primary);
    color: var(--text-primary);
    font-family: var(--font-sans);
    line-height: 1.4;
    padding: 24px;
    padding-bottom: 80px;
}
header {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 12px;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--border-color);
    margin-bottom: 24px;
}
header h1 { font-size: 1.4rem; font-weight: 700; }
header .meta { color: var(--text-muted); font-size: 0.85rem; }
header a { color: var(--accent-cyan); }
.status { color: var(--text-muted); font-size: 0.85rem; font-family: var(--font-mono); margin-bottom: 12px; }
.error-note { color: var(--accent-red); font-family: var(--font-mono); padding: 12px 16px; margin-bottom: 16px; background: var(--bg-card); border: 1px solid var(--accent-red); border-radius: 8px; white-space: pre-wrap; }
.table-section {
    background: var(--bg-card);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    margin-bottom: 24px;
    overflow: hidden;
}
.table-section h2 {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 8px;
    padding: 12px 16px;
    font-size: 1rem;
    border-bottom: 1px solid var(--border-color);
    background: #0d1420;
}
.table-name { font-family: var(--font-mono); color: var(--accent-cyan); }
.table-count { color: var(--text-secondary); font-weight: 400; font-size: 0.8rem; }
.table-scroll { overflow-x: auto; max-height: 480px; overflow-y: auto; }
table.data {
    width: 100%;
    border-collapse: collapse;
    font-family: var(--font-mono);
    font-size: 0.8rem;
    white-space: nowrap;
}
table.data th {
    position: sticky;
    top: 0;
    background: #0d1420;
    text-align: left;
    padding: 8px 12px;
    border-bottom: 1px solid var(--border-color);
    color: var(--text-secondary);
}
table.data td {
    padding: 6px 12px;
    border-bottom: 1px solid #1b2637;
    color: var(--text-primary);
}
.empty-note { padding: 16px; color: var(--text-muted); font-size: 0.85rem; }
`

func writeAccountStateOpen(w io.Writer, targets []string) {
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>account_state — Schwab mirror</title>
<style>%s
    .auth-needed { margin: 1rem 0; padding: 1rem 1.25rem; border: 2px solid #b45309; background: #fffbeb; border-radius: 8px; max-width: 42rem; }
    .auth-btn { display: inline-block; margin: 0.5rem 0; padding: 0.6rem 1rem; background: #b45309; color: #fff; text-decoration: none; border-radius: 6px; font-weight: 600; }
    .auth-btn:hover { background: #92400e; }
</style>
</head>
<body>
<header>
  <div>
    <h1>account_state — Schwab mirror</h1>
    <div class="meta">Configured accounts: %s. Tables appear as each Schwab response returns.</div>
  </div>
  <a href="/">← Job state</a>
</header>
`, schwabStatePageCSS, html.EscapeString(strings.Join(targets, ", ")))
}

func writeAccountStateStatus(w io.Writer, msg string) {
	fmt.Fprintf(w, `<p class="status">%s</p>`+"\n", html.EscapeString(msg))
}

func writeAccountStateError(w io.Writer, msg string) {
	fmt.Fprintf(w, `<p class="error-note">%s</p>
`, html.EscapeString(msg))
	low := strings.ToLower(msg)
	if strings.Contains(low, "re-authenticate") || strings.Contains(low, "re-authentication") ||
		strings.Contains(low, "invalid_grant") || strings.Contains(low, "unsupported_token_type") ||
		strings.Contains(low, "refresh token") {
		fmt.Fprint(w, `<div class="auth-needed">
  <p><b>Refresh token is dead</b> — automatic access-token refresh already tried and cannot help.</p>
  <p><a class="auth-btn" href="/auth/schwab">Re-authenticate with Schwab</a></p>
  <p class="status">Schwab only knows two redirect URLs, both pointing at the live GAE
  instance — re-authenticating always finishes there, even when you click this from a
  local dashboard. Once it publishes the new token to GCS, reload this page
  and the local process will pull it down automatically.</p>
  <p class="status">Or locally: <code>orchestrator token pull</code>, then reload this page.</p>
</div>
`)
	}
}

func writeAccountStateTable(w io.Writer, t *account_state.Table) {
	if t == nil {
		return
	}
	n := 0
	if t.Rows != nil {
		n = len(t.Rows)
	}
	fmt.Fprintf(w, `<section class="table-section"><h2><span><span class="table-name">%s</span> — %s</span><span class="table-count">%d row(s)</span></h2>`,
		html.EscapeString(t.Name), html.EscapeString(t.Label), n)
	if n == 0 {
		fmt.Fprint(w, `<div class="empty-note">No rows.</div></section>`+"\n")
		return
	}
	fmt.Fprint(w, `<div class="table-scroll"><table class="data"><thead><tr>`)
	for _, col := range t.Columns {
		fmt.Fprintf(w, `<th>%s</th>`, html.EscapeString(col))
	}
	fmt.Fprint(w, `</tr></thead><tbody>`)
	for _, row := range t.Rows {
		fmt.Fprint(w, `<tr>`)
		for _, cell := range row {
			fmt.Fprintf(w, `<td>%s</td>`, html.EscapeString(cell))
		}
		fmt.Fprint(w, `</tr>`)
	}
	fmt.Fprint(w, `</tbody></table></div></section>`+"\n")
}

func writeAccountStateClose(w io.Writer) {
	fmt.Fprint(w, `</body></html>`+"\n")
}
