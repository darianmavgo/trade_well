// Package trade_history keeps a record of the trades that actually happened, and the live win rate
// that record gives each strategy.
//
// trade_history holds one row per closed round trip in one account: the strategy it is attributed
// to, its entry order id and its exit order id, the fills, and the P&L. Capture copies them in from
// the positions manage_exits closed, and only those that really traded (both fills recorded, the
// broker mirror not contradicting the entry); a trade already captured is left as it was.
//
// Go opens the database and runs the SQL files in sql/; every number is calculated there, one view
// per stage:
//
//	trade_history_candidate      the closed positions that really traded (what Capture copies)
//	trade_history_decision       one row per strategy buying one symbol on one day
//	trade_history_running_count  each decision in closing order, with the count and wins so far
//	trade_history_running        the ongoing win rate and its 95% interval after each decision
//	trade_history_win_rate       where each strategy stands now (its newest running row)
//	live_vs_backtest             that live rate against the stored backtest rates, with a verdict
//
// The views read their tables when queried, so they follow trade_history as trades are captured.
package trade_history

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// JobName is the CLI subcommand, and the log prefix.
const JobName = "trade_history"

// views are the view files, one stage each.
var views = []string{
	"view_candidate", "view_decision", "view_running_count", "view_running", "view_win_rate", "view_live_vs_backtest",
}

// Verdicts, as in the win-rate comparison.
const (
	NoBacktest = "NO_BACKTEST"
	TooEarly   = "TOO_EARLY"
	Below      = "BELOW"
	Above      = "ABOVE"
	InLine     = "IN_LINE"
)

// file returns one of this package's stage files, sql/orchestrator/trade_history/<name>.sql.
func file(name string) string { return orchestratorsql.File("trade_history", name) }

