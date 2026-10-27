package live_vs_backtest_slippage

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_trades"
)

// JobName is the job_runs.job value for this command.
const JobName = "live_vs_backtest_slippage"

// Row is one strategy's slippage summary.
type Row struct {
	StrategyID string
	Fills      int
	AvgEntry   sql.NullFloat64
	AvgExit    sql.NullFloat64
	WorstEntry sql.NullFloat64
	AvgLimit   sql.NullFloat64
	AvgDelay   sql.NullFloat64
	Fees       float64
}

// Fill is one live fill, ordered so the largest entry slippage is first.
type Fill struct {
	Strategy  string
	Symbol    string
	AsOf      string
	EntryBps  sql.NullFloat64
	ExitBps   sql.NullFloat64
	Delay     sql.NullFloat64
	Fees      float64
	EntryFill float64
	ModelFill float64
}

// Result is the summary plus the fills that paid the most above the model.
type Result struct {
	Rows  []Row
	Worst []Fill
}

// Run summarizes live_vs_backtest_trade into live_vs_backtest_slippage. It does not rebuild the
// ledger. An empty ledger is an error: run live_vs_backtest_trades first.
func Run(ctx context.Context, db *sql.DB) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if db == nil {
		return Result{}, fmt.Errorf("live_vs_backtest_slippage: nil db")
	}
	if err := live_vs_backtest_trades.EnsureSchema(db); err != nil {
		return Result{}, err
	}
	if err := EnsureSchema(db); err != nil {
		return Result{}, err
	}
	var n int
	if err := db.QueryRow(orchestratorsql.Q("live_vs_backtest_slippage/run")).Scan(&n); err != nil {
		return Result{}, err
	}
	if n == 0 {
		return Result{}, fmt.Errorf("live_vs_backtest_slippage: live_vs_backtest_trade is empty; run orchestrator live_vs_backtest_trades first")
	}
	built := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(orchestratorsql.Q("live_vs_backtest_slippage/run_2")); err != nil {
		return Result{}, err
	}
	if _, err := db.Exec(orchestratorsql.Q("live_vs_backtest_slippage/run_3"), built); err != nil {
		return Result{}, err
	}
	rows, err := db.Query(orchestratorsql.Q("live_vs_backtest_slippage/run_4"))
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.StrategyID, &r.Fills, &r.AvgEntry, &r.AvgExit, &r.WorstEntry, &r.AvgLimit, &r.AvgDelay, &r.Fees); err != nil {
			return Result{}, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return Result{}, err
	}
	worst, err := worstFills(db)
	if err != nil {
		return Result{}, err
	}
	return Result{Rows: out, Worst: worst}, nil
}

func worstFills(db *sql.DB) ([]Fill, error) {
	rows, err := db.Query(orchestratorsql.Q("live_vs_backtest_slippage/worst_fills"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fill
	for rows.Next() {
		var f Fill
		if err := rows.Scan(&f.Strategy, &f.Symbol, &f.AsOf, &f.EntryBps, &f.ExitBps, &f.Delay, &f.Fees, &f.EntryFill, &f.ModelFill); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// RunCommand is the orchestrator live_vs_backtest_slippage command.
func RunCommand(ctx context.Context, out io.Writer, env jobenv.Env) error {
	started := time.Now().UTC()
	res, err := Run(ctx, env.DB)
	if err != nil {
		record(env.DB, job_runs.JobRunFailed, err.Error(), 0, started)
		return err
	}
	status := job_runs.JobRunOK
	if len(res.Rows) == 0 {
		status = job_runs.JobRunEmpty
	}
	record(env.DB, status, fmt.Sprintf("%d strategies", len(res.Rows)), len(res.Rows), started)
	fmt.Fprint(out, format(res))
	return nil
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

func format(res Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "entry bps: positive means the live fill paid more than the model; exit bps: positive means the live fill sold higher\n")
	fmt.Fprintf(&b, "avg delay is trading sessions from the intended session to the entry fill\n")
	fmt.Fprintf(&b, "%-22s %5s %8s %8s %8s %8s %7s %8s\n",
		"strategy", "fills", "ent_bps", "exit_bps", "worst", "vs_limit", "delay", "fees")
	for _, r := range res.Rows {
		fmt.Fprintf(&b, "%-22s %5d %8s %8s %8s %8s %7s %8.2f\n",
			r.StrategyID, r.Fills, num(r.AvgEntry), num(r.AvgExit), num(r.WorstEntry), num(r.AvgLimit), num(r.AvgDelay), r.Fees)
	}
	if len(res.Worst) > 0 {
		fmt.Fprintf(&b, "\nlargest entry slippage\n")
		fmt.Fprintf(&b, "%-22s %-6s %-10s %8s %8s %8s %8s\n", "strategy", "symbol", "as_of", "ent_bps", "delay", "live", "model")
		for _, f := range res.Worst {
			fmt.Fprintf(&b, "%-22s %-6s %-10s %8s %8s %8.4f %8.4f\n",
				f.Strategy, f.Symbol, f.AsOf, num(f.EntryBps), num(f.Delay), f.EntryFill, f.ModelFill)
		}
	}
	return b.String()
}

func num(n sql.NullFloat64) string {
	if !n.Valid {
		return "-"
	}
	return fmt.Sprintf("%.1f", n.Float64)
}
