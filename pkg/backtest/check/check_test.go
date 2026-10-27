package check

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func writeRun(t *testing.T, signals []models.Signal, trades []models.Trade) (path string, runID int64) {
	t.Helper()
	dir := t.TempDir()
	r, err := storage.OpenResults(dir, "streak")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	id, err := r.WriteRun(storage.RunMeta{StrategyID: "s", Kind: "single", Engine: storage.EngineHourly},
		storage.RunPayload{Signals: signals, Trades: trades})
	if err != nil {
		t.Fatal(err)
	}
	return r.Path, id
}

func sig(sym, date string) models.Signal {
	return models.Signal{StrategyID: "s", Symbol: sym, Date: date, Entry: 1, Direction: "LONG"}
}

func trade(sym, entry, exit string) models.Trade {
	return models.Trade{StrategyID: "s", Symbol: sym, EntryDate: entry, ExitDate: exit, EntryPrice: 10, ExitPrice: 11, Shares: 5}
}

func byName(rs []Result, name string) Result {
	for _, r := range rs {
		if r.Name == name {
			return r
		}
	}
	return Result{Name: "missing " + name, Bad: 1}
}

func TestACleanRunPassesEveryCheckpoint(t *testing.T) {
	path, id := writeRun(t,
		[]models.Signal{sig("AAA", "2026-03-02"), sig("AAA", "2026-03-10")},
		[]models.Trade{trade("AAA", "2026-03-03", "2026-03-05"), trade("AAA", "2026-03-10", "2026-03-12")})
	got, err := Run(Options{ResultsDB: path, RunID: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("%d checkpoints, want 4", len(got))
	}
	for _, r := range got {
		if !r.OK() {
			t.Errorf("%s failed on a clean run: %d %s", r.Name, r.Bad, r.Detail)
		}
	}
}

func TestBadRunsAreCaught(t *testing.T) {
	path, id := writeRun(t,
		[]models.Signal{sig("AAA", "2026-03-02")},
		[]models.Trade{
			trade("AAA", "2026-03-03", "2026-03-09"),
			trade("AAA", "2026-03-05", "2026-03-06"), // overlaps the first and has no signal of its own
			trade("BBB", "2026-03-04", "2026-03-03"), // no signal, exits before it enters
		})
	got, err := Run(Options{ResultsDB: path, RunID: id})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"trade entries":        "BBB",
		"one entry per signal": "s: 3 trades, 1 signals",
		"trades":               "BBB",
		"no overlap":           "AAA",
	} {
		r := byName(got, name)
		if r.OK() || !strings.Contains(r.Detail, want) {
			t.Errorf("%s: bad=%d detail=%q, want a failure naming %q", name, r.Bad, r.Detail, want)
		}
	}
}

func markovDB(t *testing.T, through string, signal float64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "markov.db")
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.RunStage(db, "markov_model_schema", nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO markov_model_meta (symbol, bars, first_date, last_date, trained_at, lookback_days, state_threshold, trained_through)
		 VALUES ('AMD', 100, '2020-01-01', '2020-06-01', 't', 20, 0.05, '` + through + `')`,
		`INSERT INTO markov_prediction VALUES ('AMD', '2020-06-01', 1, 0.6, 0.2, ` + ftoa(signal) + `)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func TestModelChecks(t *testing.T) {
	good, err := Run(Options{MarkovDB: markovDB(t, "2020-05-01", 0.4), Through: "2020-05-01"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range good {
		if !r.OK() {
			t.Errorf("%s failed on a clean model: %s", r.Name, r.Detail)
		}
	}
	late, _ := Run(Options{MarkovDB: markovDB(t, "2020-05-20", 0.4), Through: "2020-05-01"})
	if r := byName(late, "model cutoff"); r.OK() || !strings.Contains(r.Detail, "AMD") {
		t.Errorf("a model trained past the cutoff passed: %+v", r)
	}
	wrong, _ := Run(Options{MarkovDB: markovDB(t, "2020-05-01", 0.9)}) // 0.6 - 0.2 is 0.4, not 0.9
	if r := byName(wrong, "signal arithmetic"); r.OK() {
		t.Errorf("a signal that is not prob_bull - prob_bear passed: %+v", r)
	}
}
