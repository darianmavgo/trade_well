package modelstate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/calendar"
	"github.com/darianmavgo/trade_well/pkg/backtest/dailybars"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	_ "modernc.org/sqlite"
)

// bundled copies the QQQ model compiled into this repo to a fresh file, the way a cold instance
// starts.
func bundled(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../strategies/deploy/hmm_models.db")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "hmm_models.db")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func memory(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func lastDate(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var d string
	if err := db.QueryRow(`SELECT last_date FROM hmm_model_meta WHERE symbol = 'QQQ'`).Scan(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

// extend does what ExtendHMM does for one new session: a prediction row, one state row per
// state, and the model's last_date moved forward.
func extend(t *testing.T, path, date string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO hmm_prediction VALUES ('QQQ', ?, 0.01, 0.2, 2, 0.9, 2, 0.8, -1.5, 0)`, date); err != nil {
		t.Fatal(err)
	}
	for s := 0; s < 3; s++ {
		if _, err := db.Exec(`INSERT INTO hmm_state_prob VALUES ('QQQ', ?, ?, 0.3, 0.3)`, date, s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE hmm_model_meta SET last_date = ? WHERE symbol = 'QQQ'`, date); err != nil {
		t.Fatal(err)
	}
}

func TestPredictionsSurviveAColdStart(t *testing.T) {
	ctx := context.Background()
	turso := memory(t)

	// Instance A extends the bundled model by one session and stores it.
	a := bundled(t)
	base := lastDate(t, a)
	extend(t, a, "2099-01-04")
	if n, err := PersistHMMModelAssetToTurso(ctx, turso, a); err != nil || n < 1 {
		t.Fatalf("persist: %d %v", n, err)
	}
	if n, err := PersistHMMModelAssetToTurso(ctx, turso, a); err != nil || n != 0 {
		t.Fatalf("a second persist must find nothing new: %d %v", n, err)
	}

	// Instance B starts cold from the bundle: it is behind until it loads what A stored.
	b := bundled(t)
	if lastDate(t, b) != base {
		t.Fatalf("the bundle should start at %s", base)
	}
	n, err := HydrateHMMModelAssetFromTurso(ctx, turso, b)
	if err != nil || n < 1 {
		t.Fatalf("hydrate: %d %v", n, err)
	}
	if got := lastDate(t, b); got != "2099-01-04" {
		t.Fatalf("last_date after hydrate = %s, want 2099-01-04", got)
	}
	if n, err := HydrateHMMModelAssetFromTurso(ctx, turso, b); err != nil || n != 0 {
		t.Fatalf("a second hydrate must find nothing new: %d %v", n, err)
	}
	if n, err := PersistHMMModelAssetToTurso(ctx, turso, b); err != nil || n != 0 {
		t.Fatalf("an instance that only loaded stored predictions has nothing to store: %d %v", n, err)
	}
}

func TestAnotherModelsPredictionsAreNotLoaded(t *testing.T) {
	ctx := context.Background()
	turso := memory(t)
	a := bundled(t)
	extend(t, a, "2099-01-04")
	if _, err := PersistHMMModelAssetToTurso(ctx, turso, a); err != nil {
		t.Fatal(err)
	}
	// The model is retrained: a different trained_at. Its predecessor's rows must not be read.
	if _, err := turso.Exec(`UPDATE hmm_prediction_live SET trained_at = 'an older fit'; UPDATE hmm_state_prob_live SET trained_at = 'an older fit'`); err != nil {
		t.Fatal(err)
	}
	b := bundled(t)
	base := lastDate(t, b)
	if n, err := HydrateHMMModelAssetFromTurso(ctx, turso, b); err != nil || n != 0 {
		t.Fatalf("hydrate read another model's predictions: %d %v", n, err)
	}
	if got := lastDate(t, b); got != base {
		t.Fatalf("last_date moved to %s", got)
	}
}

func TestStoreBarsAppendsAndReplaces(t *testing.T) {
	ctx := context.Background()
	turso := memory(t)
	if err := EnsureBarsSchema(turso); err != nil {
		t.Fatal(err)
	}
	b := func(d string, adj float64) models.Bar {
		return models.Bar{Date: d, Close: adj, AdjClose: adj, Volume: 3}
	}
	if err := storeBars(ctx, turso, "QQQ", []models.Bar{b("2026-10-20", 1), b("2026-10-21", 2)}, true); err != nil {
		t.Fatal(err)
	}
	if err := storeBars(ctx, turso, "QQQ", []models.Bar{b("2026-10-21", 5), b("2026-10-22", 6)}, false); err != nil {
		t.Fatal(err)
	}
	f, err := fingerprintOf(ctx, turso, "daily_bars_live", "QQQ")
	if err != nil || f.n != 3 || f.newest != "2026-10-22" || f.adjSum != 12 {
		t.Fatalf("after append: %+v %v", f, err)
	}
	// A restated history replaces every stored day instead of mixing two price scales.
	if err := storeBars(ctx, turso, "QQQ", []models.Bar{b("2026-10-22", 9)}, true); err != nil {
		t.Fatal(err)
	}
	if f, _ := fingerprintOf(ctx, turso, "daily_bars_live", "QQQ"); f.n != 1 || f.adjSum != 9 {
		t.Fatalf("after replace: %+v", f)
	}
}

// A symbol already stored through the last completed session is not downloaded: with no network
// to call, EnsureBars returning without error shows it made no request.
func TestEnsureBarsDownloadsNothingWhenCurrent(t *testing.T) {
	ctx := context.Background()
	turso := memory(t)
	now := time.Date(2026, 10, 23, 22, 0, 0, 0, time.UTC)
	last := calendar.LastCompletedSession(now).Format("2006-01-02")
	if err := EnsureBarsSchema(turso); err != nil {
		t.Fatal(err)
	}
	if err := storeBars(ctx, turso, "QQQ", []models.Bar{{Date: last, Close: 1, AdjClose: 1, Volume: 1}}, true); err != nil {
		t.Fatal(err)
	}
	n, err := EnsureBars(ctx, turso, []string{"QQQ"}, now, func(string) {})
	if err != nil || n != 0 {
		t.Fatalf("EnsureBars on a current symbol: %d %v", n, err)
	}
}

func TestHydrateBarsRebuildsOnlyWhenTheWorkingCopyDiffers(t *testing.T) {
	ctx := context.Background()
	turso := memory(t)
	if err := EnsureBarsSchema(turso); err != nil {
		t.Fatal(err)
	}
	b := func(d string, adj float64) models.Bar {
		return models.Bar{Date: d, Close: adj, AdjClose: adj, Volume: 3}
	}
	if err := storeBars(ctx, turso, "QQQ", []models.Bar{b("2026-10-20", 1), b("2026-10-21", 2)}, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "market_history_daily.db")
	if n, err := BuildDailyBarsWorkingCopyFromTurso(ctx, turso, path, []string{"QQQ"}); err != nil || n != 1 {
		t.Fatalf("first hydrate: %d %v", n, err)
	}
	if n, err := BuildDailyBarsWorkingCopyFromTurso(ctx, turso, path, []string{"QQQ"}); err != nil || n != 0 {
		t.Fatalf("an identical working copy must not be rebuilt: %d %v", n, err)
	}
	// Same dates and count, but the source rescaled the history: the sum differs, so it is rebuilt.
	if err := storeBars(ctx, turso, "QQQ", []models.Bar{b("2026-10-20", 1.5), b("2026-10-21", 2.5)}, true); err != nil {
		t.Fatal(err)
	}
	if n, err := BuildDailyBarsWorkingCopyFromTurso(ctx, turso, path, []string{"QQQ"}); err != nil || n != 1 {
		t.Fatalf("a restated history must rebuild the working copy: %d %v", n, err)
	}
	if d, ok := dailybars.LastDate(path, "QQQ"); !ok || d != "2026-10-21" {
		t.Fatalf("LastDate = %q %v", d, ok)
	}
}
