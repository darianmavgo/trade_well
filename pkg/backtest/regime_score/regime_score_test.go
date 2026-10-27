package regime_score

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// writeResults writes one results database <dir>/<family>.db holding one run of trades.
func writeResults(t *testing.T, dir, family, strategy string, trades []models.Trade) {
	t.Helper()
	r, err := storage.OpenResults(dir, family)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.WriteRun(storage.RunMeta{StrategyID: strategy, Kind: "single", Engine: storage.EngineHourly},
		storage.RunPayload{Trades: trades}); err != nil {
		t.Fatal(err)
	}
}

func trade(entry, exit string, price, pnl, ret float64, hold int) models.Trade {
	return models.Trade{Symbol: "XYZ", EntryDate: entry, ExitDate: exit, EntryPrice: price,
		NetPnL: pnl, ReturnPct: ret, HoldDays: hold}
}

func writeModel(t *testing.T, path string) {
	t.Helper()
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE markov_prediction(symbol VARCHAR, date VARCHAR, state BIGINT, prob_bull DOUBLE, prob_bear DOUBLE, signal DOUBLE);
		INSERT INTO markov_prediction VALUES
		('SPY', '2024-01-02',  1, 0, 0, 0),
		('SPY', '2024-01-03',  0, 0, 0, 0),
		('SPY', '2024-01-04', -1, 0, 0, 0),
		('SPY', '2024-01-05', -1, 0, 0, 0),
		('SPY', '2024-01-08',  1, 0, 0, 0),
		('QQQ', '2024-01-02', -1, 0, 0, 0),
		('QQQ', '2024-01-08', -1, 0, 0, 0)`); err != nil {
		t.Fatal(err)
	}
}

type labelled struct {
	Entry  string  `db:"entry_date"`
	Win    string  `db:"win"`
	Regime string  `db:"regime"`
	Date   *string `db:"regime_date"`
}

func TestRunLabelsAndScores(t *testing.T) {
	runs := t.TempDir()
	model := filepath.Join(t.TempDir(), "markov_models.db")
	writeModel(t, model)

	// Report 1: five trades. Friday 2024-01-05 is bear, so the Monday entry is bear.
	writeResults(t, filepath.Join(runs, "1"), "alpha", "s-a", []models.Trade{
		trade("2024-01-08", "2024-01-09", 10, 100, 0.02, 1),          // Monday: Friday's bear
		trade("2024-01-03", "2024-01-04", 10, -50, -0.01, 1),         // Wednesday: Tuesday's bull
		trade("2024-01-04", "2024-01-05", 10, 30, 0.03, 1),           // Thursday: Wednesday's sideways
		trade("2024-01-02", "2024-01-03", 10, 40, 0.005, 1),          // first session: nothing before it
		trade("2024-01-09 10:00:00", "2024-01-10", 10, 10, 0.004, 1), // after the last session, with a time
	})
	// Report 2 re-ran the strategy: its 2024-01-08 trade is a duplicate, 2024-01-05 is new.
	writeResults(t, filepath.Join(runs, "2"), "alpha", "s-a", []models.Trade{
		trade("2024-01-08", "2024-01-09", 10, 100, 0.02, 1),
		trade("2024-01-05", "2024-01-08", 10, -20, -0.005, 1), // Friday: Thursday's bear
	})
	// The same entry date held out is a different window, not a duplicate.
	writeResults(t, filepath.Join(runs, "2", "oos"), "alpha", "s-a", []models.Trade{
		trade("2024-01-04", "2024-01-05", 10, 30, 0.03, 1),
	})
	// A database in a run folder with no trades table is skipped, and a database outside a run
	// folder is not looked at.
	other, err := storage.OpenSQLite(filepath.Join(runs, "3", "other.db"))
	if err != nil {
		t.Fatal(err)
	}
	other.Exec(`CREATE TABLE notes(x)`)
	other.Close()
	if err := os.WriteFile(filepath.Join(runs, "stray.db"), []byte("not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	res, err := Run(context.Background(), Config{RunsDir: runs, ModelDB: model, OutDir: t.TempDir(), MinTrades: 2, Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || !strings.HasSuffix(res.Skipped[0], filepath.Join("3", "other.db")) {
		t.Fatalf("skipped = %v", res.Skipped)
	}

	db, err := storage.OpenSQLiteReadOnly(res.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var got []labelled
	if err := db.Select(&got, `SELECT entry_date, win, regime, regime_date FROM trade_regime ORDER BY win, entry_date`); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"in_sample 2024-01-02": "unlabeled",
		"in_sample 2024-01-03": "bull",
		"in_sample 2024-01-04": "sideways",
		"in_sample 2024-01-05": "bear",
		"in_sample 2024-01-08": "bear",
		"in_sample 2024-01-09": "bull",
		"oos 2024-01-04":       "sideways",
	}
	if len(got) != len(want) {
		t.Fatalf("scored %d trades, want %d: %+v", len(got), len(want), got)
	}
	for _, g := range got {
		if w := want[g.Win+" "+g.Entry]; w != g.Regime {
			t.Errorf("%s %s: regime %s, want %s", g.Win, g.Entry, g.Regime, w)
		}
	}

	cov := map[string]int{}
	var rows []struct {
		What string `db:"what"`
		N    int    `db:"n"`
	}
	if err := db.Select(&rows, `SELECT what, n FROM regime_coverage`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		cov[r.What] = r.N
	}
	if cov["trades loaded"] != 8 || cov["duplicates dropped"] != 1 || cov["trades scored"] != 7 || cov["unlabeled (no earlier session)"] != 1 {
		t.Errorf("coverage = %v", cov)
	}

	type score struct {
		N     int      `db:"n"`
		Win   float64  `db:"win_rate"`
		Avg   float64  `db:"avg_return_pct"`
		PnL   float64  `db:"total_net_pnl"`
		PF    *float64 `db:"profit_factor"`
		Thin  bool     `db:"thin"`
		VsAll float64  `db:"vs_all_avg_return_pct"`
	}
	var bear score
	if err := db.Get(&bear, `SELECT n, win_rate, avg_return_pct, total_net_pnl, profit_factor, thin, vs_all_avg_return_pct
		FROM regime_score WHERE strategy_id = 's-a' AND win = 'in_sample' AND regime = 'bear'`); err != nil {
		t.Fatal(err)
	}
	// Bear trades: +100 (2%) and -20 (-0.5%).
	if bear.N != 2 || bear.Win != 0.5 || bear.PnL != 80 || bear.PF == nil || *bear.PF != 5 || bear.Thin {
		t.Errorf("bear = %+v", bear)
	}
	if math.Abs(bear.Avg-0.0075) > 1e-12 {
		t.Errorf("bear avg = %v", bear.Avg)
	}
	// ALL over the six in-sample trades: (0.02-0.01+0.03+0.005+0.004-0.005)/6.
	if wantVs := 0.0075 - 0.044/6; math.Abs(bear.VsAll-wantVs) > 1e-12 {
		t.Errorf("bear vs ALL = %v, want %v", bear.VsAll, wantVs)
	}
	var sideways score
	if err := db.Get(&sideways, `SELECT n, win_rate, avg_return_pct, total_net_pnl, profit_factor, thin, vs_all_avg_return_pct
		FROM regime_score WHERE strategy_id = 's-a' AND win = 'in_sample' AND regime = 'sideways'`); err != nil {
		t.Fatal(err)
	}
	if sideways.N != 1 || !sideways.Thin || sideways.PF != nil {
		t.Errorf("sideways = %+v", sideways) // one winning trade: no losses, so no profit factor
	}
	var pooled int
	if err := db.Get(&pooled, `SELECT n FROM regime_pooled WHERE win = 'in_sample' AND regime = 'ALL'`); err != nil || pooled != 6 {
		t.Errorf("pooled ALL in_sample = %d, %v", pooled, err)
	}
	if !strings.Contains(out.String(), "All strategies pooled") || !strings.Contains(out.String(), "bear") {
		t.Errorf("report missing tables:\n%s", out.String())
	}
}

func TestRunFreshDatabaseEachTime(t *testing.T) {
	runs := t.TempDir()
	model := filepath.Join(t.TempDir(), "markov_models.db")
	writeModel(t, model)
	writeResults(t, filepath.Join(runs, "1"), "alpha", "s-a", []models.Trade{trade("2024-01-08", "2024-01-09", 10, 1, 0.01, 1)})
	out := t.TempDir()
	for i := 0; i < 2; i++ {
		res, err := Run(context.Background(), Config{RunsDir: runs, ModelDB: model, OutDir: out})
		if err != nil {
			t.Fatal(err)
		}
		db, err := storage.OpenSQLiteReadOnly(res.DB)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if err := db.Get(&n, `SELECT COUNT(*) FROM trade_all`); err != nil || n != 1 {
			t.Fatalf("run %d: trade_all = %d, %v", i, n, err)
		}
		db.Close()
	}
}

func TestRunErrors(t *testing.T) {
	runs := t.TempDir()
	model := filepath.Join(t.TempDir(), "markov_models.db")
	writeModel(t, model)
	if _, err := Run(context.Background(), Config{RunsDir: runs, ModelDB: model, OutDir: t.TempDir()}); err == nil {
		t.Error("no results databases: want an error")
	}
	writeResults(t, filepath.Join(runs, "1"), "alpha", "s-a", []models.Trade{trade("2024-01-08", "2024-01-09", 10, 1, 0.01, 1)})
	if _, err := Run(context.Background(), Config{RunsDir: runs, ModelDB: model, OutDir: t.TempDir(), Symbol: "NOPE"}); err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Errorf("unknown symbol: err = %v", err)
	}
	if _, err := Run(context.Background(), Config{RunsDir: runs, ModelDB: filepath.Join(t.TempDir(), "missing.db"), OutDir: t.TempDir()}); err == nil {
		t.Error("missing model: want an error")
	}
}
