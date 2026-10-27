package reliability

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

func putBars(t *testing.T, path string, rows [][]any) {
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
		tf := "1d"
		if len(r[1].(string)) > 10 {
			tf = "1h"
		}
		if _, err := db.Exec(`INSERT INTO backtest_start (symbol, Date, timeframe, open, high, low, close, volume) VALUES (?, ?, ?, ?, ?, ?, ?, 1000)`,
			r[0], r[1], tf, r[2], r[3], r[4], r[5]); err != nil {
			t.Fatal(err)
		}
	}
}

// market writes a daily database with AAA bars on 2021-03-01..03 and, when
// hourly is set, a full hourly session on 2021-03-02 (EST: 14:00 UTC is 9:00).
func market(t *testing.T, hourly bool) string {
	dir := t.TempDir()
	daily := filepath.Join(dir, "market_history.db")
	putBars(t, daily, [][]any{
		{"AAA", "2021-03-01", 100.0, 101.0, 99.0, 100.0},
		{"AAA", "2021-03-02", 100.0, 104.0, 94.0, 103.0},
		{"AAA", "2021-03-03", 103.0, 105.0, 102.0, 104.0},
	})
	if hourly {
		var rows [][]any
		for h := 14; h <= 20; h++ {
			// opens at 100, dips to 94.9 in the first hour, then climbs to 104
			lo, hi := 99.0, 100.5
			if h == 14 {
				lo = 94.9
			}
			if h >= 16 {
				lo, hi = 100.0+float64(h-16), 101.0+float64(h-16)
			}
			rows = append(rows, []any{"AAA", fmt.Sprintf("2021-03-02 %02d:00:00", h), 100.0, hi, lo, 100.0})
		}
		putBars(t, filepath.Join(dir, "market_history_hourly.db"), rows)
	}
	return daily
}

func input(db string, trades []models.Trade) Input {
	return Input{
		MarketDB: db, Table: "backtest_start", Strategy: strategy.NewPark("AAA"),
		Config:        strategy.StrategyConfig{EntryLimitPct: 0.95},
		Trades:        trades,
		Report:        models.PerformanceReport{StartDate: "2021-03-01", EndDate: "2021-03-03", TotalTrades: len(trades)},
		HoldoutMonths: 12, Phase: "in-sample",
	}
}

func byCode(fs []Finding, code string) Finding {
	for _, f := range fs {
		if f.Code == code {
			return f
		}
	}
	return Finding{}
}

func sameDayProfit() models.Trade {
	return models.Trade{Symbol: "AAA", OrderType: "limit", EntryDate: "2021-03-02", ExitDate: "2021-03-02",
		EntryPrice: 95, TargetPrice: 97.85, StopLossPrice: 90.25, ExitPrice: 97.85, ExitReason: models.ExitReasonProfitTarget, NetPnL: 100}
}

func TestSameDayExitWithoutHourlyBarsFailsIntrabarAndResolution(t *testing.T) {
	fs := Assess(input(market(t, false), []models.Trade{sameDayProfit()}))
	if f := byCode(fs, "INTRABAR"); f.Level != Fail {
		t.Errorf("INTRABAR = %v %q", f.Level, f.Summary)
	}
	if f := byCode(fs, "RESOLUTION"); f.Level != Fail {
		t.Errorf("RESOLUTION = %v %q", f.Level, f.Summary)
	}
}

func TestSameDayExitWithAFullHourlySessionPasses(t *testing.T) {
	fs := Assess(input(market(t, true), []models.Trade{sameDayProfit()}))
	if f := byCode(fs, "INTRABAR"); f.Level != Pass {
		t.Errorf("INTRABAR = %v %q", f.Level, f.Summary)
	}
	if f := byCode(fs, "RESOLUTION"); f.Level != Pass {
		t.Errorf("RESOLUTION = %v %q", f.Level, f.Summary)
	}
}

func TestFillOutsideTheSessionRangeIsLookahead(t *testing.T) {
	tr := sameDayProfit()
	tr.EntryPrice = 80 // the day never traded below 94
	fs := Assess(input(market(t, true), []models.Trade{tr}))
	if f := byCode(fs, "LOOKAHEAD"); f.Level != Fail {
		t.Errorf("LOOKAHEAD = %v %q", f.Level, f.Summary)
	}
}

func TestStopAndTargetInOneDailyBarIsReported(t *testing.T) {
	tr := models.Trade{Symbol: "AAA", OrderType: "limit", EntryDate: "2021-03-01", ExitDate: "2021-03-02",
		EntryPrice: 99, TargetPrice: 103.5, StopLossPrice: 95, ExitPrice: 95, ExitReason: models.ExitReasonStopLoss}
	fs := Assess(input(market(t, true), []models.Trade{tr}))
	if f := byCode(fs, "INTRABAR"); f.Level != Info {
		t.Errorf("INTRABAR = %v %q", f.Level, f.Summary)
	}
}

func TestOverfitFlagsACollapseAgainstInSample(t *testing.T) {
	in := input(market(t, true), []models.Trade{sameDayProfit()})
	in.Phase = "out-of-sample"
	in.Prior = &models.PerformanceReport{CAGR: 0.80, WinRate: 0.70, ProfitFactor: 1.7}
	in.Report.CAGR, in.Report.WinRate, in.Report.ProfitFactor = 0.05, 0.55, 1.05
	if f := byCode(Assess(in), "OVERFIT"); f.Level != Fail {
		t.Errorf("OVERFIT = %v %q", f.Level, f.Summary)
	}
}

func TestNoHoldoutIsAWarning(t *testing.T) {
	in := input(market(t, true), []models.Trade{sameDayProfit()})
	in.Phase, in.HoldoutMonths = "", 0
	if f := byCode(Assess(in), "OVERFIT"); f.Level < Warn {
		t.Errorf("OVERFIT = %v %q", f.Level, f.Summary)
	}
}
