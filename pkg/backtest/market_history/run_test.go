package market_history

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/universe"
)

func TestParseSymbolArgs(t *testing.T) {
	got := parseSymbolArgs([]string{"VOO,", "IEF,", "GLD,", "USO,", "HYG"})
	want := []string{"VOO", "IEF", "GLD", "USO", "HYG"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	got = parseSymbolArgs([]string{"voo,ief", " gld "})
	want = []string{"VOO", "IEF", "GLD"}
	if len(got) != len(want) || got[0] != "VOO" || got[1] != "IEF" || got[2] != "GLD" {
		t.Fatalf("got %v, want %v", got, want)
	}
	if len(parseSymbolArgs(nil)) != 0 {
		t.Fatal("empty args should yield no symbols")
	}
}

func TestRunRequiresDBAndPolygonKey(t *testing.T) {
	if _, err := Run(context.Background(), Config{}); err == nil {
		t.Fatal("expected error for empty DB")
	}
	// Run must not fall back to POLYGON_API_KEY from the environment.
	t.Setenv("POLYGON_API_KEY", "from-env")
	_, err := Run(context.Background(), Config{DB: filepath.Join(t.TempDir(), "m.db"), Source: "polygon", Symbols: "SPY"})
	if err == nil {
		t.Fatal("expected error: polygon source without Config.PolygonKey")
	}
}

func TestRunStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	_, err := Run(ctx, Config{DB: filepath.Join(t.TempDir(), "m.db"), Symbols: "SPY,QQQ", AlpacaKey: "k", AlpacaSecret: "s", Out: &out})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestRunNilOutIsSilentAndSafe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// nil Out must not panic.
	_, _ = Run(ctx, Config{DB: filepath.Join(t.TempDir(), "m.db"), Symbols: "SPY"})
}

func TestRunUnleveragedETFsSkipsSymbolsAlreadyInHourlyDB(t *testing.T) {
	dir := t.TempDir()
	udbPath := filepath.Join(dir, "universe.db")
	udb, err := universe.Open(udbPath)
	if err != nil {
		t.Fatal(err)
	}
	etf := func(sym, lev string) universe.SymbolRecord {
		return universe.SymbolRecord{Symbol: sym, AssetType: "ETF", IsETF: true, Leverage: lev, Direction: "long", Category: "other", Active: true}
	}
	if err := universe.SaveSymbols(udb, []universe.SymbolRecord{etf("SPY", "none"), etf("TQQQ", "3x")}); err != nil {
		t.Fatal(err)
	}
	udb.Close()

	// SPY already has the whole requested window in the hourly database.
	daily := filepath.Join(dir, "market_history.db")
	hdb, err := storage.OpenSQLite(appenv.BarDB(daily, "1h"))
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.EnsureBarTable(hdb, "backtest_start"); err != nil {
		t.Fatal(err)
	}
	bar := func(d string) models.Bar {
		return models.Bar{Symbol: "SPY", Date: d, Timeframe: "1h", Open: 1, High: 2, Low: 1, Close: 2, Volume: 5}
	}
	if err := storage.UpsertBars(hdb, "backtest_start", []models.Bar{bar("2026-01-05 15:00:00"), bar("2026-01-09 20:00:00")}); err != nil {
		t.Fatal(err)
	}
	hdb.Close()

	var out bytes.Buffer
	sum, err := Run(context.Background(), Config{
		DB: daily, UniverseDB: udbPath, UnleveragedETFs: true, Source: "polygon", PolygonKey: "k",
		Timeframe: "1h", Start: "2026-01-05", End: "2026-01-09", Rate: 5, Out: &out,
	})
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if sum.Symbols != 1 || sum.CachedSymbols != 1 || sum.NewBars != 0 {
		t.Fatalf("summary %+v, want only SPY, served from market_history_hourly.db with no request\n%s", sum, out.String())
	}
}

func TestRunStatusReportsPulledAndLeft(t *testing.T) {
	daily := filepath.Join(t.TempDir(), "market_history.db")
	hdb, err := storage.OpenSQLite(appenv.BarDB(daily, "1h"))
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.EnsureBarTable(hdb, "backtest_start"); err != nil {
		t.Fatal(err)
	}
	bar := func(sym, d string) models.Bar {
		return models.Bar{Symbol: sym, Date: d, Timeframe: "1h", Open: 1, High: 2, Low: 1, Close: 2, Volume: 5}
	}
	if err := storage.UpsertBars(hdb, "backtest_start", []models.Bar{
		bar("SPY", "2025-12-29 15:00:00"), bar("SPY", "2026-01-09 20:00:00"), // whole window
		bar("QQQ", "2026-01-07 15:00:00"), bar("QQQ", "2026-01-09 20:00:00"), // starts late
	}); err != nil {
		t.Fatal(err)
	}
	hdb.Close()

	var out bytes.Buffer
	// No polygon key: a status report makes no request.
	sum, err := Run(context.Background(), Config{
		DB: daily, Symbols: "SPY,QQQ,IWM", Source: "polygon", Timeframe: "1h",
		Start: "2025-12-29", End: "2026-01-09", Rate: 5, Status: true, Out: &out,
	})
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if sum.Symbols != 3 || sum.CachedSymbols != 1 || sum.UpdatedSymbols != 2 {
		t.Fatalf("summary %+v, want 3 requested, 1 pulled, 2 left\n%s", sum, out.String())
	}
	for _, want := range []string{"already pulled    : 1", "partly pulled     : 1", "not pulled        : 1", "at least 2 requests", "next up           : QQQ IWM"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}
}

func TestSymbolsAcceptSymbolListIDs(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "strategies.db")
	rdb, err := refdb.Open(settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rdb.Exec(`INSERT OR REPLACE INTO symbol_lists (symbol_list_id, list) VALUES ('my-list', 'AAA,BBB,SPY')`); err != nil {
		t.Fatal(err)
	}
	rdb.Close()

	got, err := expandSymbolLists(settings, []string{"spy", " My-List ", "ccc"})
	want := []string{"SPY", "AAA", "BBB", "CCC"}
	if err != nil || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, %v; want %v (list expanded, tickers upper-cased, no repeats)", got, err, want)
	}

	// Through Run: the list's three symbols are what the status report covers.
	var out bytes.Buffer
	sum, err := Run(context.Background(), Config{
		DB: filepath.Join(t.TempDir(), "market_history.db"), SettingsDB: settings, Symbols: "my-list",
		Timeframe: "1h", Start: "2026-01-05", End: "2026-01-09", Status: true, Out: &out,
	})
	if err != nil || sum.Symbols != 3 {
		t.Fatalf("Run: %+v, %v\n%s", sum, err, out.String())
	}
}
