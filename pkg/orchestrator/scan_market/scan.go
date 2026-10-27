package scan_market

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	backtestmodels "github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
)

// Signal is one candidate BUY produced by livescan for a stack member,
// before any account-based sizing. scan_market never touches Schwab --
// sizing and staging happen in pkg/stage_orders.
type Signal struct {
	StrategyID string
	Priority   int
	AllocPct   float64
	// Sizing is the strategy's own sizing rule: "fixed_shares" (FixedShares),
	// "fixed_dollar" (FixedDollar), or empty/"fixed_pct" (AllocPct of equity).
	// stage_orders honors it, so a one-share smoke-test strategy stays one share.
	Sizing      string
	FixedShares int
	FixedDollar float64
	Symbol      string
	Price       float64
	// NextSession is the session the entry is for (YYYY-MM-DD), from the scan.
	NextSession string
	Raw         backtestmodels.Signal
}

// NoBuyNote records a stack member that produced no BUY signal this run.
type NoBuyNote struct {
	StrategyID string
	Symbols    []string
	Detail     string
}

// Result is the outcome of one Run call.
type Result struct {
	Signals        []Signal
	NoBuy          []NoBuyNote
	Details        []string
	SymbolsScanned int
	LatestDate     string
	NextSession    string
}

// Run invokes livescan for the given stack members -- in priority order,
// already resolved via pkg/strategies (scan_market never calls
// strategy.Get itself) -- and returns raw BUY candidates. It takes no
// *trader.TraderClient anywhere in its call graph: 0 dependency on Schwab.
func Run(ctx context.Context, db *sql.DB, cfg *appconfig.Config, stackID string, members []strategy.Strategy, extraSymbols []string) (*Result, error) {
	if len(members) == 0 {
		return nil, fmt.Errorf("scan_market: empty strategy stack %q", stackID)
	}
	res := &Result{}
	now := time.Now().UTC()

	memberIDs := make([]string, len(members))
	memberSymbols := make([][]string, len(members))
	allocByID := map[string]float64{}
	cfgByID := map[string]strategy.StrategyConfig{}
	seenSym := map[string]bool{}
	for i, m := range members {
		id := m.ID()
		memberIDs[i] = id
		mcfg := m.DefaultConfig()
		allocByID[id] = mcfg.AllocationPct
		cfgByID[id] = mcfg
		if rp, ok := m.(strategy.RequiredSymbolsProvider); ok {
			for _, s := range rp.RequiredSymbols() {
				s = strings.ToUpper(strings.TrimSpace(s))
				if s == "" {
					continue
				}
				memberSymbols[i] = append(memberSymbols[i], s)
				seenSym[s] = true
			}
		}
		res.Details = append(res.Details, fmt.Sprintf("Stack P%d %s alloc=%.0f%% hold=%dd",
			i, id, mcfg.AllocationPct*100, mcfg.HoldingWindow))
	}
	for _, s := range extraSymbols {
		s = strings.ToUpper(strings.TrimSpace(s))
		if s != "" {
			seenSym[s] = true
		}
	}
	res.SymbolsScanned = len(seenSym)

	marketDB, err := resolveMarketDB(cfg)
	if err != nil {
		return res, err
	}
	res.Details = append(res.Details, fmt.Sprintf("Invoking livescan for stack %s (market db: %s)...", stackID, marketDB))

	outDir := filepath.Join(os.TempDir(), "trade_orchestrator_livescan")
	restore, source, err := useSchwabDailyBars(ctx, cfg)
	if err != nil {
		return res, err
	}
	defer restore()
	res.Details = append(res.Details, "market data source="+source)
	persistBars := syncBars(ctx, db, marketDB, runner.RequiredSymbolsFor(members, ""), &res.Details)
	live, err := runner.RunLiveScan(members, marketDB, outDir)
	persistBars() // also after a failed scan: the download is kept even when a later step fails
	if err != nil {
		err = withDataSource(source, err)
		detail := err.Error()
		res.Details = append(res.Details, detail)
		for i, id := range memberIDs {
			res.NoBuy = append(res.NoBuy, NoBuyNote{StrategyID: id, Symbols: memberSymbols[i], Detail: detail})
		}
		return res, err
	}
	res.LatestDate = live.AsOf
	res.NextSession = live.NextSession
	res.Details = append(res.Details, fmt.Sprintf("livescan as-of %s -> next session %s (%d ENTER signal(s), %d symbol(s) loaded).",
		live.AsOf, live.NextSession, len(live.Signals), live.SymbolsLoaded))

	memberIndex := map[string]int{}
	for i, id := range memberIDs {
		memberIndex[id] = i
	}

	for _, sig := range live.Signals {
		id := strings.TrimSpace(sig.StrategyID)
		prio, ok := memberIndex[id]
		if !ok {
			continue
		}
		dir := sig.Direction
		if dir == "" {
			dir = "LONG"
		}
		if dir != "LONG" && dir != "BUY" {
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
		alloc := allocByID[id] // 0 stays 0: stage_orders skips it rather than guess a size
		res.Signals = append(res.Signals, Signal{
			StrategyID:  id,
			Priority:    prio,
			AllocPct:    alloc,
			Sizing:      cfgByID[id].PositionSizing,
			FixedShares: cfgByID[id].FixedShares,
			FixedDollar: cfgByID[id].FixedDollar,
			Symbol:      sym,
			Price:       price,
			Raw: backtestmodels.Signal{
				Symbol:     sym,
				Date:       live.AsOf,
				Close:      sig.Close,
				BuyLimit:   sig.BuyLimit,
				TakeProfit: sig.TakeProfit,
				StopLoss:   sig.StopLoss,
				Direction:  dir,
				StrategyID: id,
			},
		})
	}

	fired := make(map[string]bool, len(res.Signals))
	for _, s := range res.Signals {
		fired[s.StrategyID] = true
	}
	for i, id := range memberIDs {
		if fired[id] {
			continue
		}
		detail := fmt.Sprintf("no buy signal from livescan as of %s.", live.AsOf)
		res.NoBuy = append(res.NoBuy, NoBuyNote{StrategyID: id, Symbols: memberSymbols[i], Detail: detail})
		res.Details = append(res.Details, fmt.Sprintf("[%s] NO BUY: %s", id, detail))
	}

	if db != nil {
		if err := EnsureSchema(db); err == nil {
			for _, note := range res.NoBuy {
				_, _ = UpsertSignalDay(db, note.StrategyID, live.AsOf, "", "NO_BUY", 0, note.Detail, "NO_BUY", now)
			}
		}
	}

	return res, nil
}

// resolveMarketDB resolves the market history DB path. It does not require
// the file to exist: APP_FOLDER is local disk (ephemeral on App Engine), so a
// cold instance has no file. On Turso, syncBars fills it from market_bars;
// otherwise runner.RunLiveScan's AutoDownload creates and populates it at this path.
func resolveMarketDB(cfg *appconfig.Config) (string, error) {
	if cfg != nil && cfg.MarketDB != "" {
		return cfg.MarketDB, nil
	}
	// backtestgosqlite's appenv is the one authority on where market data
	// lives ($APP_FOLDER/$APP_DATA/market_history.db).
	return appenv.MarketDB(), nil
}
