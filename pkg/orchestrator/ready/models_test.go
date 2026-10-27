package ready

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/markov_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	_ "modernc.org/sqlite"
)

// marketWith writes a real market DB holding one daily AMUU bar on date.
func marketWith(t *testing.T, date string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "market.db")
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.UpsertBars(db, "backtest_start", []models.Bar{{Symbol: "AMUU", Date: date, Timeframe: "1d", Open: 1, High: 1, Low: 1, Close: 1}}); err != nil {
		t.Fatal(err)
	}
	return path
}

// An App Engine instance starts from the bundled models, which trail the last session until the
// evening scan scores the sessions in between. That is only a warning while the market data can
// supply those sessions, and bad when it cannot.
func TestModelsCheckWarnsWhenTheScanCanCatchUpAndIsBadWhenItCannot(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	members, _ := allowlistCheck([]string{"markov_model_amuu"}) // places the bundled models
	if len(members) != 1 {
		t.Fatalf("members = %d", len(members))
	}
	last, ok := markov_strategy.ModelLastDate(appenv.MarkovDB(), "AMUU")
	if !ok {
		t.Fatal("the bundle has no AMUU model")
	}
	lastDay, _ := time.Parse("2006-01-02", last)
	completed := lastDay.AddDate(0, 0, 14) // far enough that the model trails by more than two sessions

	ahead := modelsCheck(members, marketWith(t, lastDay.AddDate(0, 0, 14).Format("2006-01-02")), completed)
	if ahead.Status != Warn || !strings.Contains(ahead.Detail, "the next scan catches it up") {
		t.Errorf("market ahead of the model = %s %q, want warn", ahead.Status, ahead.Detail)
	}
	stuck := modelsCheck(members, marketWith(t, last), completed)
	if stuck.Status != Bad {
		t.Errorf("market no newer than the model = %s %q, want bad", stuck.Status, stuck.Detail)
	}
	// No market file at all: the scan downloads the window first, so a model inside that window is a
	// warning; a model further behind than the download reaches needs a deliberate retrain.
	missing := modelsCheck(members, filepath.Join(t.TempDir(), "none.db"), completed)
	if missing.Status != Warn || !strings.Contains(missing.Detail, "downloads it and catches up") {
		t.Errorf("no market data, model within the download window = %s %q, want warn", missing.Status, missing.Detail)
	}
	tooOld := modelsCheck(members, filepath.Join(t.TempDir(), "none.db"), lastDay.AddDate(0, 0, maxCatchUpDays+30))
	if tooOld.Status != Bad {
		t.Errorf("no market data, model older than the download window = %s %q, want bad", tooOld.Status, tooOld.Detail)
	}
}

// A cold App Engine instance has no market file; the bars the scan keeps in market_bars stand in
// for it, so the check reports the real freshness instead of a blanket warning.
func TestMarketCheckReadsMarketBarsWhenThereIsNoLocalFile(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	members, _ := allowlistCheck([]string{"markov_model_amuu"})
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := scan_market.EnsureBarsSchema(db); err != nil {
		t.Fatal(err)
	}
	completed := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO market_bars VALUES ('AMUU','2026-10-23',1,1,1,1,1,1)`); err != nil {
		t.Fatal(err)
	}
	none := filepath.Join(t.TempDir(), "none.db")

	c := marketCheck(members, db, none, completed)
	if c.Status != OK || !strings.Contains(c.Detail, "Turso market_bars") || !strings.Contains(c.Detail, "AMUU: 2026-10-23") {
		t.Errorf("got %s: %s", c.Status, c.Detail)
	}
	if _, err := db.Exec(`DELETE FROM market_bars`); err != nil {
		t.Fatal(err)
	}
	if c := marketCheck(members, db, none, completed); c.Status != Bad {
		t.Errorf("no bars anywhere should be Bad, got %s: %s", c.Status, c.Detail)
	}
	if c := marketCheck(members, nil, none, completed); c.Status != Warn {
		t.Errorf("no file and no database should warn, got %s", c.Status)
	}
}
