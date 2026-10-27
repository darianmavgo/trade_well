package stage_orders

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	backtestmodels "github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// JobOptions configures RunJob. The same options drive the CLI
// (`orchestrator stage_orders`) and the HTTP route (`/cron/stage_orders`).
type JobOptions struct {
	// DryRun sizes and reports but writes no stage_order and changes no
	// buy-signal status.
	DryRun bool
	// MaxTradingDays and ProfitPct are the optional CLI overrides Run takes
	// (0 = use the strategy's own values).
	MaxTradingDays int
	ProfitPct      float64
	// Members holds every resolved strategy of every allowlisted stack, keyed
	// by strategy ID. Injected by the caller (pkg/strategies): stage_orders
	// never looks strategies up in the backtestgosqlite registry itself.
	Members map[string]strategy.Strategy
	// Now overrides the clock in tests.
	Now time.Time
}

// JobResult summarizes one RunJob call.
type JobResult struct {
	RunAt     time.Time
	Accounts  int
	Signals   int // NEW buy signals considered from the latest scan
	Staged    int // stage_order rows written (or that would be written, in a dry run)
	Dismissed int // buy signals closed without an order (expired, superseded, skipped)
	Details   []string
	Rows      []Row
}

// RunJob is the stage_orders job. It takes the buy signals scan_market wrote
// (the latest scan's NEW rows in scan_market_buy_signal), sizes them against
// each account's live cash/equity with Run, writes stage_order rows for
// pkg/submit_orders, and moves each signal to STAGED or DISMISSED so a rerun
// never stages it twice.
//
// A signal whose next session has already passed is DISMISSED as expired
// rather than staged: entering on an old signal is not what was backtested.
func RunJob(ctx context.Context, db *sql.DB, accounts []*trader.ResolvedAccount, opts JobOptions) (*JobResult, error) {
	if db == nil {
		return nil, fmt.Errorf("stage_orders: nil db")
	}
	started := time.Now().UTC()
	now := opts.Now
	if now.IsZero() {
		now = started
	}
	res := &JobResult{RunAt: started, Accounts: len(accounts)}
	if err := EnsureSchema(db); err != nil {
		return res, err
	}
	if err := scan_market.EnsureSchema(db); err != nil {
		return res, err
	}
	if len(accounts) == 0 {
		return res, fmt.Errorf("stage_orders: no accounts to stage for")
	}

	latest, superseded, err := scan_market.PendingBuySignals(db)
	if err != nil {
		return res, err
	}
	for _, s := range superseded {
		res.Dismissed++
		res.Details = append(res.Details, fmt.Sprintf("dismiss %s/%s from older scan %s (superseded)", s.StrategyID, s.Symbol, s.ScanRunID))
		if !opts.DryRun {
			_ = scan_market.SetBuySignalStatus(db, s.ID, scan_market.BuySignalDismissed, 0, "superseded by a newer scan")
		}
	}

	today := now.In(job_runs.EasternTime()).Format("2006-01-02")
	byStack := map[string][]scan_market.BuySignal{}
	var stackOrder []string
	for _, s := range latest {
		if s.NextSession != "" && s.NextSession < today {
			res.Dismissed++
			res.Details = append(res.Details, fmt.Sprintf("dismiss %s/%s: signal for session %s expired (today %s)", s.StrategyID, s.Symbol, s.NextSession, today))
			res.Rows = append(res.Rows, Row{Strategy: s.StrategyID, Symbol: s.Symbol, Signal: "BUY", Status: "EXPIRED",
				Detail: fmt.Sprintf("signal for %s, today is %s", s.NextSession, today)})
			if !opts.DryRun {
				_ = scan_market.SetBuySignalStatus(db, s.ID, scan_market.BuySignalDismissed, 0, "expired: next session "+s.NextSession)
			}
			continue
		}
		res.Signals++
		key := s.StackID
		if key == "" {
			key = s.StrategyID
		}
		if _, ok := byStack[key]; !ok {
			stackOrder = append(stackOrder, key)
		}
		byStack[key] = append(byStack[key], s)
	}

	staged := map[int64]int64{}   // buy_signal id -> stage_order id
	handled := map[int64]string{} // buy_signal id -> skip reason (when no account staged it)
	for _, stackID := range stackOrder {
		sigs := byStack[stackID]
		var members []strategy.Strategy
		for _, id := range strategy.ParseStack(stackID) {
			if m, ok := opts.Members[id]; ok {
				members = append(members, m)
			}
		}
		cands := make([]scan_market.Signal, 0, len(sigs))
		for _, s := range sigs {
			cand := scan_market.Signal{
				StrategyID:  s.StrategyID,
				Priority:    s.StackPriority,
				AllocPct:    s.AllocationPct,
				Symbol:      s.Symbol,
				Price:       s.Price,
				NextSession: s.NextSession,
				Raw:         rawSignal(s),
			}
			if m, ok := opts.Members[s.StrategyID]; ok {
				mc := m.DefaultConfig()
				cand.Sizing, cand.FixedShares, cand.FixedDollar = mc.PositionSizing, mc.FixedShares, mc.FixedDollar
			}
			cands = append(cands, cand)
		}
		for _, acc := range accounts {
			r, err := Run(ctx, db, acc, stackID, cands, members, opts.MaxTradingDays, opts.DryRun, opts.ProfitPct)
			if r != nil {
				res.Staged += r.OrdersStaged
				res.Details = append(res.Details, r.Details...)
				res.Rows = append(res.Rows, r.Rows...)
			}
			if err != nil {
				res.Details = append(res.Details, fmt.Sprintf("stage %s for %s failed: %v", stackID, acc.AccountNumber, err))
				res.Rows = append(res.Rows, Row{Account: acc.AccountNumber, Strategy: stackID, Signal: "BUY", Status: "ERROR", Detail: err.Error()})
				continue
			}
			for _, row := range r.Rows {
				for _, s := range sigs {
					if s.StrategyID != row.Strategy || !strings.EqualFold(s.Symbol, row.Symbol) {
						continue
					}
					switch row.Status {
					case "STAGED":
						staged[s.ID] = latestStagedID(db, acc.AccountNumber, s.StrategyID, s.Symbol)
					case "SKIPPED", "ERROR":
						if _, ok := handled[s.ID]; !ok {
							handled[s.ID] = row.Detail
						}
					}
				}
			}
		}
		if opts.DryRun {
			continue
		}
		for _, s := range sigs {
			if id, ok := staged[s.ID]; ok {
				_ = scan_market.SetBuySignalStatus(db, s.ID, scan_market.BuySignalStaged, id, "")
			} else if reason, ok := handled[s.ID]; ok {
				res.Dismissed++
				_ = scan_market.SetBuySignalStatus(db, s.ID, scan_market.BuySignalDismissed, 0, "not staged: "+reason)
			}
			// A signal no Run reported on stays NEW and is retried next run.
		}
	}

	status := job_runs.JobRunOK
	if res.Staged == 0 {
		status = job_runs.JobRunEmpty
	}
	summary := fmt.Sprintf("dry_run=%v accounts=%d signals=%d staged=%d dismissed=%d", opts.DryRun, res.Accounts, res.Signals, res.Staged, res.Dismissed)
	if !opts.DryRun {
		if _, err := job_runs.RecordJobRun(db, job_runs.JobRun{
			Job: "stage_orders", Status: status, Summary: summary, RowsWritten: res.Staged, StartedAt: started, FinishedAt: time.Now().UTC(),
		}); err != nil {
			res.Details = append(res.Details, fmt.Sprintf("job_run write failed: %v", err))
		}
	}
	return res, nil
}

// rawSignal rebuilds the backtestgosqlite signal Run's window logic reads,
// from the columns scan_market stored. RawJSON is the livescan detail and
// only fills gaps.
func rawSignal(s scan_market.BuySignal) backtestmodels.Signal {
	sig := backtestmodels.Signal{
		Symbol:     s.Symbol,
		Date:       s.AsOfDate,
		Close:      s.Close,
		BuyLimit:   s.BuyLimit,
		TakeProfit: s.TakeProfit,
		StopLoss:   s.StopLoss,
		Direction:  s.Direction,
		StrategyID: s.StrategyID,
	}
	if sig.BuyLimit <= 0 {
		var raw struct {
			BuyLimit float64 `json:"buy_limit"`
		}
		if json.Unmarshal([]byte(s.RawJSON), &raw) == nil {
			sig.BuyLimit = raw.BuyLimit
		}
	}
	if sig.BuyLimit <= 0 {
		sig.BuyLimit = s.Price
	}
	return sig
}

func latestStagedID(db *sql.DB, account, strategyName, symbol string) int64 {
	var id int64
	_ = db.QueryRow(orchestratorsql.Q("stage_orders/latest_staged_id"),
		account, strategyName, symbol).Scan(&id)
	return id
}
