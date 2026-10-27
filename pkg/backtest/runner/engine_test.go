package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func engineBars() ([]models.Bar, []string) {
	closes := []float64{100, 99, 98, 103, 104, 105, 106, 107, 108, 109, 110, 111}
	dates := []string{"2026-01-02", "2026-01-05", "2026-01-06", "2026-01-07", "2026-01-08", "2026-01-09",
		"2026-01-12", "2026-01-13", "2026-01-14", "2026-01-15", "2026-01-16", "2026-01-20"}
	var bars []models.Bar
	for i, c := range closes {
		bars = append(bars, models.Bar{Symbol: "AAA", Date: dates[i], Open: c, High: c + 0.5, Low: c - 0.5, Close: c, AdjClose: c, Volume: 1000})
	}
	return bars, dates
}

func runEngine(t *testing.T, margin bool) RunResult {
	t.Helper()
	bars, dates := engineBars()
	market := marketDB(t, map[string][]models.Bar{"AAA": bars})
	strat := streakRow("engine_test", "AAA", 1, 0.5, 0.05, 0.5)
	cfg := strat.DefaultConfig()
	cfg.UseMargin = margin
	res := ExecuteStrategy(strat, cfg, map[string][]models.Bar{"AAA": bars}, dates, 100000, "", t.TempDir(), market)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	return res
}

func storedEngine(t *testing.T, res RunResult) string {
	t.Helper()
	db, err := storage.OpenSQLiteReadOnly(res.DbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var engine *string
	if err := db.Get(&engine, `SELECT engine FROM runs WHERE run_id = ?`, res.RunID); err != nil {
		t.Fatal(err)
	}
	if engine == nil {
		return ""
	}
	return *engine
}

// A backtest runs on the hourly simulator and the stored run says so.
func TestBacktestUsesTheHourlySimulatorAndRecordsIt(t *testing.T) {
	res := runEngine(t, false)
	if res.Engine != storage.EngineHourly {
		t.Fatalf("engine %q, want %q", res.Engine, storage.EngineHourly)
	}
	if got := storedEngine(t, res); got != storage.EngineHourly {
		t.Fatalf("stored engine %q", got)
	}
	if len(res.Trades) == 0 {
		t.Fatal("no trades; the series does not exercise the engine")
	}
	for _, tr := range res.Trades {
		if tr.EntryDate == tr.ExitDate && tr.ExitReason == models.ExitReasonProfitTarget {
			t.Errorf("a daily-bar run booked a same-session target: %+v", tr)
		}
	}
}

// A setting the hourly simulator does not model is refused with its name, never run on something that
// would mislead.
func TestUnsupportedSettingIsRefusedWithItsName(t *testing.T) {
	bars, dates := engineBars()
	market := marketDB(t, map[string][]models.Bar{"AAA": bars})
	strat := streakRow("engine_test", "AAA", 1, 0.5, 0.05, 0.5)
	cfg := strat.DefaultConfig()
	cfg.UseMargin = true
	res := ExecuteStrategy(strat, cfg, map[string][]models.Bar{"AAA": bars}, dates, 100000, "", t.TempDir(), market)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "margin") {
		t.Fatalf("err %v, want a refusal naming margin", res.Err)
	}
}

func storedColumn(t *testing.T, res RunResult, col string) string {
	t.Helper()
	db, err := storage.OpenSQLiteReadOnly(res.DbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v *string
	if err := db.Get(&v, `SELECT `+col+` FROM runs WHERE run_id = ?`, res.RunID); err != nil {
		t.Fatal(err)
	}
	if v == nil {
		return ""
	}
	return *v
}

// A run that traded through an unadjusted split is flagged, and the flag is stored with the run.
func TestRunThroughAnUnadjustedSplitIsSuspect(t *testing.T) {
	bars, dates := engineBars()
	for i := 3; i < len(bars); i++ { // a 2-for-1 split that was never adjusted for
		bars[i].Open, bars[i].High, bars[i].Low, bars[i].Close, bars[i].AdjClose = bars[i].Open/2, bars[i].High/2, bars[i].Low/2, bars[i].Close/2, bars[i].Close/2
	}
	market := marketDB(t, map[string][]models.Bar{"AAA": bars})
	strat := streakRow("split_test", "AAA", 1, 0.5, 0.05, 0.5)
	res := ExecuteStrategy(strat, strat.DefaultConfig(), map[string][]models.Bar{"AAA": bars}, dates, 100000, "", t.TempDir(), market)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !strings.Contains(res.Suspect, "AAA 2026-01-07") {
		t.Fatalf("suspect %q, want the split on 2026-01-07 named", res.Suspect)
	}
	if got := storedColumn(t, res, "suspect"); got != res.Suspect {
		t.Fatalf("stored suspect %q, want %q", got, res.Suspect)
	}
}

func TestACleanRunIsNotSuspect(t *testing.T) {
	if res := runEngine(t, false); res.Suspect != "" {
		t.Fatalf("suspect %q on clean bars", res.Suspect)
	}
}

func TestRowProblemNamesARowThatCanNeverFill(t *testing.T) {
	cfg := streakRow("x", "AAA", 1, 0.5, 0.05, 0.10).DefaultConfig() // a 10% stop: 90% of the entry
	cfg.EntryLimitPct = 0.85                                         // limit at 85%: under the stop
	if RowProblem(cfg) == "" {
		t.Fatal("a limit under its stop was not flagged")
	}
	cfg.EntryLimitPct = 0.95
	if why := RowProblem(cfg); why != "" {
		t.Fatalf("a limit over its stop was flagged: %s", why)
	}
	cfg.EntryLimitPct = 0
	if why := RowProblem(cfg); why != "" {
		t.Fatalf("no limit was flagged: %s", why)
	}
}

// A kept calculation database from an earlier run is removed before the next run uses its path.
func TestFreshCalcFileRemovesWhatAnEarlierRunLeft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calc_x.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.WriteFile(path+suffix, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := freshCalcFile(path); got != path {
		t.Fatalf("path %q", got)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err == nil {
			t.Errorf("%s survived", path+suffix)
		}
	}
}

// A stored run with no engine was made by the daily simulator: it is flagged and never skipped as done.
func TestRunsFromBeforeTheHourlyEngineAreRedone(t *testing.T) {
	m := map[string]CompiledResult{"old": {StrategyID: "old"}, "new": {StrategyID: "new", Engine: storage.EngineHourly}, "legacy": {StrategyID: "legacy", Engine: storage.EngineDaily}}
	if n := DropStale(m); n != 1 {
		t.Fatalf("dropped %d, want only the run with no engine", n)
	}
	if _, ok := m["old"]; ok || len(m) != 2 {
		t.Fatalf("left %v", m)
	}
}
