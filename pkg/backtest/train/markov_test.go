package train

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/realbars"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

type want struct {
	state              int
	probBull, probBear float64
}

// expected walks the series the plain way: state from the 20-bar return,
// transitions to the next state, counts over earlier bars only.
func expected(closes []float64) map[int]want {
	var idx, states []int
	for i := 20; i < len(closes); i++ {
		r := (closes[i] - closes[i-20]) / closes[i-20]
		s := 0
		if r >= 0.05 {
			s = 1
		} else if r <= -0.05 {
			s = -1
		}
		idx = append(idx, i)
		states = append(states, s)
	}
	out := map[int]want{}
	for j := range idx {
		var bull, bear, total int
		for k := 0; k < j; k++ { // earlier bars with a known next state
			if states[k] != states[j] {
				continue
			}
			total++
			switch states[k+1] {
			case 1:
				bull++
			case -1:
				bear++
			}
		}
		w := want{state: states[j]}
		if total > 0 {
			w.probBull = float64(bull) / float64(total)
			w.probBear = float64(bear) / float64(total)
		}
		out[idx[j]] = w
	}
	return out
}

func TestTrainPersistsWalkForwardModel(t *testing.T) {
	// Real daily bars: GOOGL (years of history) and a recent listing too short to train.
	short := realbars.ShortSymbol(t, 5, 20)
	market := realbars.Copy(t, "GOOGL", short)
	closes := realbars.Closes(t, market, "GOOGL")
	dates := realbars.Dates(t, market, "GOOGL")
	modelPath := filepath.Join(t.TempDir(), "markov.db")
	exp := expected(closes)

	res, err := TrainMarkov(context.Background(), MarkovConfig{MarketDB: market, ModelDB: modelPath, Symbols: []string{"googl", short, "GOOGL"}, Batch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Requested != 2 || res.Trained != 1 || res.Skipped != 1 || res.Rows != len(exp) {
		t.Fatalf("result %+v, want 2 requested, 1 trained, 1 skipped, %d rows", res, len(exp))
	}

	db, err := storage.OpenSQLiteReadOnly(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rows []struct {
		Date     string  `db:"date"`
		State    int     `db:"state"`
		ProbBull float64 `db:"prob_bull"`
		ProbBear float64 `db:"prob_bear"`
		Signal   float64 `db:"signal"`
	}
	if err := db.Select(&rows, "SELECT date, state, prob_bull, prob_bear, signal FROM markov_prediction WHERE symbol = 'GOOGL' ORDER BY date"); err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(exp) {
		t.Fatalf("%d rows, want %d", len(rows), len(exp))
	}
	states := map[int]bool{}
	for k, r := range rows {
		w := exp[20+k]
		if r.Date != dates[20+k] || r.State != w.state ||
			math.Abs(r.ProbBull-w.probBull) > 1e-9 || math.Abs(r.ProbBear-w.probBear) > 1e-9 ||
			math.Abs(r.Signal-(w.probBull-w.probBear)) > 1e-9 {
			t.Fatalf("row %d: got %+v want %+v", k, r, w)
		}
		states[r.State] = true
	}
	if len(states) != 3 {
		t.Fatalf("the series only exercised states %v", states)
	}

	// The model was trained on the real history: it ends on the last real bar.
	if res.MarketThrough != dates[len(dates)-1] {
		t.Fatalf("trained through %s, the real GOOGL history ends %s", res.MarketThrough, dates[len(dates)-1])
	}
	var lastDate string
	if err := db.Get(&lastDate, "SELECT last_date FROM markov_model_meta WHERE symbol = 'GOOGL'"); err != nil || lastDate != dates[len(dates)-1] {
		t.Fatalf("model last_date %q (err %v), want %s", lastDate, err, dates[len(dates)-1])
	}

	var meta int
	if err := db.Get(&meta, fmt.Sprintf("SELECT COUNT(*) FROM markov_model_meta WHERE symbol = 'GOOGL' AND bars = %d", len(exp))); err != nil || meta != 1 {
		t.Fatalf("meta rows %d err %v", meta, err)
	}
	if err := db.Get(&meta, "SELECT COUNT(*) FROM markov_model_meta WHERE symbol = ?", short); err != nil || meta != 0 {
		t.Fatalf("a symbol with too little history must have no model, got %d (err %v)", meta, err)
	}

	// Retraining replaces rather than duplicates.
	if _, err := TrainMarkov(context.Background(), MarkovConfig{MarketDB: market, ModelDB: modelPath, Symbols: []string{"GOOGL"}}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Get(&n, "SELECT COUNT(*) FROM markov_prediction WHERE symbol = 'GOOGL'"); err != nil || n != len(exp) {
		t.Fatalf("after retrain %d rows, want %d (err %v)", n, len(exp), err)
	}
}

// expectedThrough is expected with the cutoff rule: a transition counts only when its
// destination bar is on or before cut, so later bars are scored with frozen counts.
func expectedThrough(closes []float64, dates []string, cut string) map[int]want {
	var idx, states []int
	for i := 20; i < len(closes); i++ {
		r := (closes[i] - closes[i-20]) / closes[i-20]
		s := 0
		if r >= 0.05 {
			s = 1
		} else if r <= -0.05 {
			s = -1
		}
		idx = append(idx, i)
		states = append(states, s)
	}
	out := map[int]want{}
	for j := range idx {
		var bull, bear, total int
		for k := 0; k < j; k++ {
			if states[k] != states[j] || dates[idx[k]+1] > cut {
				continue
			}
			total++
			switch states[k+1] {
			case 1:
				bull++
			case -1:
				bear++
			}
		}
		w := want{state: states[j]}
		if total > 0 {
			w.probBull = float64(bull) / float64(total)
			w.probBear = float64(bear) / float64(total)
		}
		out[idx[j]] = w
	}
	return out
}

func TestTrainThroughNeverLearnsFromLaterSessions(t *testing.T) {
	market := realbars.Copy(t, "GOOGL")
	closes := realbars.Closes(t, market, "GOOGL")
	dates := realbars.Dates(t, market, "GOOGL")
	cut := dates[len(dates)/2]
	modelPath := filepath.Join(t.TempDir(), "markov.db")
	exp := expectedThrough(closes, dates, cut)

	if _, err := TrainMarkov(context.Background(), MarkovConfig{MarketDB: market, ModelDB: modelPath, Symbols: []string{"GOOGL"}, Through: cut}); err != nil {
		t.Fatal(err)
	}
	db, err := storage.OpenSQLiteReadOnly(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rows []struct {
		Date     string  `db:"date"`
		State    int     `db:"state"`
		ProbBull float64 `db:"prob_bull"`
		ProbBear float64 `db:"prob_bear"`
	}
	if err := db.Select(&rows, "SELECT date, state, prob_bull, prob_bear FROM markov_prediction WHERE symbol = 'GOOGL' ORDER BY date"); err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(exp) {
		t.Fatalf("%d prediction rows, want %d (sessions after the cutoff are still scored)", len(rows), len(exp))
	}
	for j, r := range rows {
		w := exp[j+20]
		if r.State != w.state || math.Abs(r.ProbBull-w.probBull) > 1e-9 || math.Abs(r.ProbBear-w.probBear) > 1e-9 {
			t.Fatalf("%s: got state %d bull %.6f bear %.6f, want state %d bull %.6f bear %.6f", r.Date, r.State, r.ProbBull, r.ProbBear, w.state, w.probBull, w.probBear)
		}
	}
	var through string
	if err := db.Get(&through, "SELECT trained_through FROM markov_model_meta WHERE symbol = 'GOOGL'"); err != nil || through != cut {
		t.Fatalf("trained_through = %q, %v; want %q", through, err, cut)
	}
}