// statement is a one-statement file without its leading comment lines, so it can be sent as it is.
func statement(name string) string {
	var out []string
	for line := range strings.SplitSeq(file(name), "\n") {
		if len(out) == 0 && strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// viewParts splits a view file into the view's name, its DROP VIEW statement and its CREATE VIEW
// statement exactly as SQLite stores it (from CREATE to the end, without the closing semicolon),
// so a stored view can be compared with it.
func viewParts(name string) (view, drop, create string) {
	text := file(name)
	i := strings.Index(text, "CREATE VIEW ")
	create = strings.TrimSuffix(strings.TrimSpace(text[i:]), ";")
	view = strings.Fields(create)[2]
	for line := range strings.SplitSeq(text[:i], "\n") {
		if strings.HasPrefix(line, "DROP VIEW") {
			drop = strings.TrimSuffix(strings.TrimSpace(line), ";")
		}
	}
	return view, drop, create
}

// EnsureSchema creates trade_history and its views. A view whose stored definition differs from the
// one in this build is dropped and created again; one that matches is left alone.
func EnsureSchema(db *sql.DB) error {
	if _, err := db.Exec(file("table")); err != nil {
		return fmt.Errorf("trade_history: table: %w", err)
	}
	for _, f := range views {
		view, drop, create := viewParts(f)
		var have string
		err := db.QueryRow(statement("view_definition"), view).Scan(&have)
		switch {
		case err == sql.ErrNoRows:
		case err != nil:
			return fmt.Errorf("trade_history: view %s: %w", view, err)
		case strings.TrimSpace(strings.ReplaceAll(have, "\r", "")) == create:
			continue
		default:
			if _, err := db.Exec(drop); err != nil {
				return fmt.Errorf("trade_history: drop view %s: %w", view, err)
			}
		}
		if _, err := db.Exec(create); err != nil {
			return fmt.Errorf("trade_history: create view %s: %w", view, err)
		}
	}
	return nil
}

// Result is what one Capture did.
type Result struct {
	// Ready is false when trade_history or its views do not exist yet (a dry run never creates them).
	Ready bool
	// New is the number of trades that were not in trade_history: captured, or with DryRun, found.
	New int
	// Total is the number of trades in trade_history afterwards (before, with DryRun).
	Total  int
	DryRun bool
}

// Capture copies the trades that actually happened, and are not in trade_history yet, into it.
// With dryRun nothing is created or written: it counts what a real run would add, and reports
// Ready false if trade_history or its views are not there yet.
func Capture(ctx context.Context, db *sql.DB, dryRun bool) (Result, error) {
	res := Result{DryRun: dryRun}
	if dryRun {
		var have int
		if err := db.QueryRowContext(ctx, statement("exists")).Scan(&have); err != nil {
			return res, err
		}
		if have < 2 {
			return res, nil
		}
	} else if err := EnsureSchema(db); err != nil {
		return res, err
	}
	res.Ready = true
	if err := db.QueryRowContext(ctx, statement("count_new")).Scan(&res.New); err != nil {
		return res, fmt.Errorf("trade_history: count new trades: %w", err)
	}
	if !dryRun && res.New > 0 {
		if _, err := db.ExecContext(ctx, statement("capture"), time.Now().UTC().Format(time.RFC3339)); err != nil {
			return res, fmt.Errorf("trade_history: capture: %w", err)
		}
	}
	if err := db.QueryRowContext(ctx, statement("count_all")).Scan(&res.Total); err != nil {
		return res, err
	}
	return res, nil
}

// Row is one strategy of the live_vs_backtest view. Win rates are fractions 0 to 1; nil means no data.
type Row struct {
	Strategy      string
	ISWinRate     *float64
	ISTrades      int
	OOSWinRate    *float64
	OOSTrades     int
	AllWinRate    *float64
	AllTrades     int
	BaseWinRate   *float64
	BaseKind      string
	LiveTrades    int
	LiveWins      int
	LiveWinRate   *float64
	LiveCILow     *float64
	LiveCIHigh    *float64
	FirstClose    string
	LastClose     string
	LiveMinusBase *float64
	Verdict       string
}

// LiveVsBacktest reads the live_vs_backtest view, worst verdict first.
func LiveVsBacktest(ctx context.Context, db *sql.DB) ([]Row, error) {
	rows, err := db.QueryContext(ctx, statement("read_live_vs_backtest"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var is, oos, all, base, lw, lo, hi, diff sql.NullFloat64
		if err := rows.Scan(&r.Strategy, &is, &r.ISTrades, &oos, &r.OOSTrades, &all, &r.AllTrades,
			&base, &r.BaseKind, &r.LiveTrades, &r.LiveWins, &lw, &lo, &hi, &r.FirstClose, &r.LastClose, &diff, &r.Verdict); err != nil {
			return nil, err
		}
		r.ISWinRate, r.OOSWinRate, r.AllWinRate, r.BaseWinRate = ptr(is), ptr(oos), ptr(all), ptr(base)
		r.LiveWinRate, r.LiveCILow, r.LiveCIHigh, r.LiveMinusBase = ptr(lw), ptr(lo), ptr(hi), ptr(diff)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Format writes the live_vs_backtest view as a text table for the CLI.
func Format(w io.Writer, rows []Row) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "STRATEGY\tBACKTEST\tBASIS\tLIVE\tLIVE 95%\tLIVE-BACKTEST\tLAST CLOSE\tVERDICT")
	for _, r := range rows {
		diff := "-"
		if r.LiveMinusBase != nil {
			diff = fmt.Sprintf("%+.0f pts", *r.LiveMinusBase*100)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Strategy,
			percent(r.BaseWinRate), orDash(r.BaseKind), cell(r.LiveWinRate, r.LiveTrades),
			interval(r.LiveCILow, r.LiveCIHigh), diff, orDash(r.LastClose), r.Verdict)
	}
	tw.Flush()
}

func ptr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	return &n.Float64
}

func percent(p *float64) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", *p*100)
}

func cell(rate *float64, n int) string {
	if rate == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f%% (n=%d)", *rate*100, n)
}

func interval(lo, hi *float64) string {
	if lo == nil || hi == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f-%.0f%%", *lo*100, *hi*100)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
