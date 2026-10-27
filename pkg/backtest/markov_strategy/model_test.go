package markov_strategy

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/realbars"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/train"
)

func TestBacktestReadsTrainedModelAndNeverTrains(t *testing.T) {
	app := t.TempDir()
	t.Setenv("APP_FOLDER", app)
	market := realbars.Copy(t, "GOOGL")

	row := refdb.MarkovStrategy{
		ID: "markov_model_googl", Name: "GOOGL markov", SignalSymbol: "GOOGL", TradeSymbol: "GOOGL",
		Direction: "long", TargetState: "bull", HoldDays: 5, TakeProfitPct: 0.05, StopLossPct: 0.05, AllocationPct: 0.5,
	}
	run := func() []string {
		s := &Strategy{Row: row}
		s.SetDatabases(market, filepath.Join(t.TempDir(), "calc.db"))
		var dates []string
		for _, sig := range s.GenerateSignals(nil) {
			dates = append(dates, sig.Date)
		}
		return dates
	}

	// No trained model: no signals, and the model database is not created by the backtest.
	if got := run(); len(got) != 0 {
		t.Fatalf("untrained symbol produced signals %v", got)
	}

	if _, err := os.Stat(filepath.Join(app, "data", "markov_models.db")); err == nil {
		t.Fatal("the backtest created the model database")
	}

	// Train as its own step, into the path backtest reads.
	if _, err := train.TrainMarkov(context.Background(), train.MarkovConfig{MarketDB: market, ModelDB: filepath.Join(app, "data", "markov_models.db"), Symbols: []string{"GOOGL"}}); err != nil {
		t.Fatal(err)
	}
	db, err := storage.OpenSQLiteReadOnly(filepath.Join(app, "data", "markov_models.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var want []string
	if err := db.Select(&want, "SELECT date FROM markov_prediction WHERE symbol = 'GOOGL' AND state = 1 AND signal > 0.10 ORDER BY date"); err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("the trained model has no bull entries; the series does not exercise the strategy")
	}

	got := run()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backtest dates %v\nwant (from the persisted model) %v", got, want)
	}

	// Running the backtest leaves the model as it was.
	var n int
	if err := db.Get(&n, "SELECT COUNT(*) FROM markov_prediction"); err != nil || n != len(realbars.Closes(t, market, "GOOGL"))-20 {
		t.Fatalf("model rows after backtest: %d (err %v)", n, err)
	}
}

