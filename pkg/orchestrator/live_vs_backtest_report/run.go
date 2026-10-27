package live_vs_backtest_report

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_state"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_slippage"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_trades"
)

// Options is one report build. HTML is the file this command creates. MarketDB and Table are the
// bars live_vs_backtest_trades replays. Allowlist is STRATEGY_ALLOWLIST, already parsed.
type Options struct {
	Account   string
	MarketDB  string
	Table     string
	HTML      string
	Allowlist []string
}

// Result is where the report was written. Warnings are the notes on that run.
// An empty Warnings list is a clean OK.
type Result struct {
	RunID      string
	HTML       string
	Decisions  int
	RoundTrips int
	Notes      int
	Warnings   []string
}

// Run reads the orchestrator database (Turso on a deployment) and the market file it is given.
// It then runs live_vs_backtest_trades and live_vs_backtest_slippage and writes the report
// database and HTML.
func Run(ctx context.Context, out io.Writer, env jobenv.Env, opts Options) (Result, error) {
	started := time.Now().UTC()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if env.DB == nil {
		return Result{}, fmt.Errorf("live_vs_backtest_report: nil db")
	}
	opts.HTML = strings.TrimSpace(opts.HTML)
	if opts.HTML == "" {
		opts.HTML = filepath.Join(ScratchDir(env.Cfg), "live_vs_backtest_report.html")
	}
	if strings.TrimSpace(opts.Table) == "" {
		opts.Table = "backtest_start"
	}

	bars, marketNote, err := live_vs_backtest_trades.LoadBarsFromMarketWorkingCopy(opts.MarketDB, opts.Table)
	if err != nil {
		record(env.DB, job_runs.JobRunFailed, err.Error(), 0, started)
		return Result{}, err
	}
	if marketNote != "" {
		fmt.Fprintln(out, marketNote)
	}
	rec, err := live_vs_backtest_trades.Run(ctx, env.DB, opts.Account, opts.MarketDB, bars)
	if err != nil {
		record(env.DB, job_runs.JobRunFailed, err.Error(), 0, started)
		return Result{}, err
	}
	var notes []note
	if marketNote != "" {
		notes = append(notes, note{Severity: "warn", Message: marketNote})
	}
	var ledgerN int
	if err := env.DB.QueryRow(orchestratorsql.Q("live_vs_backtest_report/run")).Scan(&ledgerN); err != nil {
		record(env.DB, job_runs.JobRunFailed, err.Error(), 0, started)
		return Result{}, err
	}
	audited := false
	if ledgerN == 0 {
		notes = append(notes, note{Severity: "warn", Message: "live_vs_backtest_trade is empty; live_vs_backtest_slippage was not run"})
		if err := live_vs_backtest_slippage.EnsureSchema(env.DB); err != nil {
			record(env.DB, job_runs.JobRunFailed, err.Error(), 0, started)
			return Result{}, err
		}
		if _, err := env.DB.Exec(orchestratorsql.Q("live_vs_backtest_report/run_2")); err != nil {
			record(env.DB, job_runs.JobRunFailed, err.Error(), 0, started)
			return Result{}, err
		}
	} else if _, err := live_vs_backtest_slippage.Run(ctx, env.DB); err != nil {
		record(env.DB, job_runs.JobRunFailed, err.Error(), 0, started)
		return Result{}, err
	} else {
		audited = true
	}
	fmt.Fprint(out, reconcileText(len(rec.Rows), audited))

	extra, err := gapNotes(env.DB, opts.Account)
	if err != nil {
		record(env.DB, job_runs.JobRunFailed, err.Error(), 0, started)
		return Result{}, err
	}
	notes = append(notes, extra...)

	trips, tripNotes, err := brokerCash(env.DB, opts.Allowlist, strings.TrimSpace(opts.Account))
	if err != nil {
		record(env.DB, job_runs.JobRunFailed, err.Error(), 0, started)
		return Result{}, err
	}
	for _, m := range tripNotes {
		notes = append(notes, note{Severity: "warn", Message: m})
	}
	ledgerNotes, err := ledgerGapNotes(env.DB, trips)
	if err != nil {
		record(env.DB, job_runs.JobRunFailed, err.Error(), 0, started)
		return Result{}, err
	}
	notes = append(notes, ledgerNotes...)

	runID, err := writeReportTables(ctx, env.DB, opts, notes, trips)
	if err != nil {
		record(env.DB, job_runs.JobRunFailed, err.Error(), 0, started)
		return Result{}, err
	}
	res := Result{
		RunID: runID, HTML: opts.HTML,
		Decisions: len(rec.Rows), RoundTrips: len(trips), Notes: len(notes),
		Warnings: noteMessages(notes),
	}
	record(env.DB, job_runs.JobRunOK, fmt.Sprintf("%d decisions, %d round trips, report %s", res.Decisions, res.RoundTrips, opts.HTML), res.Decisions, started)
	return res, nil
}

