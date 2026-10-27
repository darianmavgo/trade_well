// Package weekly_digest builds the Saturday summary email: the signals the scans found, the
// orders staged and submitted, how every scheduled job did, the open positions, and where the
// readiness checks stand. It only reads the orchestrator database.
package weekly_digest

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"io"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/ready"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/send_email"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
)

// JobName is the cron job, CLI subcommand and job_run name.
const JobName = "weekly_digest"

// Count is a label and how many.
type Count struct {
	Label string `json:"label"`
	N     int    `json:"n"`
}

// JobWeek is one job's runs in the week.
type JobWeek struct {
	Job    string `json:"job"`
	OK     int    `json:"ok"`
	Empty  int    `json:"empty"`
	Failed int    `json:"failed"`
	Latest string `json:"latest_failure,omitempty"`
}

// PositionRow is an open position.
type PositionRow struct {
	Account    string  `json:"account"`
	Symbol     string  `json:"symbol"`
	Quantity   float64 `json:"quantity"`
	Value      float64 `json:"market_value"`
	Unrealized float64 `json:"unrealized"`
	ExitDate   string  `json:"exit_date,omitempty"`
}

// Digest is the week.
type Digest struct {
	From, To  string
	Buys      []scan_market.BuyRow
	NoBuys    int
	Staged    []Count
	Submitted []Count
	Jobs      []JobWeek
	Positions []PositionRow
	Ready     ready.Report
}

// Build reads the week ending at now. rep is the readiness report to include, and deployed the
// strategy ids in the live allowlist: signals of strategies deployed earlier are left out.
func Build(db *sql.DB, now time.Time, rep ready.Report, deployed []string) (Digest, error) {
	d := Digest{To: now.UTC().Format("2006-01-02"), From: now.UTC().AddDate(0, 0, -7).Format("2006-01-02"), Ready: rep}
	res, err := scan_market.RecentFor(db, now, 7, deployed)
	if err != nil {
		return d, err
	}
	d.Buys, d.NoBuys = res.Buys, len(res.NoBuys)

	since := now.UTC().AddDate(0, 0, -7).Format(time.RFC3339)
	if d.Staged, err = counts(db, `SELECT status, COUNT(*) FROM stage_order WHERE staged_at >= ? GROUP BY status ORDER BY status`, since); err != nil {
		return d, err
	}
	if d.Submitted, err = counts(db, `SELECT status, COUNT(*) FROM submit_order WHERE submitted_at >= ? GROUP BY status ORDER BY status`, since); err != nil {
		return d, err
	}

	rows, err := db.Query(`SELECT job, status, COUNT(*), MAX(CASE WHEN status = 'FAILED' THEN summary ELSE '' END)
FROM job_run WHERE finished_at >= ? GROUP BY job, status ORDER BY job`, since)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	byJob := map[string]*JobWeek{}
	var order []string
	for rows.Next() {
		var job, status, fail string
		var n int
		if err := rows.Scan(&job, &status, &n, &fail); err != nil {
			return d, err
		}
		w := byJob[job]
		if w == nil {
			w = &JobWeek{Job: job}
			byJob[job] = w
			order = append(order, job)
		}
		switch status {
		case "OK":
			w.OK += n
		case "EMPTY":
			w.Empty += n
		case "FAILED":
			w.Failed += n
			w.Latest = fail
		}
	}
	if err := rows.Err(); err != nil {
		return d, err
	}
	for _, j := range order {
		d.Jobs = append(d.Jobs, *byJob[j])
	}

	pos, err := db.Query(`SELECT account_number, symbol, COALESCE(long_quantity, 0) - COALESCE(short_quantity, 0),
       COALESCE(market_value, 0), COALESCE(market_value, 0) - (COALESCE(long_quantity, 0) - COALESCE(short_quantity, 0)) * COALESCE(average_price, 0),
       COALESCE(exit_date, '')
FROM account_state_position WHERE COALESCE(long_quantity, 0) <> 0 OR COALESCE(short_quantity, 0) <> 0 ORDER BY market_value DESC`)
	if err != nil {
		return d, err
	}
	defer pos.Close()
	for pos.Next() {
		var p PositionRow
		if err := pos.Scan(&p.Account, &p.Symbol, &p.Quantity, &p.Value, &p.Unrealized, &p.ExitDate); err != nil {
			return d, err
		}
		d.Positions = append(d.Positions, p)
	}
	return d, pos.Err()
}