// A model trained on older data than the market database says so, and still runs.
func TestStaleModelWarns(t *testing.T) {
	app := t.TempDir()
	t.Setenv("APP_FOLDER", app)
	older := realbars.Copy(t, "GOOGL")
	db, err := storage.OpenSQLite(older)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM backtest_start WHERE substr(Date, 1, 10) > '2025-06-30'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := train.TrainMarkov(context.Background(), train.MarkovConfig{MarketDB: older, ModelDB: filepath.Join(app, "data", "markov_models.db"), Symbols: []string{"GOOGL"}}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	current := realbars.Copy(t, "GOOGL")
	s := &Strategy{Row: refdb.MarkovStrategy{
		ID: "markov_model_googl", Name: "GOOGL markov", SignalSymbol: "GOOGL", TradeSymbol: "GOOGL",
		Direction: "long", TargetState: "bull", HoldDays: 5, TakeProfitPct: 0.05, StopLossPct: 0.05, AllocationPct: 0.5,
	}}
	s.SetDatabases(current, filepath.Join(t.TempDir(), "calc.db"))
	if sigs := s.GenerateSignals(nil); len(sigs) == 0 {
		t.Fatal("a stale model must still produce signals")
	}
	if got := buf.String(); !strings.Contains(got, "trained through 2025-06-30") || !strings.Contains(got, "`train markov -symbols GOOGL`") {
		t.Fatalf("no staleness warning in log: %q", got)
	}
}

// A live scan prepares each markov strategy: it extends the saved model through the latest bar
// when it is behind, does nothing when it is current, and fails when there is no model.
func TestPrepareExtendsOnlyWhenBehindAndNeverTrains(t *testing.T) {
	app := t.TempDir()
	t.Setenv("APP_FOLDER", app)
	market := realbars.Copy(t, "GOOGL")
	s := &Strategy{Row: refdb.MarkovStrategy{
		ID: "markov_model_googl", Name: "x", SignalSymbol: "GOOGL", TradeSymbol: "GOOGL",
		Direction: "long", TargetState: "bull", HoldDays: 5, AllocationPct: 0.25,
	}}
	modelDB := filepath.Join(app, "data", "markov_models.db")
	last, err := storage.SymbolLastDate(market, "GOOGL")
	if err != nil || last == "" {
		t.Fatalf("last bar %q %v", last, err)
	}

	// No model: Prepare says how to make one instead of fitting one from the bars it happens to hold.
	if err := s.Prepare(context.Background(), market); err == nil || !strings.Contains(err.Error(), "train markov -symbols GOOGL") {
		t.Fatalf("a symbol with no model must fail Prepare naming the train command, got %v", err)
	}
	if _, err := os.Stat(modelDB); err == nil {
		t.Fatal("Prepare created a model file")
	}
	if _, err := train.TrainMarkov(context.Background(), train.MarkovConfig{MarketDB: market, ModelDB: modelDB, Symbols: []string{"GOOGL"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Prepare(context.Background(), market); err != nil {
		t.Fatalf("Prepare with a current model: %v", err)
	}
	trained, ok := ModelLastDate(modelDB, "GOOGL")
	if !ok || trained != last {
		t.Fatalf("model trained through %q (found %v), want %s", trained, ok, last)
	}
	before, err := os.Stat(modelDB)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Prepare(context.Background(), market); err != nil {
		t.Fatalf("second Prepare: %v", err)
	}
	after, _ := os.Stat(modelDB)
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal("a current model was trained again")
	}

	// The model now has a prediction for the latest bar, so the strategy no longer logs a missing model.
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	s.SetDatabases(market, filepath.Join(t.TempDir(), "calc.db"))
	s.GenerateSignals(nil)
	if strings.Contains(logs.String(), "no trained model") || strings.Contains(logs.String(), "was trained through") {
		t.Fatalf("model still reported missing or behind: %s", logs.String())
	}

	if err := (&Strategy{Row: refdb.MarkovStrategy{SignalSymbol: "NOSUCH"}}).Prepare(context.Background(), market); err == nil {
		t.Fatal("a symbol with no bars must fail Prepare, not pass as a quiet day")
	}
}

// Every entry carries the row's side. A signal left with the SQL placeholder as its direction is
// read by the live scan as neither LONG nor BUY, so the scan reports NO_SIGNAL on a real entry.
// The pipeline also leaves its reasoning in signal_trace, and that agrees with the entries.
func TestSignalsCarryDirectionAndTrace(t *testing.T) {
	app := t.TempDir()
	t.Setenv("APP_FOLDER", app)
	market := realbars.Copy(t, "GOOGL")
	if _, err := train.TrainMarkov(context.Background(), train.MarkovConfig{MarketDB: market, ModelDB: filepath.Join(app, "data", "markov_models.db"), Symbols: []string{"GOOGL"}}); err != nil {
		t.Fatal(err)
	}
	row := refdb.MarkovStrategy{
		ID: "markov_model_googl", Name: "GOOGL markov", SignalSymbol: "GOOGL", TradeSymbol: "GOOGL",
		Direction: "long", TargetState: "bull", HoldDays: 5, TakeProfitPct: 0.05, StopLossPct: 0.05, AllocationPct: 0.5,
	}
	calc := filepath.Join(t.TempDir(), "calc.db")
	s := &Strategy{Row: row}
	s.SetDatabases(market, calc)
	sigs := s.GenerateSignals(nil)
	if len(sigs) == 0 {
		t.Fatal("no entries; the series does not exercise the strategy")
	}
	entered := map[string]bool{}
	for _, sig := range sigs {
		if sig.Direction != "LONG" {
			t.Fatalf("signal on %s has direction %q, want LONG", sig.Date, sig.Direction)
		}
		entered[sig.Date] = true
	}

	db, err := storage.OpenSQLiteReadOnly(calc)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var trace []struct {
		Date   string `db:"date"`
		Entry  int    `db:"entry"`
		Reason string `db:"reason"`
	}
	if err := db.Select(&trace, "SELECT date, entry, reason FROM signal_trace ORDER BY date"); err != nil {
		t.Fatal(err)
	}
	if len(trace) != 5 {
		t.Fatalf("trace has %d sessions, want the latest 5", len(trace))
	}
	for _, tr := range trace {
		if (tr.Entry == 1) != entered[tr.Date] {
			t.Errorf("trace for %s says entry=%d but the signals say %v (%s)", tr.Date, tr.Entry, entered[tr.Date], tr.Reason)
		}
		if tr.Reason == "" {
			t.Errorf("trace for %s has no reason", tr.Date)
		}
	}
}