// FormatStatus is the closing summary. It starts with "live_vs_backtest_report OK"
// or "live_vs_backtest_report OK, with warnings".
func FormatStatus(res Result) string {
	head := "live_vs_backtest_report OK"
	if len(res.Warnings) > 0 {
		head = "live_vs_backtest_report OK, with warnings"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", head)
	fmt.Fprintf(&b, "  decisions %d\n", res.Decisions)
	fmt.Fprintf(&b, "  round trips %d\n", res.RoundTrips)
	fmt.Fprintf(&b, "  warnings %d\n", len(res.Warnings))
	for _, w := range res.Warnings {
		fmt.Fprintf(&b, "  warning: %s\n", w)
	}
	fmt.Fprintf(&b, "  tables __%s\n", res.RunID)
	fmt.Fprintf(&b, "  html %s\n", res.HTML)
	return b.String()
}

func noteMessages(notes []note) []string {
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		if m := strings.TrimSpace(n.Message); m != "" {
			out = append(out, m)
		}
	}
	return out
}

func reconcileText(n int, audited bool) string {
	if audited {
		return fmt.Sprintf("live_vs_backtest_trades wrote %d decision(s); live_vs_backtest_slippage summarized them\n", n)
	}
	return fmt.Sprintf("live_vs_backtest_trades wrote %d decision(s); live_vs_backtest_slippage was not run\n", n)
}

func samePath(a, b string) bool {
	a = filepath.Clean(strings.TrimSpace(a))
	b = filepath.Clean(strings.TrimSpace(b))
	return a != "." && a == b
}

type note struct {
	Severity string
	Message  string
}

func gapNotes(db *sql.DB, account string) ([]note, error) {
	var notes []note
	var stages, orders int
	if err := db.QueryRow(orchestratorsql.Q("live_vs_backtest_report/gap_notes")).Scan(&stages); err != nil {
		return nil, err
	}
	var exits int
	q := orchestratorsql.Q("live_vs_backtest_report/gap_notes_2")
	var args []any
	if strings.TrimSpace(account) != "" {
		q += ` WHERE account_number = ?`
		args = append(args, strings.TrimSpace(account))
	}
	if err := db.QueryRow(q, args...).Scan(&exits); err != nil {
		return nil, err
	}
	if stages == 0 && exits > 0 {
		notes = append(notes, note{Severity: "warn", Message: "stage_order is empty, so filled trades have no stored signal date and the replay dates them on the fill day"})
	}
	orderTable := account_state.MirrorStore().Tables().Order
	oq := `SELECT COUNT(*) FROM ` + orderTable
	if strings.TrimSpace(account) != "" {
		oq += ` WHERE account_number = ?`
	} else {
		args = nil
	}
	if err := db.QueryRow(oq, args...).Scan(&orders); err != nil {
		return nil, err
	}
	if orders == 0 {
		notes = append(notes, note{Severity: "warn", Message: "the broker mirror has no orders; run account_state before this command to fill the cash table"})
	}
	return notes, nil
}

func ledgerGapNotes(db *sql.DB, trips []roundTrip) ([]note, error) {
	sold := map[string]bool{}
	for _, t := range trips {
		if t.sellID != "" {
			sold[t.account+"|"+t.symbol] = true
		}
	}
	rows, err := db.Query(orchestratorsql.Q("live_vs_backtest_report/ledger_gap_notes"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var missing []string
	seen := map[string]bool{}
	for rows.Next() {
		var acct, sym string
		var px float64
		if err := rows.Scan(&acct, &sym, &px); err != nil {
			return nil, err
		}
		key := acct + "|" + strings.ToUpper(sym)
		if !sold[key] || seen[key] {
			continue
		}
		seen[key] = true
		missing = append(missing, strings.ToUpper(sym))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(missing) == 0 {
		return nil, nil
	}
	return []note{{
		Severity: "warn",
		Message:  "ledger exit fill is 0 for " + strings.Join(missing, ", ") + "; that live return is pnl_pct. The cash table uses the broker execution",
	}}, nil
}

func nullFloat(n sql.NullFloat64) any {
	if !n.Valid {
		return nil
	}
	return n.Float64
}

func record(db *sql.DB, status, summary string, n int, started time.Time) {
	if db == nil {
		return
	}
	_, _ = job_runs.RecordJobRun(db, job_runs.JobRun{
		Job: JobName, Status: status, Summary: summary, RowsWritten: n,
		StartedAt: started, FinishedAt: time.Now().UTC(),
	})
}

// ScratchDir is where a report build puts its per-run report database and HTML: a derived
// output, rebuilt every run, never the operational database. Under `go test` the config's
// DBPath is a file in a temp folder and the output goes beside it; otherwise it is a folder
// under the OS temp directory (App Engine's disk is read-only apart from /tmp).
func ScratchDir(cfg *appconfig.Config) string {
	if cfg != nil && cfg.DBPath != "" {
		return filepath.Dir(cfg.DBPath)
	}
	return filepath.Join(os.TempDir(), "trade_orchestrator")
}
