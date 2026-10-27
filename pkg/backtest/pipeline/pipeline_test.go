package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/hold_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/streak_strategy"
)

// refWith writes a real reference database holding the given streak and hold rows.
func refWith(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "strategies.db")
	db, err := refdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO streak_strategy (id, name, signal_symbol, trade_symbol, direction, signal_days, hold_days, take_profit_pct, stop_loss_pct, regime, allocation_pct, cash_yield, slippage_pct, next_day_limit)
		 VALUES ('streak-googl-down3-googl','x','GOOGL','GOOGL','drop',3,5,0,0,'All Regimes',0.1,0,0,0),
		        ('streak-voo-buy-tecl','x','VOO','TECL','drop',3,8,0,0,'All Regimes',0.1,0,0,0),
		        ('streak-aapl-down3','x','AAPL','AAPL','drop',3,5,0,0,'All Regimes',0.1,0,0,0)`,
		`CREATE TABLE IF NOT EXISTS hold_strategy (id TEXT PRIMARY KEY, name TEXT, symbol TEXT, total_return INTEGER NOT NULL DEFAULT 0,
		   allocation_pct REAL, cash_yield REAL, slippage_pct REAL, trailing_stop_pct REAL NOT NULL DEFAULT 0, sma_reentry_period INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO hold_strategy (id, name, symbol, allocation_pct, cash_yield, slippage_pct) VALUES ('googl-buy-hold','x','GOOGL',1,0,0), ('aapl-buy-hold','x','AAPL',1,0,0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestStrategiesForTakesOnlyTheSymbolsRows(t *testing.T) {
	ref := refWith(t)
	got, err := strategiesFor(ref, []string{"GOOGL"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"googl-buy-hold", "streak-googl-down3-googl"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GOOGL strategies %v, want %v", got, want)
	}
	got, _ = strategiesFor(ref, []string{"GOOGL", "TECL"})
	if len(got) != 3 {
		t.Fatalf("GOOGL+TECL strategies %v, want 3 (a trade symbol counts)", got)
	}
}

func TestCleanSymbolsRejectsSQL(t *testing.T) {
	if _, err := cleanSymbols([]string{"GOOGL", "x'); DROP TABLE y;--"}); err == nil {
		t.Fatal("a symbol with quotes must be refused")
	}
	got, err := cleanSymbols([]string{" googl", "AAPL", "GOOGL"})
	if err != nil || !reflect.DeepEqual(got, []string{"AAPL", "GOOGL"}) {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestEveryRunGetsItsOwnFolder(t *testing.T) {
	root := t.TempDir()
	id1, dir1, _, err := openRun(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	id2, dir2, _, err := openRun(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if id1 == id2 || dir1 == dir2 {
		t.Fatalf("two runs share a folder: %d %s and %d %s", id1, dir1, id2, dir2)
	}
	if _, dir, resumed, err := openRun(root, id1); err != nil || !resumed || dir != dir1 {
		t.Fatalf("resume run %d: %s %v %v", id1, dir, resumed, err)
	}
	if _, _, _, err := openRun(root, 99); err == nil {
		t.Fatal("resuming a run that does not exist must fail")
	}
}

func TestLockKeepsASecondPipelineOut(t *testing.T) {
	dir := t.TempDir()
	first, err := acquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(dir); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("second lock: %v, want an in-use error", err)
	}
	first.release()
	again, err := acquireLock(dir)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again.release()
}

func TestLockFromAGoneProcessIsTakenOver(t *testing.T) {
	dir := t.TempDir()
	// A pid far above any live process on the machine.
	if err := os.WriteFile(filepath.Join(dir, lockFile), []byte("2147483646\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := acquireLock(dir)
	if err != nil {
		t.Fatalf("a stale lock should be taken over: %v", err)
	}
	l.release()
}

// newCtx is a run context over a real state database in a temp run folder.
func newCtx(t *testing.T, cfg Config) *runCtx {
	t.Helper()
	dir := t.TempDir()
	state, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { state.Close() })
	return &runCtx{ctx: context.Background(), cfg: cfg, runID: 1, dir: dir, state: state, out: os.Stderr}
}

func TestScopeIsFixedAtStartAndRefusesAMixedResume(t *testing.T) {
	cfg := withDefaults(Config{Symbols: []string{"googl"}, RefDB: refWith(t)})
	rc := newCtx(t, cfg)
	if err := rc.scope(false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rc.symbols, []string{"GOOGL"}) || len(rc.strategies) != 2 {
		t.Fatalf("scope %v %v", rc.symbols, rc.strategies)
	}
	if rc.primary != "streak-googl-down3-googl" {
		t.Fatalf("primary %q", rc.primary)
	}

	resume := &runCtx{ctx: rc.ctx, cfg: withDefaults(Config{RefDB: cfg.RefDB}), runID: 1, dir: rc.dir, state: rc.state}
	if err := resume.scope(true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resume.symbols, rc.symbols) || !reflect.DeepEqual(resume.strategies, rc.strategies) {
		t.Fatalf("resume read %v %v, want the stored scope", resume.symbols, resume.strategies)
	}
	mixed := &runCtx{ctx: rc.ctx, cfg: withDefaults(Config{Symbols: []string{"AAPL"}, RefDB: cfg.RefDB}), runID: 1, dir: rc.dir, state: rc.state}
	if err := mixed.scope(true); err == nil || !strings.Contains(err.Error(), "scoped to GOOGL") {
		t.Fatalf("resuming with other symbols: %v, want a scope error", err)
	}
}

func TestPromotedRowsJoinTheScopeOnlyWhenTheListIsNotExplicit(t *testing.T) {
	ref := refWith(t)
	rc := newCtx(t, withDefaults(Config{Symbols: []string{"GOOGL"}, RefDB: ref}))
	if err := rc.scope(false); err != nil {
		t.Fatal(err)
	}
	db, err := refdb.Open(ref)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO streak_strategy (id, name, signal_symbol, trade_symbol, direction, signal_days, hold_days, take_profit_pct, stop_loss_pct, regime, allocation_pct, cash_yield, slippage_pct, next_day_limit)
		VALUES ('streak-googl-down2-googl','x','GOOGL','GOOGL','drop',2,5,0,0,'All Regimes',0.1,0,0,0)`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.refreshScope(); err != nil {
		t.Fatal(err)
	}
	if len(rc.strategies) != 3 {
		t.Fatalf("scope %v, want the promoted row added", rc.strategies)
	}
	if _, err := rc.state.Exec(`UPDATE pipeline_run SET explicit_strategies = 1`); err != nil {
		t.Fatal(err)
	}
	db, _ = refdb.Open(ref)
	db.Exec(`INSERT INTO streak_strategy (id, name, signal_symbol, trade_symbol, direction, signal_days, hold_days, take_profit_pct, stop_loss_pct, regime, allocation_pct, cash_yield, slippage_pct, next_day_limit)
		VALUES ('streak-googl-down1-googl','x','GOOGL','GOOGL','drop',1,5,0,0,'All Regimes',0.1,0,0,0)`)
	db.Close()
	if err := rc.refreshScope(); err != nil {
		t.Fatal(err)
	}
	if len(rc.strategies) != 3 {
		t.Fatalf("an explicit list grew to %v", rc.strategies)
	}
}

func TestStepsAreRecordedAndResumed(t *testing.T) {
	rc := newCtx(t, withDefaults(Config{}))
	marker := filepath.Join(rc.dir, "ran")
	runs := 0
	ok := step{name: "ok", run: func(*runCtx) error {
		runs++
		return os.WriteFile(marker, []byte("x"), 0o644)
	}}
	bad := step{name: "bad", run: func(*runCtx) error { panic("boom") }}

	if got := rc.runStep(1, ok); got.Status != StatusDone {
		t.Fatalf("ok step: %+v", got)
	}
	if got := rc.runStep(1, ok); got.Status != StatusDone || runs != 1 {
		t.Fatalf("a finished step ran again (runs=%d)", runs)
	}
	rc.cfg.Redo = true
	rc.runStep(1, ok)
	if runs != 2 {
		t.Fatalf("-redo should run the step again, runs=%d", runs)
	}
	got := rc.runStep(2, bad)
	if got.Status != StatusFailed || !strings.Contains(got.Error, "boom") {
		t.Fatalf("panicking step: %+v", got)
	}
	if s, _ := stepStatus(rc.state, "bad"); s != StatusFailed {
		t.Fatalf("stored status %q", s)
	}
}

func TestSkippedStepsSayWhy(t *testing.T) {
	rc := newCtx(t, withDefaults(Config{SkipNetwork: true}))
	got := rc.runStep(1, step{name: "net", network: true, run: func(*runCtx) error { t.Fatal("ran"); return nil }})
	if got.Status != StatusSkipped || !strings.Contains(got.Error, "network") {
		t.Fatalf("%+v", got)
	}
	got = rc.runStep(2, step{name: "stack", needsPrimary: true, run: func(*runCtx) error { t.Fatal("ran"); return nil }})
	if got.Status != StatusSkipped || !strings.Contains(got.Error, "primary") {
		t.Fatalf("%+v", got)
	}
}

func TestSelectStepsRefusesUnknownNames(t *testing.T) {
	if _, err := selectSteps(Config{Steps: []string{"backtest", "nonsense"}}); err == nil {
		t.Fatal("an unknown step name must be refused")
	}
	got, err := selectSteps(Config{Steps: []string{"backtest", "train"}, Skip: []string{"train"}})
	if err != nil || len(got) != 1 || got[0].name != "backtest" {
		t.Fatalf("got %v %v", got, err)
	}
}

// scopedCtx is a run context whose scope is every strategy of GOOGL in a real
// temporary reference database, with those families registered from it.
func scopedCtx(t *testing.T, extra ...string) *runCtx {
	t.Helper()
	ref := refWith(t)
	streak_strategy.RegisterFrom(ref)
	hold_strategy.RegisterFrom(ref)
	rc := newCtx(t, withDefaults(Config{Symbols: []string{"GOOGL"}, RefDB: ref}))
	if err := rc.scope(false); err != nil {
		t.Fatal(err)
	}
	rc.strategies = append(rc.strategies, extra...)
	rc.selected = map[string]bool{}
	return rc
}

func TestOnlyWhatTheScopeReadsIsDownloaded(t *testing.T) {
	rc := scopedCtx(t)
	got := rc.neededSymbols()
	has := map[string]bool{}
	for _, s := range got {
		has[s] = true
	}
	if !has["GOOGL"] {
		t.Errorf("needed %v, want GOOGL", got)
	}
	if !has["VOO"] {
		t.Errorf("needed %v, want VOO (googl-buy-hold benchmarks against it)", got)
	}
	for _, other := range []string{"AAPL", "TECL", "SGOV"} {
		if has[other] {
			t.Errorf("needed %v, but nothing in scope reads %s", got, other)
		}
	}
	rc.selected["stack_eval"] = true
	if !contains(rc.neededSymbols(), "SGOV") {
		t.Errorf("the park symbol is needed once stack_eval runs: %v", rc.neededSymbols())
	}
}

func TestOptionHistoryIsOnlyForCoveredCallStrategies(t *testing.T) {
	rc := scopedCtx(t)
	if why := whenCoveredCall(rc); why == "" {
		t.Fatal("GOOGL has no covered-call strategy, so option history should be skipped")
	}
	if got := rc.coveredCallUnderlyings(); len(got) != 0 {
		t.Fatalf("underlyings %v", got)
	}
	rc = scopedCtx(t, "schd-covered-call", "vym-covered-call-5pct")
	if why := whenCoveredCall(rc); why != "" {
		t.Fatalf("a covered-call strategy is in scope but the step is skipped: %s", why)
	}
	if got := rc.coveredCallUnderlyings(); !reflect.DeepEqual(got, []string{"SCHD", "VYM"}) {
		t.Fatalf("underlyings %v, want SCHD and VYM", got)
	}
}

func TestStepsWithNothingToDoAreSkippedUnlessNamed(t *testing.T) {
	rc := scopedCtx(t)
	opt := step{name: "market_history_options", when: whenCoveredCall, run: func(*runCtx) error { return nil }}
	if why := opt.skipReason(rc); why == "" {
		t.Error("options step should be skipped by default for GOOGL")
	}
	rc.cfg.explicit = map[string]bool{"market_history_options": true}
	if why := opt.skipReason(rc); why != "" {
		t.Errorf("a step named in -steps must run, got skip: %s", why)
	}
	for _, s := range []struct {
		name string
		when func(*runCtx) string
	}{{"train", whenTrainable}, {"study", whenHMM}} {
		rc.cfg.explicit = nil
		if s.when(rc) == "" {
			t.Errorf("%s should be skipped when no markov, tree or hmm strategy is in scope", s.name)
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestStrategiesForFamilyStreak(t *testing.T) {
	ref := filepath.Join(t.TempDir(), "strategies.db")
	db, err := storage.OpenSQLite(ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE streak_strategy (id TEXT, signal_symbol TEXT, trade_symbol TEXT);
		INSERT INTO streak_strategy VALUES ('streak-a', 'voo', 'tecl'), ('streak-b', 'googl', 'googl'), ('streak-c', 'aapl', 'aapl')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	ids, syms, err := strategiesForFamily(ref, "streak", 0)
	if err != nil || strings.Join(ids, ",") != "streak-a,streak-b,streak-c" || strings.Join(syms, ",") != "AAPL,GOOGL,TECL,VOO" {
		t.Fatalf("all rows: %v %v %v", ids, syms, err)
	}
	ids, syms, err = strategiesForFamily(ref, "streak", 2)
	if err != nil || strings.Join(ids, ",") != "streak-a,streak-b" || strings.Join(syms, ",") != "GOOGL,TECL,VOO" {
		t.Fatalf("limit 2: %v %v %v", ids, syms, err)
	}
	if _, _, err := strategiesForFamily(ref, "nope", 0); err == nil {
		t.Fatal("unknown family must fail")
	}
}
