package hmm_strategy

import (
	"context"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/dailybars"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/train"
)

// synthBars is a random walk whose volatility switches between three regimes (stressed, normal,
// calm), so a trained model has a clear highest-volatility state and many crossings.
func synthBars(n int, seed int64) []models.Bar {
	rng := rand.New(rand.NewSource(seed))
	mu := []float64{-0.0012, 0.0002, 0.0009}
	sd := []float64{0.020, 0.008, 0.005}
	day := time.Date(2010, 1, 4, 0, 0, 0, 0, time.UTC)
	price, s := 100.0, 1
	var bars []models.Bar
	for len(bars) < n {
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			day = day.AddDate(0, 0, 1)
			continue
		}
		if rng.Float64() > 0.97 {
			s = rng.Intn(3)
		}
		price *= math.Exp(mu[s] + sd[s]*rng.NormFloat64())
		bars = append(bars, models.Bar{Date: day.Format("2006-01-02"), Close: price, AdjClose: price, Volume: 1})
		day = day.AddDate(0, 0, 1)
	}
	return bars
}

// world is a temp app folder holding a QQQ model trained on synthetic bars and a market database
// with the same sessions.
type world struct {
	market string
	model  string
}

func newWorld(t *testing.T) world {
	t.Helper()
	app := t.TempDir()
	t.Setenv("APP_FOLDER", app)
	bars := synthBars(2600, 7)

	barsDB := filepath.Join(t.TempDir(), "market_history_daily.db")
	db, err := dailybars.Open(barsDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := dailybars.Replace(db, "QQQ", bars); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := train.TrainHMM(context.Background(), train.HMMConfig{BarsDB: barsDB, ModelDB: appenv.HMMDB(), Symbols: []string{"QQQ"}, MinBars: 500, Seed: 1, Out: &strings.Builder{}}); err != nil {
		t.Fatal(err)
	}

	market := filepath.Join(t.TempDir(), "market.db")
	m, err := storage.OpenSQLite(market)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Exec(`CREATE TABLE backtest_start (idx INTEGER, symbol TEXT, Date TEXT, timeframe TEXT,
		open REAL, high REAL, low REAL, close REAL, volume INTEGER, "Adj Close" REAL)`); err != nil {
		t.Fatal(err)
	}
	for i, b := range bars {
		if _, err := m.Exec(`INSERT INTO backtest_start (idx, symbol, Date, timeframe, open, high, low, close, volume, "Adj Close")
			VALUES (?, 'QQQ', ?, '1d', ?, ?, ?, ?, 1000, ?)`, i, b.Date, b.Close, b.Close*1.002, b.Close*0.998, b.Close, b.Close); err != nil {
			t.Fatal(err)
		}
	}
	return world{market: market, model: appenv.HMMDB()}
}

func row(rule string) refdb.HMMStrategy {
	return refdb.HMMStrategy{
		ID: "hmm-test-" + rule, Name: "test " + rule, HMMSymbol: "QQQ", TradeSymbol: "QQQ",
		Rule: rule, StressProb: 0.5, HoldDays: 3, StopLossPct: 0.04, AllocationPct: 0.2,
	}
}

func signals(t *testing.T, w world, r refdb.HMMStrategy) []models.Signal {
	t.Helper()
	s := &Strategy{Row: r}
	s.SetDatabases(w.market, filepath.Join(t.TempDir(), "calc.db"))
	return s.GenerateSignals(nil)
}

func dates(sigs []models.Signal) []string {
	var out []string
	for _, s := range sigs {
		out = append(out, s.Date)
	}
	return out
}

// stressSeries reads the model the way the strategy must: the stressed state is the one with the
// highest volatility, and its filtered probability by date.
func stressSeries(t *testing.T, w world) (ds []string, ps []float64) {
	t.Helper()
	db, err := storage.OpenSQLiteReadOnly(w.model)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var state int
	if err := db.Get(&state, `SELECT state FROM hmm_params WHERE symbol = 'QQQ' ORDER BY mu_vol DESC LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	type pr struct {
		Date string  `db:"date"`
		Prob float64 `db:"prob"`
	}
	var rows []pr
	if err := db.Select(&rows, `SELECT date, prob FROM hmm_state_prob WHERE symbol = 'QQQ' AND state = ? ORDER BY date`, state); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		ds, ps = append(ds, r.Date), append(ps, r.Prob)
	}
	return ds, ps
}

func TestCalmFilterEntersExactlyWhileStressIsLow(t *testing.T) {
	w := newWorld(t)
	ds, ps := stressSeries(t, w)
	var want []string
	for i := range ds {
		if ps[i] < 0.5 {
			want = append(want, ds[i])
		}
	}
	got := signals(t, w, row(RuleCalmFilter))
	if len(want) == 0 || len(want) == len(ds) {
		t.Fatalf("the series does not exercise the rule: %d of %d sessions qualify", len(want), len(ds))
	}
	if !reflect.DeepEqual(dates(got), want) {
		t.Fatalf("calm_filter dates %d, want %d from the saved model", len(got), len(want))
	}
	for _, s := range got {
		if s.Symbol != "QQQ" || s.Entry != 1 || s.Direction != "LONG" || s.OrderType != "market" || s.HoldDaysOverride != 3 {
			t.Fatalf("signal %+v", s)
		}
		if math.Abs(s.StopLoss-s.Close*0.96) > 1e-6 {
			t.Fatalf("stop %v for close %v, want %v", s.StopLoss, s.Close, s.Close*0.96)
		}
	}
}

func TestCalmDownEntersOnlyOnTheCrossing(t *testing.T) {
	w := newWorld(t)
	ds, ps := stressSeries(t, w)
	var want []string
	for i := 1; i < len(ds); i++ {
		if ps[i] < 0.5 && ps[i-1] >= 0.5 {
			want = append(want, ds[i])
		}
	}
	if len(want) < 2 {
		t.Fatalf("the series has %d crossings; the test needs more", len(want))
	}
	got := signals(t, w, row(RuleCalmDown))
	if !reflect.DeepEqual(dates(got), want) {
		t.Fatalf("calm_down dates %v, want %v", dates(got), want)
	}
}

// The state numbers come from sorting by mean return and are not stable between refits, so the
// signals must not depend on them. Swapping the stressed state's number with another's must not
// change one signal.
func TestSignalsDoNotDependOnStateNumbers(t *testing.T) {
	w := newWorld(t)
	before := dates(signals(t, w, row(RuleCalmFilter)))
	if len(before) == 0 {
		t.Fatal("no signals before the relabel")
	}
	db, err := storage.OpenSQLite(w.model)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE hmm_params SET state = state + 10`,
		`UPDATE hmm_params SET state = CASE state WHEN 10 THEN 2 WHEN 12 THEN 0 ELSE state - 10 END`,
		`UPDATE hmm_state_prob SET state = state + 10`,
		`UPDATE hmm_state_prob SET state = CASE state WHEN 10 THEN 2 WHEN 12 THEN 0 ELSE state - 10 END`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	after := dates(signals(t, w, row(RuleCalmFilter)))
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("relabelling the states changed the signals: %d before, %d after", len(before), len(after))
	}
}

// A backtest only reads the model: with none saved it returns no signals and does not create one.
func TestMissingModelGivesNoSignalsAndCreatesNothing(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	w := world{market: newWorld(t).market}
	t.Setenv("APP_FOLDER", t.TempDir())
	if got := signals(t, w, row(RuleCalmFilter)); len(got) != 0 {
		t.Fatalf("%d signals without a model", len(got))
	}
	if _, err := os.Stat(appenv.HMMDB()); err == nil {
		t.Fatal("the backtest created the model database")
	}
}

func TestBacktestLeavesTheModelAsItWas(t *testing.T) {
	w := newWorld(t)
	read := func() []byte {
		b, err := os.ReadFile(w.model)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	before := read()
	signals(t, w, row(RuleCalmFilter))
	signals(t, w, row(RuleCalmDown))
	if string(before) != string(read()) {
		t.Fatal("a backtest changed the model database")
	}
}

func TestValidateRow(t *testing.T) {
	good := row(RuleCalmFilter)
	if err := ValidateRow(good); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*refdb.HMMStrategy){
		"empty id":            func(r *refdb.HMMStrategy) { r.ID = "" },
		"reserved id":         func(r *refdb.HMMStrategy) { r.ID = "hmm_strategy" },
		"empty hmm symbol":    func(r *refdb.HMMStrategy) { r.HMMSymbol = "" },
		"injected symbol":     func(r *refdb.HMMStrategy) { r.TradeSymbol = "QQQ'; DROP TABLE x" },
		"unknown rule":        func(r *refdb.HMMStrategy) { r.Rule = "buy_the_dip" },
		"threshold of one":    func(r *refdb.HMMStrategy) { r.StressProb = 1 },
		"threshold of zero":   func(r *refdb.HMMStrategy) { r.StressProb = 0 },
		"stop of 100 percent": func(r *refdb.HMMStrategy) { r.StopLossPct = 1 },
		"no allocation":       func(r *refdb.HMMStrategy) { r.AllocationPct = 0 },
	} {
		r := good
		mutate(&r)
		if ValidateRow(r) == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// The model symbol is a field of the row and defaults to QQQ, the one model that exists.
func TestHMMSymbolDefaultsToQQQAndTheFamilyResolvesRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "strategies.db")
	db, err := refdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO hmm_strategy (id, name, rule, stress_prob, hold_days, take_profit_pct, stop_loss_pct, allocation_pct, cash_yield, slippage_pct)
		VALUES ('hmm-defaults', 'defaults', 'calm_filter', 0.5, 3, 0, 0.04, 0.2, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	got, ok, err := refdb.HMMStrategyByID(db, "hmm-defaults")
	db.Close()
	if err != nil || !ok {
		t.Fatalf("row: %v %v", ok, err)
	}
	if got.HMMSymbol != "QQQ" || got.TradeSymbol != "QQQ" {
		t.Fatalf("defaults are %q and %q, want QQQ", got.HMMSymbol, got.TradeSymbol)
	}

	RegisterFrom(path)
	s, ok := strategy.Get("hmm-defaults")
	if !ok {
		t.Fatal("the hmm family did not resolve hmm-defaults")
	}
	if strategy.FamilyOf(s) != "hmm" {
		t.Fatalf("family %q", strategy.FamilyOf(s))
	}
	if syms := s.(strategy.RequiredSymbolsProvider).RequiredSymbols(); !reflect.DeepEqual(syms, []string{"QQQ"}) {
		t.Fatalf("required symbols %v", syms)
	}
	cfg := s.DefaultConfig()
	if cfg.Benchmark != "QQQ" || cfg.TradeSymbol != "QQQ" || !cfg.NextDayOpenEntry {
		t.Fatalf("config %+v", cfg)
	}
}

// The live scan shows signal_trace beside each decision: five latest sessions, and the entry flag
// must agree with the signals the same run produced.
func TestSignalTraceAgreesWithTheSignals(t *testing.T) {
	w := newWorld(t)
	calc := filepath.Join(t.TempDir(), "calc.db")
	s := &Strategy{Row: row(RuleCalmFilter)}
	s.SetDatabases(w.market, calc)
	entered := map[string]bool{}
	for _, sig := range s.GenerateSignals(nil) {
		entered[sig.Date] = true
	}
	db, err := storage.OpenSQLiteReadOnly(calc)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rows []struct {
		Date   string `db:"date"`
		Entry  int    `db:"entry"`
		Reason string `db:"reason"`
		Facts  string `db:"facts"`
	}
	if err := db.Select(&rows, `SELECT date, entry, reason, facts FROM signal_trace ORDER BY date`); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("%d trace rows, want the latest 5 sessions", len(rows))
	}
	for _, r := range rows {
		if (r.Entry == 1) != entered[r.Date] {
			t.Errorf("%s: trace says entry=%d but the signals say %v", r.Date, r.Entry, entered[r.Date])
		}
		if r.Reason == "" || !strings.Contains(r.Facts, `"trained_through"`) || !strings.Contains(r.Facts, `"stress_prob"`) {
			t.Errorf("%s: reason %q facts %q", r.Date, r.Reason, r.Facts)
		}
	}
}

// normal_filter enters while the middle volatility rank is the most likely regime. The reference
// ranks the saved states by volatility on its own and compares the three filtered probabilities.
func TestNormalFilterEntersWhileTheMiddleRegimeIsMostLikely(t *testing.T) {
	w := newWorld(t)
	db, err := storage.OpenSQLiteReadOnly(w.model)
	if err != nil {
		t.Fatal(err)
	}
	var states []int
	if err := db.Select(&states, `SELECT state FROM hmm_params WHERE symbol = 'QQQ' ORDER BY mu_vol`); err != nil || len(states) != 3 {
		t.Fatalf("states by volatility: %v %v", states, err)
	}
	var rows []struct {
		Date  string  `db:"date"`
		State int     `db:"state"`
		Prob  float64 `db:"prob"`
	}
	if err := db.Select(&rows, `SELECT date, state, prob FROM hmm_state_prob WHERE symbol = 'QQQ' ORDER BY date, state`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	byDate := map[string]map[int]float64{}
	var order []string
	for _, r := range rows {
		if byDate[r.Date] == nil {
			byDate[r.Date] = map[int]float64{}
			order = append(order, r.Date)
		}
		byDate[r.Date][r.State] = r.Prob
	}
	var want []string
	for _, d := range order {
		p := byDate[d]
		if p[states[1]] > p[states[0]] && p[states[1]] > p[states[2]] {
			want = append(want, d)
		}
	}
	if len(want) == 0 || len(want) == len(order) {
		t.Fatalf("the series does not exercise the rule: %d of %d sessions qualify", len(want), len(order))
	}
	r := row(RuleNormalFilter)
	got := signals(t, w, r)
	if !reflect.DeepEqual(dates(got), want) {
		t.Fatalf("normal_filter dates %d, want %d from the saved model", len(got), len(want))
	}
	// It never enters on a calm_filter-only reading: some normal sessions are under the stress
	// threshold and some calm sessions are too, so the two rules must differ.
	if calm := dates(signals(t, w, row(RuleCalmFilter))); reflect.DeepEqual(calm, want) {
		t.Fatal("normal_filter returned the calm_filter dates")
	}
	if err := ValidateRow(r); err != nil {
		t.Fatalf("a normal_filter row must validate: %v", err)
	}
	if d := (&Strategy{Row: r}).Description(); !strings.Contains(d, "normal") {
		t.Fatalf("description %q", d)
	}
}

func TestStressFilterEntersExactlyWhileStressIsHigh(t *testing.T) {
	w := newWorld(t)
	ds, ps := stressSeries(t, w)
	var want []string
	for i := range ds {
		if ps[i] >= 0.5 {
			want = append(want, ds[i])
		}
	}
	if len(want) == 0 || len(want) == len(ds) {
		t.Fatalf("the series does not exercise the rule: %d of %d sessions qualify", len(want), len(ds))
	}
	got := dates(signals(t, w, row(RuleStressFilter)))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stress_filter dates %d, want %d from the saved model", len(got), len(want))
	}
	// It is the other side of calm_filter: together they cover every session once.
	calm := dates(signals(t, w, row(RuleCalmFilter)))
	if len(calm)+len(got) != len(ds) {
		t.Fatalf("calm_filter %d + stress_filter %d sessions, want %d", len(calm), len(got), len(ds))
	}
	if d := (&Strategy{Row: row(RuleStressFilter)}).Description(); !strings.Contains(d, "stressed regime is at or over") {
		t.Fatalf("description %q", d)
	}
	if err := ValidateRow(row(RuleStressFilter)); err != nil {
		t.Fatal(err)
	}
}
