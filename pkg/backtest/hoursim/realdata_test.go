package hoursim_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/hoursim"
	"github.com/darianmavgo/trade_well/pkg/backtest/realbars"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/sessions"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
)

// TestRealDataOnTheHourlySimulator plays real signals over real hourly bars and checks the run's own
// guarantees (no trade exits before it enters, one curve point per session). The daily-vs-hourly
// comparison it once printed is in docs/HourlyEngineParity.md; the daily simulator is gone.
//
// HOURSIM_REAL_DB is a market database holding hourly rows (data/market_history_hourly.db by default) and
// HOURSIM_REAL_STRATEGIES a comma list of strategy ids; the test is skipped without the data.
func TestRealDataOnTheHourlySimulator(t *testing.T) {
	src := os.Getenv("HOURSIM_REAL_DB")
	if src == "" {
		src = filepath.Join("..", "..", "data", "market_history_hourly.db")
	}
	if fi, err := os.Stat(src); err != nil || fi.Size() == 0 {
		t.Skipf("hourly market data %s not present", src)
	}
	ids := strings.Split(envOr("HOURSIM_REAL_STRATEGIES", "streak-voo-buy-tecl,streak-shpu-down3"), ",")

	// The repository root: the Markov models are read from its data/ (read only here).
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_FOLDER", root)
	stratreg.RegisterFamiliesAt(realbars.StrategiesDB(t))

	var strats []strategy.Strategy
	symSet := map[string]bool{}
	for _, id := range ids {
		s, ok := strategy.Get(strings.TrimSpace(id))
		if !ok {
			t.Skipf("strategy %q is not in the strategies database", id)
		}
		strats = append(strats, s)
		for _, sym := range s.(strategy.RequiredSymbolsProvider).RequiredSymbols() {
			symSet[sym] = true
		}
	}
	var symbols []string
	for s := range symSet {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)

	market := filepath.Join(t.TempDir(), "market_history_hourly.db") // hourly bars and their sessions share this file
	db, err := storage.OpenSQLite(market)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.EnsureBarTable(db, "backtest_start"); err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS src", strings.ReplaceAll(src, "'", "''"))); err != nil {
		t.Fatal(err)
	}
	quoted := "'" + strings.Join(symbols, "','") + "'"
	if _, err := db.Exec(`INSERT INTO backtest_start (Date, timeframe, asset_class, open, high, low, close, "Adj Close", volume, symbol)
		SELECT substr(replace(Date, 'T', ' '), 1, 19), '1h', 'equity', open, high, low, close, "Adj Close", volume, symbol
		FROM src.backtest_start WHERE timeframe = '1h' AND symbol IN (` + quoted + `)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DETACH DATABASE src"); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Derive(db, "backtest_start"); err != nil {
		t.Fatal(err)
	}
	daily, dates, err := storage.FetchBars(db, "backtest_start", symbols, "", "")
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	hourly, err := runner.LoadIntraday(market, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(dates) < 100 {
		t.Skipf("only %d sessions copied", len(dates))
	}

	t.Logf("%-24s %-9s %6s %6s %8s %8s %8s", "strategy", "engine", "trades", "win%", "return%", "maxDD%", "daily-bar sessions")
	for _, s := range strats {
		s.SetDatabases(market, filepath.Join(t.TempDir(), "calc.db"))
		signals := s.GenerateSignals(daily)
		if len(signals) == 0 {
			t.Logf("%-24s no signals", s.ID())
			continue
		}
		cfg := s.DefaultConfig()

		out, err := hoursim.Run(hoursim.Input{Signals: signals, Daily: daily, Hourly: hourly, Dates: dates, Config: cfg, Capital: 100000})
		if err != nil {
			t.Fatalf("%s: %v", s.ID(), err)
		}
		fallback := 0
		for _, ds := range out.DailySessions {
			fallback += len(ds)
		}
		t.Logf("%-24s %-9s %6d %6.1f %8.1f %8.1f %8d", s.ID(), "hourly", len(out.Trades), 100*out.Report.WinRate, 100*out.Report.TotalReturnPct, 100*out.Report.MaxDrawdownPct, fallback)
		for _, tr := range out.Trades {
			if tr.ExitDate < tr.EntryDate {
				t.Errorf("%s: exits before it enters: %+v", s.ID(), tr)
			}
		}
		if len(out.Curve) != len(dates) {
			t.Errorf("%s: curve has %d points for %d sessions", s.ID(), len(out.Curve), len(dates))
		}
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
