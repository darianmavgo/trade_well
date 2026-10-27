package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/jmoiron/sqlx"
)

// Many workers writing runs into one results.db at the same time must all
// land, each run whole, with distinct run ids.
func TestWriteRunConcurrentWriters(t *testing.T) {
	r, err := OpenResults(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	const workers, perWorker = 16, 10
	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id := fmt.Sprintf("s%d_%d", w, i)
				_, err := r.WriteRun(RunMeta{StrategyID: id, Kind: "single"}, RunPayload{
					Trades:  []models.Trade{{Symbol: "VOO", EntryDate: "2024-01-02", ExitDate: "2024-01-05", NetPnL: 5}, {Symbol: "VOO", EntryDate: "2024-02-02", ExitDate: "2024-02-05", NetPnL: -1}},
					Equity:  []models.DailyEquityPoint{{Date: "2024-01-02", TotalEquity: 100}, {Date: "2024-01-03", TotalEquity: 101}, {Date: "2024-01-04", TotalEquity: 102}},
					Reports: []NamedReport{{StrategyID: id, Report: models.PerformanceReport{CAGR: float64(i), TotalTrades: 2}}},
				})
				if err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	var runs, trades, curve, perf int
	for table, dst := range map[string]*int{"runs": &runs, "trades": &trades, "equity_curve": &curve, "performance_summary": &perf} {
		if err := r.DB.Get(dst, "SELECT COUNT(*) FROM "+table); err != nil {
			t.Fatal(err)
		}
	}
	n := workers * perWorker
	if runs != n || trades != 2*n || curve != 3*n || perf != n {
		t.Fatalf("runs=%d trades=%d curve=%d perf=%d, want %d/%d/%d/%d", runs, trades, curve, perf, n, 2*n, 3*n, n)
	}
	var distinct int
	if err := r.DB.Get(&distinct, "SELECT COUNT(DISTINCT run_id) FROM trades"); err != nil || distinct != n {
		t.Fatalf("distinct run ids on trades = %d (err %v), want %d", distinct, err, n)
	}
}

// A run that fails midway leaves nothing behind.
func TestWriteRunIsAtomic(t *testing.T) {
	r, err := OpenResults(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// Drop a table the payload writes to, so the write fails after the runs row is inserted.
	if _, err := r.DB.Exec("DROP TABLE equity_curve"); err != nil {
		t.Fatal(err)
	}
	_, err = r.WriteRun(RunMeta{StrategyID: "x"}, RunPayload{
		Trades: []models.Trade{{Symbol: "VOO"}},
		Equity: []models.DailyEquityPoint{{Date: "2024-01-02", TotalEquity: 100}},
	})
	if err == nil {
		t.Fatal("expected a write error")
	}
	var runs, trades int
	_ = r.DB.Get(&runs, "SELECT COUNT(*) FROM runs")
	_ = r.DB.Get(&trades, "SELECT COUNT(*) FROM trades")
	if runs != 0 || trades != 0 {
		t.Fatalf("partial run left behind: runs=%d trades=%d", runs, trades)
	}
}

// Run directories are numbered one above the highest there is, even when two
// processes ask at once, and a family's result file lives inside its run.
func TestNewRunNumbersAndFamilyFiles(t *testing.T) {
	root := t.TempDir()
	if n, dir := LatestRunDir(root); n != 0 || dir != "" {
		t.Fatalf("empty root: %d %q", n, dir)
	}
	var wg sync.WaitGroup
	got := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, dir, err := NewRun(root)
			if err != nil || filepath.Base(dir) != fmt.Sprint(n) {
				t.Errorf("NewRun: %d %s %v", n, dir, err)
			}
			got <- n
		}()
	}
	wg.Wait()
	close(got)
	seen := map[int]bool{}
	for n := range got {
		if seen[n] {
			t.Fatalf("run %d handed out twice", n)
		}
		seen[n] = true
	}
	if n, _ := LatestRunDir(root); n != 8 {
		t.Fatalf("latest run %d, want 8", n)
	}
	// directories that are not run numbers are ignored
	for _, name := range []string{"2026-10-01", "007", "notes"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := LatestRunDir(root); n != 8 {
		t.Fatalf("latest run %d with stray directories, want 8", n)
	}
	if _, err := RunDir(root, 99); err == nil {
		t.Fatal("expected an error for a run that does not exist")
	}
	dir, err := RunDir(root, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, fam := range []string{"markov", "tree"} {
		r, err := ResultsFor(dir, fam)
		if err != nil {
			t.Fatal(err)
		}
		if r.Path != filepath.Join(dir, fam+".db") {
			t.Fatalf("path %s", r.Path)
		}
	}
	CloseSharedResults()
	files, err := ResultFiles(dir)
	if err != nil || len(files) != 2 || filepath.Base(files[0]) != "markov.db" || filepath.Base(files[1]) != "tree.db" {
		t.Fatalf("result files %v (err %v)", files, err)
	}
}

// A results file made before runs.engine existed gets the column, and its old runs read as no
// engine (the daily simulator) while new ones record theirs.
func TestOpenResultsAddsEngineColumnToAnOlderFile(t *testing.T) {
	dir := t.TempDir()
	old, err := sqlx.Open("sqlite", resultsPath(dir, "single"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE runs (run_id INTEGER PRIMARY KEY AUTOINCREMENT, strategy_id TEXT NOT NULL,
		kind TEXT NOT NULL DEFAULT 'single', window_start TEXT, window_end TEXT, capital REAL, alloc_pct REAL,
		default_asset TEXT, market_max_date TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP);
		INSERT INTO runs (strategy_id) VALUES ('before')`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	r, err := OpenResults(dir, "single")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.WriteRun(RunMeta{StrategyID: "after", Kind: "single", Engine: EngineHourly}, RunPayload{}); err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Strategy string  `db:"strategy_id"`
		Engine   *string `db:"engine"`
	}
	if err := r.DB.Select(&got, `SELECT strategy_id, engine FROM runs ORDER BY run_id`); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Engine != nil || got[1].Engine == nil || *got[1].Engine != EngineHourly {
		t.Fatalf("runs %+v, want the old run with no engine and the new one hourly", got)
	}
}
