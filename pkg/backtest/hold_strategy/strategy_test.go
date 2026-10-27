package hold_strategy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/hoursim"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"

	"github.com/darianmavgo/trade_well/pkg/backtest/realbars"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func signalDates(t *testing.T, row refdb.HoldStrategy) ([]string, []string, []float64) {
	t.Helper()
	market := realbars.Copy(t, "GOOGL")
	db, err := storage.OpenSQLite(market)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bars, _, err := storage.FetchBars(db, "backtest_start", []string{"GOOGL"}, "2021-01-01", "")
	if err != nil {
		t.Fatal(err)
	}
	s := &Strategy{Row: row}
	s.SetDatabases(market, filepath.Join(t.TempDir(), "calc.db"))
	var got, dates []string
	var closes []float64
	for _, b := range bars["GOOGL"] {
		dates = append(dates, b.Date[:10])
		closes = append(closes, b.Close)
	}
	for _, sig := range s.GenerateSignals(bars) {
		got = append(got, sig.Date)
	}
	return got, dates, closes
}

// A row that never bails and never re-enters is buy and hold: one entry, on the first bar.
func TestHoldNeverBailIsOneEntryOnFirstBar(t *testing.T) {
	got, dates, _ := signalDates(t, refdb.HoldStrategy{ID: "t", Name: "t", Symbol: "GOOGL", AllocationPct: 1})
	if len(got) != 1 || got[0] != dates[0] {
		t.Fatalf("got %v, want one entry on %s", got, dates[0])
	}
}

// With a re-entry average the entries are the first bar plus every bar after the
// average is full whose close is above it, checked here against a literal loop.
func TestHoldReentryEntriesMatchSMA(t *testing.T) {
	const period = 20
	got, dates, closes := signalDates(t, refdb.HoldStrategy{ID: "t", Name: "t", Symbol: "GOOGL", AllocationPct: 1, TrailingStopPct: 0.07, SMAReentryPeriod: period})
	want := []string{dates[0]}
	for i := period; i < len(closes); i++ {
		sum := 0.0
		for _, c := range closes[i-period+1 : i+1] {
			sum += c
		}
		if closes[i] > sum/period && dates[i] != dates[0] {
			want = append(want, dates[i])
		}
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d: got %s, want %s", i, got[i], want[i])
		}
	}
}

func TestHoldConfigOnlyTrailsWhenSet(t *testing.T) {
	plain := (&Strategy{Row: refdb.HoldStrategy{ID: "p", Symbol: "VOO", AllocationPct: 1}}).DefaultConfig()
	bail := (&Strategy{Row: refdb.HoldStrategy{ID: "b", Symbol: "VOO", AllocationPct: 1, TrailingStopPct: 0.07, SMAReentryPeriod: 20}}).DefaultConfig()
	if plain.UseTrailingStop || plain.TrailingStopPct != 0 {
		t.Errorf("plain hold trails: %+v", plain)
	}
	if !bail.UseTrailingStop || bail.TrailingStopPct != 0.07 {
		t.Errorf("bail hold does not trail: %+v", bail)
	}
}