func counts(db *sql.DB, query string, args ...any) ([]Count, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Count
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Label, &c.N); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Summary is the one-line outcome the job_run row keeps.
func (d Digest) Summary() string {
	failed := 0
	for _, j := range d.Jobs {
		failed += j.Failed
	}
	return fmt.Sprintf("weekly_digest %s..%s buys=%d no_buy=%d job_failures=%d positions=%d ready=%s",
		d.From, d.To, len(d.Buys), d.NoBuys, failed, len(d.Positions), d.Ready.Status)
}

func masked(acct string) string {
	if len(acct) > 4 {
		return "…" + acct[len(acct)-4:]
	}
	return acct
}

// Text is the digest as plain text.
func (d Digest) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Weekly digest %s to %s\n\n", d.From, d.To)
	fmt.Fprintf(&b, "Signals: %d buy candidate(s), %d no-buy note(s)\n", len(d.Buys), d.NoBuys)
	for _, g := range groupBuys(d.Buys) {
		fmt.Fprintf(&b, "  %s %s: %d candidate(s) -> %s: %s\n", g.AsOf, g.StrategyID, len(g.Symbols), g.Status, symbolList(g.Symbols))
	}
	writeCounts(&b, "Orders staged", d.Staged)
	writeCounts(&b, "Orders submitted", d.Submitted)
	b.WriteString("\nJobs (runs in the week)\n")
	for _, j := range d.Jobs {
		fmt.Fprintf(&b, "  %-16s ok %d, empty %d, failed %d", j.Job, j.OK, j.Empty, j.Failed)
		if j.Latest != "" {
			fmt.Fprintf(&b, "  last failure: %s", j.Latest)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\nOpen positions: %d\n", len(d.Positions))
	for _, p := range d.Positions {
		fmt.Fprintf(&b, "  %s %-6s qty %.0f value $%.2f unrealized %+.2f exit %s\n", masked(p.Account), p.Symbol, p.Quantity, p.Value, p.Unrealized, p.ExitDate)
	}
	b.WriteString("\n" + ready.Text(d.Ready))
	return b.String()
}

type buyGroup struct {
	AsOf, StrategyID, Status string
	Symbols                  []string
}

// groupBuys folds candidates into one line per bar date, strategy and status, in the order
// they came (newest first), so a strategy that fires on 40 symbols is one line.
func groupBuys(buys []scan_market.BuyRow) []buyGroup {
	var out []buyGroup
	index := map[string]int{}
	for _, b := range buys {
		key := b.AsOf + "|" + b.StrategyID + "|" + b.Status
		i, ok := index[key]
		if !ok {
			i = len(out)
			index[key] = i
			out = append(out, buyGroup{AsOf: b.AsOf, StrategyID: b.StrategyID, Status: b.Status})
		}
		out[i].Symbols = append(out[i].Symbols, b.Symbol)
	}
	return out
}

func symbolList(syms []string) string {
	const show = 8
	if len(syms) <= show {
		return strings.Join(syms, " ")
	}
	return strings.Join(syms[:show], " ") + fmt.Sprintf(" … +%d more", len(syms)-show)
}

func writeCounts(b *strings.Builder, title string, cs []Count) {
	fmt.Fprintf(b, "\n%s:", title)
	if len(cs) == 0 {
		b.WriteString(" none\n")
		return
	}
	for _, c := range cs {
		fmt.Fprintf(b, " %s=%d", c.Label, c.N)
	}
	b.WriteString("\n")
}

// HTML is the digest as an email body: the text in a preformatted block, so it reads the same
// in a terminal and an inbox.
func (d Digest) HTML() string {
	return "<pre style=\"font:13px/1.5 ui-monospace,Menlo,monospace\">" + html.EscapeString(d.Text()) + "</pre>"
}

// JobOptions are the inputs of RunJob.
type JobOptions struct {
	Ready ready.Options
	Email bool // false prints the digest and sends nothing (--dry-run)
}

// RunJob is the `weekly_digest` job: build the week, print it, and email it unless Email is false.
// The first line printed is the outcome the job_run row keeps.
func RunJob(ctx context.Context, db *sql.DB, out io.Writer, o JobOptions) (Digest, error) {
	now := o.Ready.Now
	if now.IsZero() {
		now = time.Now()
	}
	o.Ready.DB = db
	d, err := Build(db, now, ready.Run(ctx, o.Ready), strategies.Members(o.Ready.Allowlist))
	if err != nil {
		return d, err
	}
	fmt.Fprintf(out, "OK %s\n\n%s", d.Summary(), d.Text())
	if o.Email {
		if err := send_email.SendHTMLEmail(db, JobName, "Weekly digest "+d.To, d.HTML()); err != nil {
			return d, fmt.Errorf("weekly digest not emailed: %w", err)
		}
	}
	return d, nil
}
