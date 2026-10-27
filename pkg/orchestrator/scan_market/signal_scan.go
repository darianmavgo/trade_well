package scan_market

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	backtestmodels "github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
)

// SignalScanSummary summarizes a Schwab-free signal scan pass.
type SignalScanSummary struct {
	ScanRunID           string
	StrategiesRequested int
	MembersEvaluated    int
	LivescanSignals     int
	BuySignalsWritten   int
	AsOf                string
	NextSession         string
	Details             []string
	// Signals holds every BuySignal actually written this run (same rows as
	// scan_market_buy_signal), so callers -- notably the cron email report --
	// can name the specific ticker(s) a BUY was detected on instead of just a
	// strategy-level count.
	Signals []BuySignal
	// Strategies is one line per scanned strategy -- BUY, NO_BUY (with livescan's reason) or
	// NOT_SCANNED -- for the report email, so it lists what was scanned and not only what fired.
	Strategies []StrategyScan
}

// RunSignalScan runs livescan for the given strategy/stack IDs and
// writes scan_market_buy_signal rows for every LONG/BUY ENTER. It does not
// touch stage_order or account state; its only Schwab use is daily price history
// when MarketDataSource is "schwab". membersByID must
// contain every parsed stack member's resolved strategy.Strategy -- this
// function never calls strategy.Get itself.
func RunSignalScan(ctx context.Context, db *sql.DB, cfg *appconfig.Config, strategyIDs []string, membersByID map[string]strategy.Strategy) (*SignalScanSummary, error) {
	if db == nil {
		return nil, fmt.Errorf("RunSignalScan: nil db")
	}
	if len(strategyIDs) == 0 {
		return nil, fmt.Errorf("RunSignalScan: no strategy IDs")
	}
	if err := EnsureSchema(db); err != nil {
		return nil, err
	}

	runID := time.Now().UTC().Format("20060102T150405Z")
	res := &SignalScanSummary{
		ScanRunID:           runID,
		StrategiesRequested: len(strategyIDs),
	}

	memberToStack := map[string]string{}
	memberPriority := map[string]int{}
	var members []string
	seen := map[string]bool{}
	for _, raw := range strategyIDs {
		stack := strings.TrimSpace(raw)
		parts := strategy.ParseStack(stack)
		if len(parts) == 0 {
			res.Details = append(res.Details, fmt.Sprintf("skip empty strategy id %q", raw))
			continue
		}
		for i, id := range parts {
			if !seen[id] {
				seen[id] = true
				members = append(members, id)
			}
			if _, ok := memberToStack[id]; !ok {
				memberToStack[id] = stack
				memberPriority[id] = i
			}
		}
	}
	res.MembersEvaluated = len(members)
	if len(members) == 0 {
		return res, fmt.Errorf("RunSignalScan: no strategy members after parsing")
	}

	marketDB, err := resolveMarketDB(cfg)
	if err != nil {
		return res, err
	}
	outDir := filepath.Join(os.TempDir(), "trade_orchestrator_scan_market")
	res.Details = append(res.Details, fmt.Sprintf("livescan members=%s market_db=%s", strings.Join(members, ","), marketDB))

	var strats []strategy.Strategy
	for _, id := range members {
		if s, ok := membersByID[id]; ok {
			strats = append(strats, s)
		}
	}
	restore, source, err := useSchwabDailyBars(ctx, cfg)
	if err != nil {
		return res, err
	}
	defer restore()
	res.Details = append(res.Details, "market data source="+source)
	persistBars := syncBars(ctx, db, marketDB, runner.RequiredSymbolsFor(strats, ""), &res.Details)
	live, err := runner.RunLiveScan(strats, marketDB, outDir)
	persistBars() // also after a failed scan: the download is kept even when a later step fails
	if err != nil {
		return res, withDataSource(source, err)
	}
	res.AsOf = live.AsOf
	res.NextSession = live.NextSession
	res.LivescanSignals = len(live.Signals)
	res.Details = append(res.Details, fmt.Sprintf(
		"livescan as_of=%s next_session=%s signals=%d symbols_loaded=%d",
		live.AsOf, live.NextSession, len(live.Signals), live.SymbolsLoaded))

	// Why each strategy did or did not enter, and the Markov predictions scored since training. Both
	// are audit copies: a failure to save them is logged and never fails the scan.
	traces, predictions := RecordTrace(db, runID, live.AsOf, live.Rows, time.Now())
	res.Details = append(res.Details, fmt.Sprintf("trace rows=%d live markov predictions saved=%d", traces, predictions))
	for _, row := range live.Rows {
		for _, t := range row.Trace {
			if t.Date == live.AsOf {
				res.Details = append(res.Details, fmt.Sprintf("why %s: %s", row.StrategyID, t.Reason))
			}
		}
	}

	statusByID := map[string]runner.SignalScanRow{}
	for _, row := range live.Rows {
		statusByID[strings.TrimSpace(row.StrategyID)] = row
	}

	now := time.Now().UTC()
	for _, sig := range live.Signals {
		id := strings.TrimSpace(sig.StrategyID)
		if !seen[id] {
			continue
		}
		dir := strings.ToUpper(strings.TrimSpace(sig.Direction))
		if dir == "" {
			dir = "LONG"
		}
		if dir != "LONG" && dir != "BUY" && dir != "ENTER" {
			continue
		}
		sym := strings.ToUpper(strings.TrimSpace(sig.Symbol))
		if sym == "" {
			continue
		}
		price := sig.Price
		if price <= 0 {
			price = sig.BuyLimit
		}
		if price <= 0 {
			price = sig.Close
		}

		allocPct, holdDays, stratName := 0.0, 0, ""
		if strat, ok := membersByID[id]; ok {
			mcfg := strat.DefaultConfig()
			allocPct, holdDays, stratName = mcfg.AllocationPct, mcfg.HoldingWindow, strat.Name()
		}
		notes := ""
		if st, ok := statusByID[id]; ok {
			notes = fmt.Sprintf("livescan_status=%s symbols=%s signal_date=%s", st.Status, st.Symbols, st.SignalDate)
			if stratName == "" {
				stratName = st.StrategyName
			}
		}

		bs := &BuySignal{
			ScanRunID:     runID,
			StackID:       memberToStack[id],
			StrategyID:    id,
			StrategyName:  stratName,
			Symbol:        sym,
			Direction:     dir,
			AsOfDate:      firstNonEmpty(sig.Date, live.AsOf),
			NextSession:   live.NextSession,
			Price:         price,
			Close:         sig.Close,
			BuyLimit:      sig.BuyLimit,
			TakeProfit:    sig.TakeProfit,
			StopLoss:      sig.StopLoss,
			AllocationPct: allocPct,
			HoldDays:      holdDays,
			StackPriority: memberPriority[id],
			Status:        BuySignalNew,
			RawJSON:       MarshalBuySignalRaw(sig),
			Notes:         notes,
			CreatedAt:     now,
		}
		if _, err := InsertBuySignal(db, bs); err != nil {
			return res, fmt.Errorf("insert buy_signal %s/%s: %w", id, sym, err)
		}
		res.BuySignalsWritten++
		res.Signals = append(res.Signals, *bs)
		res.Details = append(res.Details, fmt.Sprintf(
			"BUY %s/%s @ %.4f TP=%.4f SL=%.4f alloc=%.0f%% hold=%dd",
			id, sym, price, sig.TakeProfit, sig.StopLoss, allocPct*100, holdDays))
	}

	if res.BuySignalsWritten == 0 {
		res.Details = append(res.Details, "no LONG/BUY ENTER signals to record")
	}
	res.Strategies = strategyScans(members, membersByID, statusByID, res.Signals, live.AsOf)
	status := job_runs.JobRunOK
	if res.BuySignalsWritten == 0 {
		status = job_runs.JobRunEmpty
	}
	summary := fmt.Sprintf("as_of=%s next=%s members=%d livescan_signals=%d buy_signals=%d",
		res.AsOf, res.NextSession, res.MembersEvaluated, res.LivescanSignals, res.BuySignalsWritten)
	if _, err := job_runs.RecordJobRun(db, job_runs.JobRun{
		Job:         "scan_market",
		Status:      status,
		Summary:     summary,
		RowsWritten: res.BuySignalsWritten,
		FinishedAt:  time.Now().UTC(),
	}); err != nil {
		res.Details = append(res.Details, fmt.Sprintf("job_run write failed: %v", err))
	}
	return res, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// strategyScans builds the per-strategy report lines: each member's symbols, whether it entered
// on the as-of bar and, when it did not, the reason livescan traced for that bar.
func strategyScans(members []string, membersByID map[string]strategy.Strategy, statusByID map[string]runner.SignalScanRow, written []BuySignal, asOf string) []StrategyScan {
	buys := map[string][]Signal{}
	for _, b := range written {
		buys[b.StrategyID] = append(buys[b.StrategyID], Signal{
			StrategyID: b.StrategyID, Symbol: b.Symbol, Price: b.Price, AllocPct: b.AllocationPct,
			Raw: backtestmodels.Signal{TakeProfit: b.TakeProfit, StopLoss: b.StopLoss},
		})
	}
	out := make([]StrategyScan, 0, len(members))
	for _, id := range members {
		sc := StrategyScan{StrategyID: id}
		if m, ok := membersByID[id]; ok {
			if rp, ok := m.(strategy.RequiredSymbolsProvider); ok {
				sc.Symbols = strings.Join(rp.RequiredSymbols(), ",")
			}
			if sc.Symbols == "" {
				sc.Symbols = m.DefaultConfig().Benchmark
			}
		}
		row, ran := statusByID[id]
		if row.Symbols != "" {
			sc.Symbols = row.Symbols
		}
		switch {
		case len(buys[id]) > 0:
			sc.Status, sc.Detail = ScanBuy, buyDetail(buys[id])
		case !ran:
			sc.Status, sc.Detail = ScanNotScanned, "livescan returned no result for this strategy"
		default:
			sc.Status = ScanNoBuy
			var why []string
			for _, t := range row.Trace {
				if t.Date == asOf && t.Reason != "" {
					why = append(why, t.Reason)
				}
			}
			sc.Detail = "no entry as of " + asOf
			if len(why) > 0 {
				sc.Detail += ": " + strings.Join(why, "; ")
			}
		}
		out = append(out, sc)
	}
	return out
}