// regimeSignals writes a Markov model whose bear bars are the given indexes of
// GOOGL's bars from 2021 on, then returns the row's signals, the bars' dates and
// the simulated trades.
func regimeSignals(t *testing.T, bearIdx map[int]bool) ([]models.Signal, []string, []models.Trade) {
	t.Helper()
	app := t.TempDir()
	t.Setenv("APP_FOLDER", app)
	market := realbars.Copy(t, "GOOGL")
	mdb, err := storage.OpenSQLite(market)
	if err != nil {
		t.Fatal(err)
	}
	defer mdb.Close()
	bars, dates, err := storage.FetchBars(mdb, "backtest_start", []string{"GOOGL"}, "2021-01-01", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(app, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	model, err := storage.OpenSQLite(appenv.MarkovDB())
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()
	model.MustExec(`CREATE TABLE markov_prediction (symbol TEXT NOT NULL, date TEXT NOT NULL, state INTEGER NOT NULL,
		prob_bull REAL NOT NULL, prob_bear REAL NOT NULL, signal REAL NOT NULL, PRIMARY KEY (symbol, date)) WITHOUT ROWID`)
	model.MustExec(`CREATE TABLE markov_model_meta (symbol TEXT PRIMARY KEY, bars INTEGER NOT NULL, first_date TEXT NOT NULL,
		last_date TEXT NOT NULL, trained_at TEXT NOT NULL, lookback_days INTEGER NOT NULL, state_threshold REAL NOT NULL)`)
	for i, d := range dates {
		state := 1
		if bearIdx[i] {
			state = -1
		} else if i%7 == 0 {
			state = 0
		}
		model.MustExec(`INSERT INTO markov_prediction VALUES ('GOOGL', ?, ?, 0.3, 0.3, 0)`, d, state)
	}
	model.MustExec(`INSERT INTO markov_model_meta VALUES ('GOOGL', ?, ?, ?, 'now', 20, 0.05)`, len(dates), dates[0], dates[len(dates)-1])

	s := &Strategy{Row: refdb.HoldStrategy{ID: "t", Name: "t", Symbol: "GOOGL", AllocationPct: 1, RegimeSymbol: "googl"}}
	s.SetDatabases(market, filepath.Join(t.TempDir(), "calc.db"))
	sigs := s.GenerateSignals(bars)
	out, err := hoursim.Run(hoursim.Input{Signals: sigs, Daily: bars, Dates: dates, Config: s.DefaultConfig(), Capital: 100000})
	if err != nil {
		t.Fatal(err)
	}
	return sigs, dates, out.Trades
}

// Long unless the state is bear: one entry per run of non-bear bars, and the sale
// is the close of the first bear bar after the run.
func TestHoldRegimeSellsOnBearAndBuysBackWhenNot(t *testing.T) {
	bear := map[int]bool{10: true, 11: true, 12: true, 30: true, 31: true}
	sigs, dates, trades := regimeSignals(t, bear)
	last := len(dates) - 1
	wantEntry := []int{0, 13, 32}
	if len(sigs) != len(wantEntry) {
		t.Fatalf("got %d entries, want %d: %+v", len(sigs), len(wantEntry), sigs)
	}
	for i, idx := range wantEntry {
		if sigs[i].Date != dates[idx] {
			t.Errorf("entry %d on %s, want %s", i, sigs[i].Date, dates[idx])
		}
	}
	if len(trades) < 2 {
		t.Fatalf("got %d trades, want at least 2 completed", len(trades))
	}
	wantExit := []string{dates[10], dates[30]}
	for i, d := range wantExit {
		if trades[i].EntryDate[:10] != dates[wantEntry[i]] || trades[i].ExitDate[:10] != d {
			t.Errorf("trade %d: %s -> %s, want %s -> %s", i, trades[i].EntryDate, trades[i].ExitDate, dates[wantEntry[i]], d)
		}
	}
	if sigs[2].HoldDaysOverride != 0 {
		t.Errorf("the run to the last bar (%d) must not be sold, got hold override %d", last, sigs[2].HoldDaysOverride)
	}
}

// A symbol with no trained model gives no signals rather than a silent buy and hold.
func TestHoldRegimeWithoutModelGivesNoSignals(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	s := &Strategy{Row: refdb.HoldStrategy{ID: "t", Name: "t", Symbol: "GOOGL", AllocationPct: 1, RegimeSymbol: "GOOGL"}}
	s.SetDatabases(realbars.Copy(t, "GOOGL"), filepath.Join(t.TempDir(), "calc.db"))
	if got := s.GenerateSignals(nil); len(got) != 0 {
		t.Fatalf("got %d signals without a model", len(got))
	}
}

// A hold row has the same three exits as every family: 0 take-profit, 0 stop and
// a hold of 0 or 99999 leave the hold as it was, and a set value reaches the config.
func TestHoldExitsReachTheConfigAndZeroLeavesThemOff(t *testing.T) {
	for _, hold := range []int{0, 99999} {
		cfg := (&Strategy{Row: refdb.HoldStrategy{ID: "p", Symbol: "VOO", AllocationPct: 1, HoldDays: hold}}).DefaultConfig()
		if cfg.TargetPct != 999 || cfg.TakeProfitPct != 0 || cfg.StopLossPct != 0.0001 || cfg.HoldingWindow != 99999 {
			t.Errorf("hold_days %d with no exits changed the config: %+v", hold, cfg)
		}
	}
	row := refdb.HoldStrategy{ID: "x", Symbol: "VOO", AllocationPct: 1, TakeProfitPct: 0.08, StopLossPct: 0.05, HoldDays: 10}
	s := &Strategy{Row: row}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg := s.DefaultConfig()
	if cfg.TakeProfitPct != 0.08 || cfg.TargetPct != 1.08 || cfg.StopLossPct != 0.95 || cfg.HoldingWindow != 10 {
		t.Errorf("exits not applied: %+v", cfg)
	}
	ps := s.ParameterSpace()
	if ps.Baseline.TakeProfit != 0.08 || ps.Baseline.StopLoss != 0.05 || ps.Baseline.HoldDays != 10 {
		t.Errorf("baseline %+v", ps.Baseline)
	}
	if err := (&Strategy{Row: refdb.HoldStrategy{ID: "x", Symbol: "VOO", AllocationPct: 1, StopLossPct: 1.5}}).Validate(); err == nil {
		t.Error("stop_loss_pct 1.5 was accepted")
	}
}
