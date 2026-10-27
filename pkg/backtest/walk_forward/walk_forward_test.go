package walk_forward

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	_ "modernc.org/sqlite"
)

func TestFoldsRollMonthly(t *testing.T) {
	var dates []string
	d := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	for len(dates) < 90 {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			dates = append(dates, d.Format("2006-01-02"))
		}
		d = d.AddDate(0, 0, 1)
	}
	folds, err := Folds(dates, 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(folds) < 2 {
		t.Fatalf("folds = %d", len(folds))
	}
	if folds[0].ISEnd >= folds[0].OOSStart {
		t.Fatalf("IS runs into OOS: %+v", folds[0])
	}
	if folds[1].OOSStart <= folds[0].OOSStart {
		t.Fatalf("second fold did not move forward: %+v then %+v", folds[0], folds[1])
	}
}

func TestRunStrategyWritesSummary(t *testing.T) {
	strat, ok := strategy.Get("tsll-daily-one-share")
	if !ok {
		t.Fatal("tsll-daily-one-share is not registered")
	}
	var bars []models.Bar
	d := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	px := 20.0
	for len(bars) < 80 {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			bars = append(bars, models.Bar{
				Symbol: "TSLL", Date: d.Format("2006-01-02"),
				Open: px, High: px + 0.05, Low: px - 0.05, Close: px, Volume: 1000,
			})
			px += 0.01
		}
		d = d.AddDate(0, 0, 1)
	}
	rows, err := RunStrategy(context.Background(), strat, map[string][]models.Bar{"TSLL": bars}, nil, Options{TrainMonths: 1, TestMonths: 1, StepMonths: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 || rows[0].StrategyID != "tsll-daily-one-share" {
		t.Fatalf("rows = %+v", rows)
	}
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "wf.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Save(db, strat.ID(), rows); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM walk_forward_summary WHERE strategy_id = ?`, strat.ID()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("summary rows = %d", n)
	}
}

func TestFoldsKeepIdleDaysAndSummaryTotalsThem(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "wf.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// A fold table made before idle days existed: EnsureSchema must add the columns.
	if _, err := db.Exec(`CREATE TABLE walk_forward_fold (
		strategy_id TEXT NOT NULL, fold INTEGER NOT NULL, is_start TEXT NOT NULL, is_end TEXT NOT NULL,
		oos_start TEXT NOT NULL, oos_end TEXT NOT NULL, is_sharpe REAL NOT NULL, oos_sharpe REAL NOT NULL,
		is_return_pct REAL NOT NULL, oos_return_pct REAL NOT NULL, is_trades INTEGER NOT NULL, oos_trades INTEGER NOT NULL,
		is_max_dd REAL NOT NULL, oos_max_dd REAL NOT NULL, trials INTEGER NOT NULL DEFAULT 1, PRIMARY KEY (strategy_id, fold))`); err != nil {
		t.Fatal(err)
	}
	rows := []FoldRow{
		{StrategyID: "s", Fold: 1, ISStart: "a", ISEnd: "b", OOSStart: "c", OOSEnd: "d", ISIdleDays: 30, ISDays: 100, OOSIdleDays: 10, OOSDays: 50, Trials: 1},
		{StrategyID: "s", Fold: 2, ISStart: "a", ISEnd: "b", OOSStart: "c", OOSEnd: "d", ISIdleDays: 20, ISDays: 100, OOSIdleDays: 20, OOSDays: 50, Trials: 1},
	}
	if err := Save(db, "s", rows); err != nil {
		t.Fatal(err)
	}
	var idle, days int
	var pct float64
	if err := db.QueryRow(`SELECT oos_idle_days, oos_days, oos_idle_pct FROM walk_forward_summary WHERE strategy_id = 's'`).Scan(&idle, &days, &pct); err != nil {
		t.Fatal(err)
	}
	if idle != 30 || days != 100 || pct != 0.3 {
		t.Fatalf("summary idle = %d of %d (%v), want 30 of 100 (0.3)", idle, days, pct)
	}
	var isIdle int
	if err := db.QueryRow(`SELECT is_idle_days FROM walk_forward_fold WHERE fold = 2`).Scan(&isIdle); err != nil || isIdle != 20 {
		t.Fatalf("fold is_idle_days = %d (%v), want 20", isIdle, err)
	}
	if got := FormatRows(rows); !strings.Contains(got, "10/50") {
		t.Errorf("the fold table should show out-of-sample idle days:\n%s", got)
	}
}
