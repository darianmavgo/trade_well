package market_history

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func putBars(t *testing.T, path string, rows [][2]string) {
	t.Helper()
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.EnsureBarTable(db, "backtest_start"); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO backtest_start (symbol, Date, timeframe, open, high, low, close, volume) VALUES (?, ?, ?, 1, 1, 1, 1, 1)`,
			r[0], r[1], map[bool]string{true: "1d", false: "1h"}[len(r[1]) == 10]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUpdateDryRunPicksOnlySymbolsWithGaps(t *testing.T) {
	dir := t.TempDir()
	daily := filepath.Join(dir, "market_history.db")
	hourly := filepath.Join(dir, "market_history_hourly.db")
	days := []string{"2021-01-04", "2021-01-05", "2021-01-06"}
	var d, h [][2]string
	for _, day := range days {
		for _, sym := range []string{"SPY", "FULL", "GAPPY"} {
			d = append(d, [2]string{sym, day})
		}
		h = append(h, [2]string{"FULL", day + " 14:00:00"})
	}
	h = append(h, [2]string{"GAPPY", "2021-01-04 14:00:00"}, [2]string{"GAPPY", "2021-01-06 14:00:00"})
	putBars(t, daily, d)
	putBars(t, hourly, h)

	cfg := DefaultUpdateConfig()
	cfg.DB, cfg.DryRun, cfg.Timeframe = daily, true, "1h"
	var out bytes.Buffer
	cfg.Out = &out
	need, err := Update(context.Background(), cfg, []string{"FULL", "GAPPY"})
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 0 {
		t.Errorf("a dry run downloads nothing, got %v", need)
	}
	text := out.String()
	if !strings.Contains(text, "1 of 2 symbols are missing") || !strings.Contains(text, "GAPPY  1 of 3 sessions missing") || strings.Contains(text, "FULL   ") {
		t.Errorf("report:\n%s", text)
	}
}

func TestUpdateNeedsSymbols(t *testing.T) {
	cfg := DefaultUpdateConfig()
	cfg.DB = filepath.Join(t.TempDir(), "m.db")
	if _, err := Update(context.Background(), cfg, nil); err == nil {
		t.Fatal("expected an error with no symbols")
	}
}

func TestUpdateDefaultsToHourlyBars(t *testing.T) {
	if got := DefaultUpdateConfig().Timeframe; got != "1h" {
		t.Errorf("default timeframe = %q, want 1h", got)
	}
}
