package trigger_limits

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// CommandOptions are the inputs to the `orchestrator trigger_limits` job.
type CommandOptions struct {
	Accounts jobenv.Accounts
	Strategy string // strategy ids to stage and seat, comma-separated; default: every greenlit strategy with a limit entry
	DryRun   bool   // report what would be seated, swapped, placed or cancelled; write and send nothing
}

// AnyWatching reports whether stage_orders holds a WATCHING order. Most passes have
// nothing to do, so callers look here before calling the broker.
func AnyWatching(db *sql.DB) (bool, error) {
	if err := stage_orders.EnsureSchema(db); err != nil {
		return false, err
	}
	watching, err := stage_orders.GetWatchingStagedOrders(db, "")
	if err != nil {
		return false, err
	}
	return len(watching) > 0, nil
}

// SchwabQuotes is the QuoteFunc that reads quotes from Schwab. A symbol whose quote has
// no detail is left out, so it ranks as infinitely far.
func SchwabQuotes(tc *trader.TraderClient) QuoteFunc {
	return func(ctx context.Context, symbols []string) (map[string]Quote, error) {
		raw, err := tc.Client.GetQuotes(ctx, symbols)
		if err != nil {
			return nil, err
		}
		out := make(map[string]Quote, len(raw))
		for sym, q := range raw {
			d, err := q.Detail()
			if err != nil {
				continue
			}
			out[strings.ToUpper(sym)] = Quote{Low: d.LowPrice, Ask: d.AskPrice, Last: d.LastPrice}
		}
		return out, nil
	}
}

// Summary is the one-line result the CLI prints and /cron/trigger_limits answers with.
func (r *JobResult) Summary(dryRun bool) string {
	return fmt.Sprintf("trigger_limits dry_run=%v watching=%d seated=%d swapped=%d triggered=%d placed=%d cancelled=%d expired=%d",
		dryRun, r.Watching, r.Seated, r.Swapped, r.Triggered, r.Promoted, r.Cancelled, r.Expired)
}

// maxCommandFailures is how many refused or failed orders end a hand-run seating.
const maxCommandFailures = 2

// RunCommand is the `orchestrator trigger_limits` job: stage the strategy's limit entries
// as WATCHING when none are waiting, then seat the day's watched limit
// entries at the broker, nearest to filling first, and report each one as it is accepted
// or refused, whatever the hour. It never cancels anything (no swaps, no expiry pass, no closing of skipped
// orders) and stops placing after two refused or failed orders, a buying-power refusal
// included. /cron/trigger_limits runs RunJob, which also swaps, triggers and cancels.
// Output goes to out; the caller decides the exit code from the returned error.
func RunCommand(ctx context.Context, out io.Writer, env jobenv.Env, opts CommandOptions) error {
	fmt.Fprintf(out, "\n%s%s=== trigger_limits ===%s\n", jobenv.ColBold, jobenv.ColCyan, jobenv.ColReset)
	jobenv.WriteMode(out, opts.DryRun)

	strats, err := strategies.ResolveGreenlit(env.DB, env.Cfg.StrategyAllowlist)
	if err != nil {
		return fmt.Errorf("failed to resolve greenlit strategies: %w", err)
	}
	members := make(map[string]strategy.Strategy, len(strats))
	var limitIDs []string
	for _, st := range strats {
		members[st.ID()] = st
		if st.DefaultConfig().EntryLimitPct > 0 {
			limitIDs = append(limitIDs, st.ID())
		}
	}

	// Stage the limit entries as WATCHING unless some are already waiting.
	any, err := AnyWatching(env.DB)
	if err != nil {
		return err
	}
	if !any {
		ids := opts.Strategy
		if ids == "" {
			ids = strings.Join(limitIDs, ",")
		}
		if ids == "" {
			return fmt.Errorf("no greenlit strategy has a limit entry: add one to STRATEGY_ALLOWLIST or pass -strategy")
		}
		if err := stage_orders.RunScanAndStage(ctx, out, env, stage_orders.ScanOptions{Accounts: opts.Accounts, Strategies: ids, DryRun: opts.DryRun}); err != nil {
			return fmt.Errorf("staging %s: %w", ids, err)
		}
		if any, err = AnyWatching(env.DB); err != nil {
			return err
		}
	}
	if !any {
		fmt.Fprintln(out, "nothing staged as WATCHING: no buy signals to place")
		return nil
	}
	targets, err := opts.Accounts.Targets()
	if err != nil {
		return err
	}
	tc, accts, err := jobenv.ResolveAccounts(ctx, env.Cfg, env.DB, targets)
	if err != nil {
		return fmt.Errorf("failed to resolve target account(s): %w", err)
	}
	seatOpts := Options{DryRun: opts.DryRun, Members: members, Quotes: SchwabQuotes(tc), NoCancel: true, AnySession: true, MaxFailures: maxCommandFailures}

	var errs []string
	for _, acc := range accts {
		fmt.Fprintf(out, "\n%sAccount %s%s\n", jobenv.ColBold, acc.AccountNumber, jobenv.ColReset)
		res, err := Seat(ctx, env.DB, tc, acc, seatOpts)
		if res != nil {
			for _, row := range res.Rows {
				mark, col := "✗", jobenv.ColRed
				switch row.Status {
				case "SEATED", "WOULD_SEAT":
					mark, col = "✓", jobenv.ColGreen
				case "SKIPPED":
					mark, col = "-", jobenv.ColDim
				}
				fmt.Fprintf(out, "  %s%s%s %-6s %-17s %s\n", col, mark, jobenv.ColReset, row.Symbol, row.Status, row.Detail)
			}
			fmt.Fprintf(out, "  %sSummary:%s accepted=%d refused_or_failed=%d stopped=%v\n", jobenv.ColBold, jobenv.ColReset, res.Seated, res.Rejected, res.Stopped)
			if res.Stopped {
				fmt.Fprintf(out, "  %sStopped after %d refused or failed orders; nothing more was submitted for this account.%s\n", jobenv.ColRed, maxCommandFailures, jobenv.ColReset)
			}
			if res.Seated == 0 && res.Rejected == 0 && len(res.Rows) == 0 {
				fmt.Fprintln(out, "  nothing seated: no watched limit for today's session, or outside the seating window (09:00 to 15:30 ET on a session day)")
			}
		}
		if err != nil {
			fmt.Fprintf(out, "  %s[Error]%s %v\n", jobenv.ColRed, jobenv.ColReset, err)
			errs = append(errs, fmt.Sprintf("account %s: %v", acc.AccountNumber, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("trigger_limits: %s", strings.Join(errs, "; "))
	}
	return nil
}
