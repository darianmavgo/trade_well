package scan_market

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func traceRows() []runner.SignalScanRow {
	facts := json.RawMessage(`{"model_symbol":"MULL","state":"bull","prob_bull":0.9184,"prob_bear":0.0102,"signal":0.9082,"rule":"state bull and signal > 0.10","trained_through":"2025-10-09","scored_live":1}`)
	return []runner.SignalScanRow{{
		StrategyID: "markov_model_mull",
		Trace: []runner.TraceRow{
			{Symbol: "MULL", Date: "2026-10-09", Entry: true, Reason: "ENTER: state bull, signal 0.91 (signal > 0.10)", Facts: facts},
			{Symbol: "MULL", Date: "2026-10-12", Entry: false, Reason: "no entry: state is sideways, the strategy needs bull", Facts: facts},
		},
	}}
}

func TestTraceIsSavedAndReadBack(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	now := time.Date(2026, 10, 12, 22, 0, 0, 0, time.UTC)

	n, err := SaveTrace(db, "20261012T220000Z", "2026-10-12", traceRows(), now)
	if err != nil || n != 2 {
		t.Fatalf("saved %d rows, err %v", n, err)
	}

	nightly, err := RecentTrace(db, now, 7, []string{"markov_model_mull"})
	if err != nil {
		t.Fatal(err)
	}
	if len(nightly) != 1 || nightly[0].Date != "2026-10-12" || nightly[0].Entry || !nightly[0].AsOf {
		t.Fatalf("nightly verdicts %+v, want only the as-of session", nightly)
	}
	if !strings.Contains(nightly[0].FactsText, "prob_bull=0.9184") || !strings.Contains(nightly[0].FactsText, "trained_through=2025-10-09") || strings.Contains(nightly[0].FactsText, "model_symbol") {
		t.Errorf("facts text %q", nightly[0].FactsText)
	}

	latest, err := LatestTrace(db, nil)
	if err != nil || len(latest) != 2 || latest[0].Date != "2026-10-12" {
		t.Fatalf("latest scan %+v err %v, want both sessions newest first", latest, err)
	}

	// The scan page reads it with the rest of the results, and a filter hides other strategies.
	res, err := RecentFor(db, now, 7, []string{"markov_model_mull"})
	if err != nil || len(res.Trace) != 1 || len(res.Latest) != 2 {
		t.Fatalf("results trace %d latest %d err %v", len(res.Trace), len(res.Latest), err)
	}
	if other, _ := RecentFor(db, now, 7, []string{"someone-else"}); len(other.Trace) != 0 || len(other.Latest) != 0 {
		t.Errorf("another strategy's filter still saw the trace: %+v", other)
	}
}

// Predictions scored after a model's cutoff are copied out of the model database, and only those.
func TestLivePredictionsAreCopiedOutOfTheModelDatabase(t *testing.T) {
	model := filepath.Join(t.TempDir(), "markov_models.db")
	m, err := storage.OpenSQLite(model)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.RunStage(m, "markov_model_schema", nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO markov_model_meta (symbol, bars, first_date, last_date, trained_at, lookback_days, state_threshold, trained_through)
		 VALUES ('MULL', 10, '2026-09-01', '2026-10-12', 't', 20, 0.05, '2026-10-09'), ('AMD', 10, '2026-09-01', '2026-10-12', 't', 20, 0.05, '2026-10-09')`,
		`INSERT INTO markov_prediction VALUES ('MULL','2026-10-08',1,0.9,0.01,0.89), ('MULL','2026-10-09',1,0.9,0.01,0.89),
		 ('MULL','2026-10-12',1,0.92,0.01,0.91), ('AMD','2026-10-12',0,0.5,0.2,0.3)`,
	} {
		if _, err := m.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	m.Close()

	db := setupTestDB(t)
	defer db.Close()
	now := time.Date(2026, 10, 12, 22, 0, 0, 0, time.UTC)
	n, err := CopyMarkovModelAssetPredictionsToTurso(db, model, "r1", []string{"MULL"}, now)
	if err != nil || n != 1 {
		t.Fatalf("saved %d, err %v, want only MULL's 2026-10-12 (after its 2026-10-09 cutoff)", n, err)
	}
	var sym, date string
	var sig float64
	if err := db.QueryRow(`SELECT symbol, date, signal FROM markov_prediction_live`).Scan(&sym, &date, &sig); err != nil || sym != "MULL" || date != "2026-10-12" || sig != 0.91 {
		t.Fatalf("row %s %s %v err %v", sym, date, sig, err)
	}
	// Saving again replaces, it does not duplicate.
	if n, _ := CopyMarkovModelAssetPredictionsToTurso(db, model, "r2", []string{"MULL"}, now); n != 1 {
		t.Fatalf("second save wrote %d", n)
	}
	var count int
	db.QueryRow(`SELECT count(*) FROM markov_prediction_live`).Scan(&count)
	if count != 1 {
		t.Errorf("%d rows after saving twice", count)
	}
	// A missing model database writes nothing and is not an error.
	if n, err := CopyMarkovModelAssetPredictionsToTurso(db, filepath.Join(t.TempDir(), "none.db"), "r3", []string{"MULL"}, now); err != nil || n != 0 {
		t.Errorf("missing model database: %d, %v", n, err)
	}
}
