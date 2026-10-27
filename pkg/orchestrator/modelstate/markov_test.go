package modelstate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func bundledMarkov(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../strategies/deploy/markov_models.db")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "markov_models.db")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func markovMeta(t *testing.T, path, symbol string) (last string, bars int, through sql.NullString) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT last_date, bars, trained_through FROM markov_model_meta WHERE symbol = ?`, symbol).Scan(&last, &bars, &through); err != nil {
		t.Fatal(err)
	}
	return
}

// extendMarkov does what ExtendMarkov's publish stage does for one new session.
func extendMarkov(t *testing.T, path, symbol, date string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO markov_prediction VALUES (?, ?, 1, 0.6, 0.2, 0.4)`, symbol, date); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE markov_model_meta SET trained_through = COALESCE(trained_through, last_date), bars = bars + 1, last_date = ? WHERE symbol = ?`, date, symbol); err != nil {
		t.Fatal(err)
	}
}

func TestMarkovPredictionsSurviveAColdStart(t *testing.T) {
	ctx := context.Background()
	turso := memory(t)

	a := bundledMarkov(t)
	baseLast, baseBars, baseThrough := markovMeta(t, a, "AMD")
	extendMarkov(t, a, "AMD", "2099-01-04")
	// The first persist stores every session past each model's learned-through date: the bundled
	// out-of-sample ones and the one just added.
	if n, err := PersistMarkovModelAssetToTurso(ctx, turso, a); err != nil || n < 1 {
		t.Fatalf("persist: %d %v", n, err)
	}
	if n, err := PersistMarkovModelAssetToTurso(ctx, turso, a); err != nil || n != 0 {
		t.Fatalf("a second persist must find nothing new: %d %v", n, err)
	}

	b := bundledMarkov(t)
	n, err := HydrateMarkovModelAssetFromTurso(ctx, turso, b)
	if err != nil || n != 1 {
		t.Fatalf("hydrate: %d %v", n, err)
	}
	last, bars, through := markovMeta(t, b, "AMD")
	if last != "2099-01-04" || bars != baseBars+1 {
		t.Fatalf("after hydrate last=%s bars=%d, want 2099-01-04 and %d (was %s)", last, bars, baseBars+1, baseLast)
	}
	if baseThrough.Valid && through.String != baseThrough.String {
		t.Fatalf("trained_through changed from %q to %q", baseThrough.String, through.String)
	}
	if n, err := HydrateMarkovModelAssetFromTurso(ctx, turso, b); err != nil || n != 0 {
		t.Fatalf("a second hydrate must find nothing new: %d %v", n, err)
	}
	if n, err := PersistMarkovModelAssetToTurso(ctx, turso, b); err != nil || n != 0 {
		t.Fatalf("an instance that only loaded stored predictions has nothing to store: %d %v", n, err)
	}
}

func TestMarkovRetrainedModelDoesNotReadOldPredictions(t *testing.T) {
	ctx := context.Background()
	turso := memory(t)
	a := bundledMarkov(t)
	extendMarkov(t, a, "AMD", "2099-01-04")
	if _, err := PersistMarkovModelAssetToTurso(ctx, turso, a); err != nil {
		t.Fatal(err)
	}
	if _, err := turso.Exec(`UPDATE markov_extension_live SET trained_at = 'an older fit'`); err != nil {
		t.Fatal(err)
	}
	b := bundledMarkov(t)
	base, _, _ := markovMeta(t, b, "AMD")
	if n, err := HydrateMarkovModelAssetFromTurso(ctx, turso, b); err != nil || n != 0 {
		t.Fatalf("hydrate read another model's predictions: %d %v", n, err)
	}
	if last, _, _ := markovMeta(t, b, "AMD"); last != base {
		t.Fatalf("last_date moved to %s", last)
	}
}
