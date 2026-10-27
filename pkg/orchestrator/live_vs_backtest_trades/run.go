package live_vs_backtest_trades

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
)

// JobName is the job_runs.job value for this command.
const JobName = "live_vs_backtest_trades"

// Result is one rebuild of the decision ledger.
type Result struct {
	Rows []ledger
}

// Run rebuilds live_vs_backtest_trade from buy signals, staged orders, and managed
// trades. bars may be nil: the live side is still written and the model side
// is NO_BARS. account limits staged orders and managed trades; signals that
// were never staged are still included.
func Run(ctx context.Context, db *sql.DB, account, marketDB string, bars map[string][]models.Bar) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if db == nil {
		return Result{}, fmt.Errorf("live_vs_backtest_trades: nil db")
	}
	for _, ensure := range []func(*sql.DB) error{
		scan_market.EnsureSchema,
		stage_orders.EnsureSchema,
		manage_exits.EnsureSchema,
		EnsureSchema,
	} {
		if err := ensure(db); err != nil {
			return Result{}, err
		}
	}
	rows, err := assemble(db, strings.TrimSpace(account), strings.TrimSpace(marketDB), bars)
	if err != nil {
		return Result{}, err
	}
	if err := save(db, strings.TrimSpace(account), rows); err != nil {
		return Result{}, err
	}
	return Result{Rows: rows}, nil
}

// RunCommand is the orchestrator live_vs_backtest_trades command. marketDB is the
// bars file (MARKET_DB when the flag is empty). A missing file still writes
// the live side.
func RunCommand(ctx context.Context, out io.Writer, env jobenv.Env, account, marketDB, table string) error {
	started := time.Now().UTC()
	bars, note, err := LoadBarsFromMarketWorkingCopy(marketDB, table)
	if err != nil {
		record(env.DB, job_runs.JobRunFailed, err.Error(), 0, started)
		return err
	}
	if note != "" {
		fmt.Fprintln(out, note)
	}
	res, err := Run(ctx, env.DB, account, marketDB, bars)
	if err != nil {
		record(env.DB, job_runs.JobRunFailed, err.Error(), 0, started)
		return err
	}
	status := job_runs.JobRunOK
	if len(res.Rows) == 0 {
		status = job_runs.JobRunEmpty
	}
	record(env.DB, status, fmt.Sprintf("%d decisions", len(res.Rows)), len(res.Rows), started)
	fmt.Fprint(out, format(res.Rows))
	fmt.Fprintf(out, "wrote %d row(s) to %s\n", len(res.Rows), TableName)
	return nil
}

func save(db *sql.DB, account string, rows []ledger) error {
	rows = dedupe(rows)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if account == "" {
		if _, err := tx.Exec(orchestratorsql.Q("live_vs_backtest_trades/save")); err != nil {
			return err
		}
	} else if _, err := tx.Exec(orchestratorsql.Q("live_vs_backtest_trades/save_2"), account); err != nil {
		return err
	}
	built := time.Now().UTC().Format(time.RFC3339)
	stmt, err := tx.Prepare(orchestratorsql.Q("live_vs_backtest_trades/save_3"))
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.Exec(
			r.account, r.strategy, r.symbol, r.asOf, r.next,
			r.buySignalID, r.signalID, r.stageID, r.exitID, r.orderID,
			r.liveOutcome, r.skipReason, r.entryLimit, r.entryFill, r.exitFill,
			r.qty, r.fees, r.liveExitDate, r.liveReason, r.liveReturn,
			r.modelOutcome, r.modelEntry, r.modelExit, r.modelExitDate, r.modelReason, r.modelReturn,
			nullFloat(r.entryBps), nullFloat(r.exitBps), nullFloat(r.limitBps), nullFloat(r.delay),
			r.signalMatch, r.outcomeMatch, built,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func dedupe(rows []ledger) []ledger {
	type key struct{ account, strategy, symbol, asOf string }
	best := map[key]ledger{}
	var order []key
	for _, r := range rows {
		k := key{r.account, r.strategy, r.symbol, r.asOf}
		prev, ok := best[k]
		if !ok {
			order = append(order, k)
			best[k] = r
			continue
		}
		if r.exitID > prev.exitID || (r.exitID == prev.exitID && r.stageID > prev.stageID) {
			best[k] = r
		}
	}
	out := make([]ledger, 0, len(order))
	for _, k := range order {
		out = append(out, best[k])
	}
	return out
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

func format(rows []ledger) string {
	var b strings.Builder
	fmt.Fprintf(&b, "entry bps: positive means the live fill paid more than the model\n")
	fmt.Fprintf(&b, "exit bps: positive means the live fill sold higher than the model\n")
	fmt.Fprintf(&b, "returns are fractions; fill_delay_sessions counts trading sessions from next_session to the entry fill (negative means filled before that session)\n")
	fmt.Fprintf(&b, "%-22s %-6s %-10s %-10s %-12s %8s %8s %6s %s\n",
		"strategy", "symbol", "as_of", "live", "model", "ent_bps", "exit_bps", "delay", "match")
	for _, r := range rows {
		fmt.Fprintf(&b, "%-22s %-6s %-10s %-10s %-12s %8s %8s %6s %d/%d\n",
			r.strategy, r.symbol, r.asOf, r.liveOutcome, r.modelOutcome,
			bpsText(r.entryBps), bpsText(r.exitBps), bpsText(r.delay), r.signalMatch, r.outcomeMatch)
	}
	return b.String()
}

func bpsText(n sql.NullFloat64) string {
	if !n.Valid {
		return "-"
	}
	return fmt.Sprintf("%.1f", n.Float64)
}
