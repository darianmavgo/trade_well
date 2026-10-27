package runner

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/streak_strategy"
)

// streakRow is a real streak_strategy row: drop signalDays days in a row on sym,
// then buy sym and hold. Its allocation, hold and exits are the row's.
func streakRow(id, sym string, signalDays int, allocation, takeProfit, stopLoss float64) *streak_strategy.Strategy {
	return &streak_strategy.Strategy{Row: refdb.StreakStrategy{
		ID: id, Name: id, SignalSymbol: sym, TradeSymbol: sym, Direction: "drop",
		SignalDays: signalDays, HoldDays: 8, TakeProfitPct: takeProfit, StopLossPct: stopLoss,
		Regime: "All Regimes", AllocationPct: allocation,
	}}
}

// marketDB writes bars into a real temporary SQLite market database.
func marketDB(t *testing.T, bars map[string][]models.Bar) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "market.db")
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE backtest_start (
		idx INTEGER, symbol TEXT, Date TEXT, timeframe TEXT,
		open REAL, high REAL, low REAL, close REAL, volume INTEGER, "Adj Close" REAL)`); err != nil {
		t.Fatal(err)
	}
	i := 0
	for sym, series := range bars {
		for _, b := range series {
			if _, err := db.Exec(`INSERT INTO backtest_start (idx, symbol, Date, timeframe, open, high, low, close, volume, "Adj Close")
				VALUES (?, ?, ?, '1d', ?, ?, ?, ?, 1000, ?)`, i, sym, b.Date, b.Open, b.High, b.Low, b.Close, b.Close); err != nil {
				t.Fatal(err)
			}
			i++
		}
	}
	return path
}

func TestExecuteStackAllocOverrideSizesTenPercent(t *testing.T) {
	const price = 100.0
	bar := func(sym string) []models.Bar {
		return []models.Bar{
			{Date: "2026-01-02", Open: price, High: price, Low: price, Close: price},
			{Date: "2026-01-05", Open: price, High: price, Low: price, Close: price},
			// The hourly engine does not count a position entered on the last bar of the data as
			// a trade (nothing can exit it), so the data runs one session past the entry.
			{Date: "2026-01-06", Open: price, High: price, Low: price, Close: price},
		}
	}
	sig := func(id, sym string, priority int) models.Signal {
		return models.Signal{
			Date: "2026-01-02", Symbol: sym, Close: price, BuyLimit: price,
			StrategyID: id, Priority: priority, OrderType: "limit",
		}
	}
	primary := streakRow("primary", "AAA", 1, 0.65, 0, 0)
	secondary := streakRow("secondary", "BBB", 1, 0.65, 0, 0)
	res := ExecuteStack(StackRequest{
		Primary:      primary,
		Secondaries:  []strategy.Strategy{secondary},
		BarsBySymbol: map[string][]models.Bar{"AAA": bar("AAA"), "BBB": bar("BBB")},
		SortedDates:  []string{"2026-01-02", "2026-01-05", "2026-01-06"},
		Capital:      100000,
		Persist:      false,
		Signals:      []models.Signal{sig("primary", "AAA", 0), sig("secondary", "BBB", 1)},
		Override:     ConfigOverride{AllocPct: 0.10},
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.AllocPct != 0.10 {
		t.Errorf("AllocPct = %v, want 0.10", res.AllocPct)
	}
	if res.PreemptedCount != 0 {
		t.Errorf("preempted %d trades; 10%% slots on different symbols should both fit", res.PreemptedCount)
	}
	if len(res.Trades) != 2 {
		t.Fatalf("trades = %d, want 2", len(res.Trades))
	}
	for _, tr := range res.Trades {
		if tr.Shares != 100 {
			t.Errorf("%s shares = %d, want 100 (10%% of $100k at $100), default alloc was 65%%", tr.StrategyID, tr.Shares)
		}
	}
}

func TestExecuteStackDefaultAssetWritesStackDB(t *testing.T) {
	const price = 100.0
	dates := []string{"2026-01-02", "2026-01-05"}
	bars := func(px float64) []models.Bar {
		out := make([]models.Bar, len(dates))
		for i, d := range dates {
			out[i] = models.Bar{Date: d, Open: px, High: px, Low: px, Close: px, AdjClose: px}
		}
		return out
	}
	primary := streakRow("primary", "AAA", 1, 0.10, 0, 0)
	res := ExecuteStack(StackRequest{
		Primary:      primary,
		BarsBySymbol: map[string][]models.Bar{"PARK": bars(price), "AAA": bars(price)},
		SortedDates:  dates,
		Capital:      100000,
		Persist:      true,
		OutDir:       t.TempDir(),
		Signals:      []models.Signal{},
		DefaultAsset: "park",
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.Default.Symbol != "PARK" {
		t.Fatalf("default symbol %q, want PARK", res.Default.Symbol)
	}
	if filepath.Base(res.DbPath) != "stack.db" {
		t.Fatalf("db path %s, want stack.db in the run folder", res.DbPath)
	}
	if res.Default.AvgWeight < 0.9 {
		t.Fatalf("avg park weight %.3f, want most of the book", res.Default.AvgWeight)
	}
	if res.Idle.DaysFullyIdle != 0 {
		t.Fatalf("idle days %d, want 0 while the park is held", res.Idle.DaysFullyIdle)
	}
}

func TestBuildConfigAllocForcesFixedPct(t *testing.T) {
	cfg := strategy.StrategyConfig{
		ID: "one-share", AllocationPct: 1, PositionSizing: "fixed_shares",
		FixedShares: 1, PositionCap: 1, HoldingWindow: 1,
	}
	over := ConfigOverride{AllocPct: 0.10}.Apply(cfg)
	if over.AllocationPct != 0.10 || over.PositionSizing != "fixed_pct" || !over.FlatAllocation {
		t.Fatalf("override = %+v, want 10%% fixed_pct", over)
	}
	kept := ConfigOverride{}.Apply(cfg)
	if kept.AllocationPct != 1 || kept.PositionSizing != "fixed_shares" || kept.FlatAllocation {
		t.Fatalf("zero override changed config: %+v", kept)
	}
}

// registerTempStreaks registers a streak family read from a temporary
// strategies database holding one row per id, so no real file is touched.
func registerTempStreaks(t *testing.T, ids ...string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "strategies.db")
	db, err := refdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range ids {
		if _, err := db.Exec(`INSERT INTO streak_strategy
			(id, name, signal_symbol, trade_symbol, direction, signal_days, hold_days, take_profit_pct, stop_loss_pct, regime, allocation_pct, cash_yield, slippage_pct, next_day_limit)
			VALUES (?, ?, 'VOO', 'TECL', 'drop', 3, 8, 0, 0, 'All Regimes', 0.1, 0, 0, 0)`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	streak_strategy.RegisterFrom(path)
}

func TestOverlayCandidates_SkipsPrimaryAndSQLDuplicates(t *testing.T) {
	registerTempStreaks(t, "streak-voo-buy-tecl", "streak-gld-down3", "streak-mara-down3")
	primary, ok := strategy.Get("streak-voo-buy-tecl")
	if !ok {
		t.Fatal("streak-voo-buy-tecl not registered")
	}
	cands := OverlayCandidates(primary, OverlayCandidateOptions{})
	seen := map[string]bool{}
	for _, c := range cands {
		if c.ID() == "streak-voo-buy-tecl" {
			t.Error("primary should not be an overlay candidate")
		}
		if c.ID() == "voo-buy-hold" || c.ID() == "genetic-momentum" {
			t.Errorf("heavy strategy %s should be excluded by default", c.ID())
		}
		if len(c.ID()) >= 4 && c.ID()[len(c.ID())-4:] == "-sql" {
			t.Errorf("SQL duplicate %s should be excluded", c.ID())
		}
		seen[c.ID()] = true
	}
	for _, want := range []string{"streak-gld-down3", "streak-mara-down3"} {
		if !seen[want] {
			t.Errorf("expected overlay %s in default candidates", want)
		}
	}
}

func TestOverlayCandidates_ExplicitIDs(t *testing.T) {
	registerTempStreaks(t, "streak-voo-buy-tecl", "streak-gld-down3")
	primary, ok := strategy.Get("streak-voo-buy-tecl")
	if !ok {
		t.Fatal("streak-voo-buy-tecl not registered")
	}
	cands := OverlayCandidates(primary, OverlayCandidateOptions{
		ExplicitIDs: []string{"streak-gld-down3", "streak-voo-buy-tecl", "no-such-strategy"},
	})
	if len(cands) != 1 || cands[0].ID() != "streak-gld-down3" {
		t.Fatalf("explicit IDs = %v, want [streak-gld-down3]", idsOf(cands))
	}
}

func idsOf(strats []strategy.Strategy) []string {
	out := make([]string, len(strats))
	for i, s := range strats {
		out[i] = s.ID()
	}
	return out
}

func TestExecuteStackEval_RanksProfitableOverlayFirst(t *testing.T) {
	dates := []string{"2026-01-01", "2026-01-02", "2026-01-05", "2026-01-06", "2026-01-07", "2026-01-08", "2026-01-09", "2026-01-12"}
	series := func(closes ...float64) []models.Bar {
		out := make([]models.Bar, len(closes))
		for i, c := range closes {
			out[i] = models.Bar{Date: dates[i], Open: c, High: c, Low: c, Close: c, AdjClose: c, Volume: 1000}
		}
		return out
	}
	bars := map[string][]models.Bar{
		// TECL never drops two days in a row, so the primary stays flat and overlays get the idle cash.
		"TECL": series(50, 50, 50, 50, 52, 52, 54, 54),
		// GLD drops two days, then rallies: its streak entry wins.
		"GLD": series(100, 99, 98, 110, 120, 130, 140, 150),
		// LOS drops two days, then keeps falling through its stop: its entry loses.
		"LOS": series(100, 95, 90, 70, 50, 40, 30, 20),
	}
	primary := streakRow("primary-tecl", "TECL", 2, 0.65, 0, 0)
	winner := streakRow("gld-winner", "GLD", 2, 0.50, 0.50, 0)
	loser := streakRow("los-loser", "LOS", 2, 0.50, 0, 0.50)

	result := ExecuteStackEval(StackEvalOptions{
		Primary:      primary,
		Candidates:   []strategy.Strategy{loser, winner},
		BarsBySymbol: bars,
		SortedDates:  dates,
		Capital:      100000,
		OutDir:       t.TempDir(),
		MarketDBPath: marketDB(t, bars),
		Concurrency:  2,
		StackDepth:   1,
	})

	if len(result.Overlays) != 2 {
		t.Fatalf("overlays = %d, want 2", len(result.Overlays))
	}
	if result.Overlays[0].Secondary.ID() != "gld-winner" {
		t.Errorf("rank 1 = %s, want gld-winner (profitable idle overlay)", result.Overlays[0].Secondary.ID())
	}
	if result.Overlays[0].IncrementalEquity <= 0 {
		t.Errorf("gld-winner incremental equity = %.2f, want > 0", result.Overlays[0].IncrementalEquity)
	}
	if got := result.Overlays[0].TradedSymbols; len(got) != 1 || got[0] != "GLD" {
		t.Errorf("gld-winner traded %v, want [GLD]", got)
	}
	if result.Overlays[1].IncrementalEquity >= 0 {
		t.Errorf("los-loser incremental equity = %.2f, want < 0 (it is stopped out)", result.Overlays[1].IncrementalEquity)
	}
	if result.Baseline.Idle.DaysFullyIdle == 0 {
		t.Errorf("primary should be fully idle in this fixture, got %+v", result.Baseline.Idle)
	}
}

func TestPickComplementaryOverlays_SkipsSharedSymbols(t *testing.T) {
	a := OverlayEval{
		Secondary:         streakRow("a", "X", 1, 0.1, 0, 0),
		IncrementalEquity: 1000,
		TradedSymbols:     []string{"GLD"},
	}
	b := OverlayEval{
		Secondary:         streakRow("b", "X", 1, 0.1, 0, 0),
		IncrementalEquity: 900,
		TradedSymbols:     []string{"GLD", "SLV"},
	}
	c := OverlayEval{
		Secondary:         streakRow("c", "X", 1, 0.1, 0, 0),
		IncrementalEquity: 800,
		TradedSymbols:     []string{"MARA"},
	}
	picked := pickComplementaryOverlays([]OverlayEval{a, b, c}, 3)
	if len(picked) != 2 {
		t.Fatalf("picked %d, want 2 (a and c; b shares GLD with a)", len(picked))
	}
	if picked[0].Secondary.ID() != "a" || picked[1].Secondary.ID() != "c" {
		t.Errorf("picked %s, %s; want a, c", picked[0].Secondary.ID(), picked[1].Secondary.ID())
	}
}
