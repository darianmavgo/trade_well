package train

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/realbars"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

type predRow struct {
	Date     string  `db:"date"`
	State    int     `db:"state"`
	ProbBull float64 `db:"prob_bull"`
	ProbBear float64 `db:"prob_bear"`
	Signal   float64 `db:"signal"`
}

func predictions(t *testing.T, modelPath string) []predRow {
	t.Helper()
	db, err := storage.OpenSQLiteReadOnly(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rows []predRow
	if err := db.Select(&rows, "SELECT date, state, prob_bull, prob_bear, signal FROM markov_prediction WHERE symbol = 'VOO' ORDER BY date"); err != nil {
		t.Fatal(err)
	}
	return rows
}

// A model trained with a cutoff on part of the history and then extended over the rest must
// hold the same predictions as one trained with that cutoff on all of it: the sessions after
// the cutoff are scored with frozen counts either way, with no refit.
func TestExtendMarkovMatchesAFullTrainWithTheSameCutoff(t *testing.T) {
	full := realbars.Copy(t, "VOO")
	dates := realbars.Dates(t, full, "VOO")
	cut := dates[len(dates)/2]
	split := dates[len(dates)*3/4]

	partial := realbars.Copy(t, "VOO")
	db, err := storage.OpenSQLite(partial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM backtest_start WHERE symbol = 'VOO' AND Date > ?", split); err != nil {
		t.Fatal(err)
	}
	db.Close()

	ctx := context.Background()
	extended := filepath.Join(t.TempDir(), "extended.db")
	if _, err := TrainMarkov(ctx, MarkovConfig{MarketDB: partial, ModelDB: extended, Symbols: []string{"VOO"}, Through: cut}); err != nil {
		t.Fatal(err)
	}
	before := predictions(t, extended)
	got, err := ExtendMarkov(ctx, ExtendConfig{MarketDB: full, ModelDB: extended, Symbols: []string{"voo"}})
	if err != nil || len(got) != 1 || got[0] != "VOO" {
		t.Fatalf("extended %v, err %v; want [VOO]", got, err)
	}

	whole := filepath.Join(t.TempDir(), "whole.db")
	if _, err := TrainMarkov(ctx, MarkovConfig{MarketDB: full, ModelDB: whole, Symbols: []string{"VOO"}, Through: cut}); err != nil {
		t.Fatal(err)
	}
	a, b := predictions(t, extended), predictions(t, whole)
	if len(a) != len(b) || len(a) <= len(before) {
		t.Fatalf("extended model has %d rows (was %d), full train has %d", len(a), len(before), len(b))
	}
	for i := range a {
		if a[i].Date != b[i].Date || a[i].State != b[i].State ||
			math.Abs(a[i].ProbBull-b[i].ProbBull) > 1e-12 || math.Abs(a[i].ProbBear-b[i].ProbBear) > 1e-12 ||
			math.Abs(a[i].Signal-b[i].Signal) > 1e-12 {
			t.Fatalf("row %d: extended %+v, full train %+v", i, a[i], b[i])
		}
	}

	// The meta moved forward but still says the model learned only through the cutoff.
	mdb, err := storage.OpenSQLiteReadOnly(extended)
	if err != nil {
		t.Fatal(err)
	}
	defer mdb.Close()
	var meta struct {
		Last, Through string
		Bars          int
	}
	if err := mdb.QueryRow("SELECT last_date, trained_through, bars FROM markov_model_meta WHERE symbol = 'VOO'").Scan(&meta.Last, &meta.Through, &meta.Bars); err != nil {
		t.Fatal(err)
	}
	if meta.Last != dates[len(dates)-1] || meta.Through != cut || meta.Bars != len(b) {
		t.Fatalf("meta %+v, want last %s, trained_through %s, bars %d", meta, dates[len(dates)-1], cut, len(b))
	}

	// Nothing more to add the second time.
	if again, err := ExtendMarkov(ctx, ExtendConfig{MarketDB: full, ModelDB: extended, Symbols: []string{"VOO"}}); err != nil || len(again) != 0 {
		t.Fatalf("second extend %v, err %v; want nothing", again, err)
	}
}

// Through keeps a session that may still be open out of the model.
func TestExtendMarkovStopsAtThrough(t *testing.T) {
	full := realbars.Copy(t, "VOO")
	dates := realbars.Dates(t, full, "VOO")
	cut := dates[len(dates)/2]
	split := dates[len(dates)*3/4]
	stop := dates[len(dates)*3/4+10]

	partial := realbars.Copy(t, "VOO")
	db, err := storage.OpenSQLite(partial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM backtest_start WHERE symbol = 'VOO' AND Date > ?", split); err != nil {
		t.Fatal(err)
	}
	db.Close()

	ctx := context.Background()
	model := filepath.Join(t.TempDir(), "m.db")
	if _, err := TrainMarkov(ctx, MarkovConfig{MarketDB: partial, ModelDB: model, Symbols: []string{"VOO"}, Through: cut}); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtendMarkov(ctx, ExtendConfig{MarketDB: full, ModelDB: model, Symbols: []string{"VOO"}, Through: stop}); err != nil {
		t.Fatal(err)
	}
	rows := predictions(t, model)
	if last := rows[len(rows)-1].Date; last != stop {
		t.Fatalf("last prediction %s, want %s", last, stop)
	}
}

func TestExtendMarkovRefusesAMissingModel(t *testing.T) {
	full := realbars.Copy(t, "VOO")
	if _, err := ExtendMarkov(context.Background(), ExtendConfig{MarketDB: full, ModelDB: filepath.Join(t.TempDir(), "none.db"), Symbols: []string{"VOO"}}); err == nil {
		t.Fatal("want an error for a model database that does not exist")
	}
}
