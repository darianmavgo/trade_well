package ready

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/train"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/modelstate"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
)

// The check reads the QQQ model compiled into this binary, placed the way a cold instance gets it.
func TestHMMCheckGradesTheBundledModel(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	if err := strategies.EnsureBundle(); err != nil {
		t.Fatal(err)
	}
	l, err := train.ReadHMMLatest(appenv.HMMDB(), "QQQ")
	if err != nil {
		t.Fatalf("the bundle has no QQQ hidden Markov model: %v", err)
	}
	last, err := time.Parse("2006-01-02", l.Date)
	if err != nil {
		t.Fatal(err)
	}

	c := hmmCheck(context.Background(), nil, last)
	if c.Name != "hmm_model" || c.Status != OK || !strings.Contains(c.Detail, "as of "+l.Date) {
		t.Fatalf("at the model's own date: %+v", c)
	}

	c = hmmCheck(context.Background(), nil, last.AddDate(0, 0, 10))
	if c.Status != Bad || !strings.Contains(c.Detail, "Prepare is not scoring") {
		t.Fatalf("ten days behind should be bad: %+v", c)
	}

	c = hmmCheck(context.Background(), nil, last.AddDate(3, 0, 0))
	if !strings.Contains(c.Detail, "retrain") {
		t.Fatalf("three years on should ask for a retrain: %+v", c)
	}
}

func TestHMMCheckIsBadWithoutAModel(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	// Placing the bundle writes the model; remove it to stand for a deploy that lacks one.
	if err := strategies.EnsureBundle(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(appenv.HMMDB()); err != nil {
		t.Fatal(err)
	}
	c := hmmCheck(context.Background(), nil, time.Now())
	if c.Status != Bad || !strings.Contains(c.Detail, "hmm strategy") {
		t.Fatalf("got %+v", c)
	}
}

// Prepare's persisted sessions in Turso count, not the bundle's date: a cold instance starts from
// the bundle, and the check must still see that the evening scan scored the last session.
func TestHMMCheckGradesWhatPrepareStoredInTurso(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	if err := strategies.EnsureBundle(); err != nil {
		t.Fatal(err)
	}
	l, err := train.ReadHMMLatest(appenv.HMMDB(), "QQQ")
	if err != nil {
		t.Fatal(err)
	}
	bundled, _ := time.Parse("2006-01-02", l.Date)
	trainedAt, err := modelstate.ReadHMMModelAssetTrainedAt(appenv.HMMDB(), "QQQ")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "turso.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := modelstate.LatestHMMPredictionInTurso(context.Background(), db, "QQQ", trainedAt); err != sql.ErrNoRows {
		t.Fatalf("empty store: %v", err)
	}

	completed := bundled.AddDate(0, 0, 4)
	c := hmmCheck(context.Background(), db, completed)
	if c.Status != Bad {
		t.Fatalf("nothing stored and the bundle far behind should be bad: %+v", c)
	}

	day := completed.Format("2006-01-02")
	if _, err := db.Exec(`INSERT INTO hmm_prediction_live (symbol, trained_at, date, ret, vol, regime, regime_prob, next_regime, next_regime_prob, loglik_bar, in_sample)
		VALUES ('QQQ', ?, ?, 0, 0, 1, 0.9, 3, 0.8, 0, 0)`, trainedAt, day); err != nil {
		t.Fatal(err)
	}
	c = hmmCheck(context.Background(), db, completed)
	if c.Status != OK || !strings.Contains(c.Detail, "as of "+day+" from Turso") || !strings.Contains(c.Detail, "next session 3 (80%)") {
		t.Fatalf("scored through the last session should be ok: %+v", c)
	}

	c = hmmCheck(context.Background(), db, completed.AddDate(0, 0, 1))
	if c.Status != Warn || !strings.Contains(c.Detail, "one session stale") {
		t.Fatalf("one session behind should warn: %+v", c)
	}
}
