package market_context

import (
	"context"
	"math"
	"math/rand"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"github.com/darianmavgo/trade_well/pkg/backtest/dailybars"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/study"
	"github.com/darianmavgo/trade_well/pkg/backtest/train"
)

// qqqBars is a random walk whose volatility switches between three regimes.
func qqqBars(n int) []models.Bar {
	rng := rand.New(rand.NewSource(11))
	mu := []float64{-0.0012, 0.0002, 0.0009}
	sd := []float64{0.020, 0.008, 0.005}
	day := time.Date(2012, 1, 2, 0, 0, 0, 0, time.UTC)
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

type etfWorld struct {
	market, model, universe, results string
	dates                            []string
	closes                           map[string][]float64
}

// newETFWorld trains a QQQ model on synthetic bars and writes a market database with three ETFs
// on the same sessions: LEV (a noisy 3x of QQQ), FLAT (random, unrelated to the regime) and
// SPLIT (FLAT with one 4x jump, an unadjusted split).
func newETFWorld(t *testing.T) etfWorld {
	t.Helper()
	dir := t.TempDir()
	w := etfWorld{
		market: filepath.Join(dir, "market.db"), model: filepath.Join(dir, "hmm_models.db"),
		universe: filepath.Join(dir, "universe.db"), results: filepath.Join(dir, "out", "hmm_regime_etf.db"),
		closes: map[string][]float64{},
	}
	bars := qqqBars(1600)

	barsDB := filepath.Join(dir, "market_history_daily.db")
	db, err := dailybars.Open(barsDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := dailybars.Replace(db, "QQQ", bars); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := train.TrainHMM(context.Background(), train.HMMConfig{BarsDB: barsDB, ModelDB: w.model, Symbols: []string{"QQQ"}, MinBars: 500, Seed: 1, Out: &strings.Builder{}}); err != nil {
		t.Fatal(err)
	}

	rng := rand.New(rand.NewSource(5))
	lev, flat, split := 100.0, 100.0, 100.0
	prev := bars[0].AdjClose
	for i, b := range bars {
		w.dates = append(w.dates, b.Date)
		if i > 0 {
			r := b.AdjClose/prev - 1
			lev *= 1 + 3*r + 0.002*rng.NormFloat64()
			f := 0.0004 + 0.01*rng.NormFloat64()
			flat *= 1 + f
			split *= 1 + f
			if i == 800 {
				split *= 4
			}
		}
		prev = b.AdjClose
		w.closes["LEV"] = append(w.closes["LEV"], lev)
		w.closes["FLAT"] = append(w.closes["FLAT"], flat)
		w.closes["SPLIT"] = append(w.closes["SPLIT"], split)
	}

	m, err := storage.OpenSQLite(w.market)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Exec(`CREATE TABLE backtest_start (Date TEXT, timeframe TEXT, close REAL, symbol TEXT)`); err != nil {
		t.Fatal(err)
	}
	for sym, cs := range w.closes {
		for i, c := range cs {
			if _, err := m.Exec(`INSERT INTO backtest_start (Date, timeframe, close, symbol) VALUES (?, '1d', ?, ?)`, w.dates[i], c, sym); err != nil {
				t.Fatal(err)
			}
		}
	}
	// An hourly row and a non-ETF symbol must not enter the study.
	if _, err := m.Exec(`INSERT INTO backtest_start (Date, timeframe, close, symbol) VALUES ('2013-01-02 10:00:00', '1h', 5, 'LEV')`); err != nil {
		t.Fatal(err)
	}
	for i, d := range w.dates {
		if _, err := m.Exec(`INSERT INTO backtest_start (Date, timeframe, close, symbol) VALUES (?, '1d', ?, 'STK')`, d, 50+float64(i)); err != nil {
			t.Fatal(err)
		}
	}

	u, err := storage.OpenSQLite(w.universe)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	if _, err := u.Exec(`CREATE TABLE universe (symbol TEXT, name TEXT, asset_type TEXT, is_etf INT, leverage TEXT, direction TEXT, category TEXT, active INT);
		INSERT INTO universe VALUES ('LEV','Lev','ETF',1,'3x','long','technology',1), ('FLAT','Flat','ETF',1,'none','long','broad_market_sp500',1),
		('SPLIT','Split','ETF',1,'none','long','other_etf',1), ('STK','Stock','CS',0,'none','long','x',1)`); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w etfWorld) run(t *testing.T, minStress int) error {
	t.Helper()
	s := &HMMRegimeETF{HMMDB: w.model, UniverseDB: w.universe, MinStress: minStress, MinRegime: 5, TopN: 2}
	s.SetDatabases(w.market, w.results)
	return s.Run()
}

type resultRow struct {
	Symbol     string  `db:"symbol"`
	Sample     string  `db:"sample"`
	N          int     `db:"n"`
	NStress    int     `db:"n_stress"`
	NNot       int     `db:"n_not_stress"`
	MeanCalm   float64 `db:"mean_calm"`
	MeanMid    float64 `db:"mean_mid"`
	MeanStress float64 `db:"mean_stress"`
	VolRatio   float64 `db:"vol_ratio"`
	TStress    float64 `db:"t_stress"`
	Eta2       float64 `db:"eta2"`
	CompAll    float64 `db:"compound_all"`
	CompFilter float64 `db:"compound_filter"`
}

func readResults(t *testing.T, path string) map[string]resultRow {
	t.Helper()
	db, err := sqlx.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rows []resultRow
	if err := db.Select(&rows, `SELECT symbol, sample, n, n_stress, n_not_stress, mean_calm, mean_mid, mean_stress, vol_ratio, t_stress, eta2, compound_all, compound_filter FROM hmm_regime_etf`); err != nil {
		t.Fatal(err)
	}
	out := map[string]resultRow{}
	for _, r := range rows {
		out[r.Symbol+"/"+r.Sample] = r
	}
	return out
}

// referenceByRegime recomputes the study's statistics in Go from the saved model and the closes:
// regimes ranked by fitted volatility, the regime at the prior close, the same sessions.
func referenceByRegime(t *testing.T, w etfWorld, sym string) (n [3]int, mean [3]float64, compAll, compFilter float64, tStress float64, growth [3]float64) {
	t.Helper()
	db, err := storage.OpenSQLiteReadOnly(w.model)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var params []struct {
		State int     `db:"state"`
		Vol   float64 `db:"mu_vol"`
	}
	if err := db.Select(&params, `SELECT state, mu_vol FROM hmm_params WHERE symbol = 'QQQ'`); err != nil {
		t.Fatal(err)
	}
	sort.Slice(params, func(i, j int) bool { return params[i].Vol < params[j].Vol })
	rank := map[int]int{}
	for i, p := range params {
		rank[p.State] = i
	}
	var preds []struct {
		Date   string `db:"date"`
		Regime int    `db:"regime"`
	}
	if err := db.Select(&preds, `SELECT date, regime FROM hmm_prediction WHERE symbol = 'QQQ' ORDER BY date`); err != nil {
		t.Fatal(err)
	}
	reg := map[string]int{}
	for _, p := range preds {
		reg[p.Date] = rank[p.Regime]
	}

	var sum, lnSum [3]float64
	lnAll, lnNot := 0.0, 0.0
	var ns, ss struct{ n, sum, sq float64 }
	cs := w.closes[sym]
	for i := 1; i < len(cs); i++ {
		r, ok := reg[w.dates[i-1]]
		if !ok {
			continue
		}
		ret := cs[i]/cs[i-1] - 1
		if cs[i]/cs[i-1] > 1.8 || cs[i]/cs[i-1] < 1/1.8 {
			continue // a split
		}
		n[r]++
		sum[r] += ret
		lnSum[r] += math.Log(1 + ret)
		lnAll += math.Log(1 + ret)
		if r == 2 {
			ss.n++
			ss.sum += ret
			ss.sq += ret * ret
		} else {
			ns.n++
			ns.sum += ret
			ns.sq += ret * ret
			lnNot += math.Log(1 + ret)
		}
	}
	for r := 0; r < 3; r++ {
		mean[r] = sum[r] / float64(n[r])
		growth[r] = lnSum[r] / float64(n[r])
	}
	varOf := func(g struct{ n, sum, sq float64 }) float64 {
		m := g.sum / g.n
		return (g.sq/g.n - m*m) * g.n / (g.n - 1)
	}
	tStress = (ns.sum/ns.n - ss.sum/ss.n) / math.Sqrt(varOf(ns)/ns.n+varOf(ss)/ss.n)
	return n, mean, math.Exp(lnAll) - 1, math.Exp(lnNot) - 1, tStress, growth
}

func TestHMMRegimeETFMatchesAGoReference(t *testing.T) {
	if _, ok := study.Get("hmm_regime_etf"); !ok {
		t.Fatal("hmm_regime_etf is not registered")
	}
	w := newETFWorld(t)
	if err := w.run(t, 20); err != nil {
		t.Fatal(err)
	}
	got := readResults(t, w.results)
	if _, ok := got["STK/all"]; ok {
		t.Error("a stock entered the ETF study")
	}
	for _, sym := range []string{"LEV", "FLAT", "SPLIT"} {
		r, ok := got[sym+"/all"]
		if !ok {
			t.Fatalf("no row for %s", sym)
		}
		n, mean, compAll, compFilter, tStress, _ := referenceByRegime(t, w, sym)
		if r.N != n[0]+n[1]+n[2] || r.NStress != n[2] || r.NNot != n[0]+n[1] {
			t.Errorf("%s: sessions %d/%d/%d, reference %v", sym, r.N, r.NStress, r.NNot, n)
		}
		for i, pair := range [][2]float64{{r.MeanCalm, mean[0]}, {r.MeanMid, mean[1]}, {r.MeanStress, mean[2]}, {r.CompAll, compAll}, {r.CompFilter, compFilter}, {r.TStress, tStress}} {
			if math.Abs(pair[0]-pair[1]) > 1e-9*math.Max(1, math.Abs(pair[1])) {
				t.Errorf("%s value %d: study %v, reference %v", sym, i, pair[0], pair[1])
			}
		}
	}
	// A 3x ETF of QQQ moves several times more after a stressed close, and the regime explains more of it.
	lev, flat := got["LEV/all"], got["FLAT/all"]
	if lev.VolRatio < 1.5 {
		t.Errorf("LEV volatility ratio %v; a 3x of QQQ should be far more volatile after a stressed close", lev.VolRatio)
	}
	if math.Abs(flat.VolRatio-1) > 0.35 {
		t.Errorf("FLAT volatility ratio %v; an unrelated series should be near 1", flat.VolRatio)
	}
	for _, r := range []resultRow{lev, flat} {
		if r.Eta2 < 0 || r.Eta2 > 0.2 {
			t.Errorf("%s eta2 %v is not a small share of the variance", r.Symbol, r.Eta2)
		}
	}
	// The in-sample and held-out samples split the whole one.
	if in, oos := got["LEV/in"], got["LEV/oos"]; in.N+oos.N != lev.N || oos.N == 0 {
		t.Errorf("samples: in %d + oos %d vs all %d", in.N, oos.N, lev.N)
	}
}

func TestHMMRegimeETFDropsSplitsAndHourlyRows(t *testing.T) {
	w := newETFWorld(t)
	if err := w.run(t, 20); err != nil {
		t.Fatal(err)
	}
	db, err := sqlx.Open("sqlite", w.results)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var dropped []string
	if err := db.Select(&dropped, `SELECT symbol || ' ' || date FROM etf_dropped ORDER BY 1`); err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 1 || dropped[0] != "SPLIT "+w.dates[800] {
		t.Fatalf("dropped sessions %v, want only SPLIT on %s", dropped, w.dates[800])
	}
	var hourly int
	if err := db.Get(&hourly, `SELECT COUNT(*) FROM etf_ret WHERE date NOT LIKE '____-__-__'`); err != nil || hourly != 0 {
		t.Fatalf("hourly rows in etf_ret: %d (%v)", hourly, err)
	}
}

func TestHMMRegimeETFRefusesAMissingOrWrongModel(t *testing.T) {
	w := newETFWorld(t)

	missing := w
	missing.model = filepath.Join(t.TempDir(), "none.db")
	if err := missing.run(t, 20); err == nil {
		t.Fatal("a missing model database was accepted")
	}

	wrong := &HMMRegimeETF{HMMDB: w.model, UniverseDB: w.universe}
	wrong.SetDatabases(w.market, w.results)
	wrong.SetSymbol("SPY")
	if err := wrong.Run(); err == nil || !strings.Contains(err.Error(), "train hmm") {
		t.Fatalf("a symbol with no model: %v", err)
	}

	// A four-state model has no calm / middle / stressed ranking.
	dir := t.TempDir()
	four := filepath.Join(dir, "hmm_models.db")
	barsDB := filepath.Join(dir, "market_history_daily.db")
	db, err := dailybars.Open(barsDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := dailybars.Replace(db, "QQQ", qqqBars(1600)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := train.TrainHMM(context.Background(), train.HMMConfig{BarsDB: barsDB, ModelDB: four, Symbols: []string{"QQQ"}, States: 4, MinBars: 500, Seed: 1, Out: &strings.Builder{}}); err != nil {
		t.Skipf("a four-state model did not fit on this series: %v", err)
	}
	w4 := w
	w4.model = four
	if err := w4.run(t, 20); err == nil || !strings.Contains(err.Error(), "4 states") {
		t.Fatalf("a four-state model: %v", err)
	}
}

// The top tables rank the ETFs by the mean log return after a close in each regime. The Go
// reference computes that mean for each ETF on its own and the best of them must be rank 1.
func TestHMMRegimeTopRanksTheBestETFPerRegime(t *testing.T) {
	w := newETFWorld(t)
	if err := w.run(t, 20); err != nil {
		t.Fatal(err)
	}
	var want [3]struct {
		sym string
		g   float64
	}
	for r := range want {
		want[r].g = math.Inf(-1)
	}
	for _, sym := range []string{"LEV", "FLAT", "SPLIT"} {
		_, _, _, _, _, growth := referenceByRegime(t, w, sym)
		for r := 0; r < 3; r++ {
			if growth[r] > want[r].g {
				want[r].sym, want[r].g = sym, growth[r]
			}
		}
	}
	db, err := storage.OpenSQLiteReadOnly(w.results)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for r := 0; r < 3; r++ {
		var top struct {
			Symbol string  `db:"symbol"`
			Growth float64 `db:"growth_all"`
		}
		if err := db.Get(&top, `SELECT symbol, growth_all FROM hmm_regime_top WHERE regime = ? AND rank_all = 1`, r); err != nil {
			t.Fatalf("regime %d: %v", r, err)
		}
		if top.Symbol != want[r].sym || math.Abs(top.Growth-want[r].g) > 1e-9 {
			t.Errorf("regime %d: top %s %v, reference %s %v", r, top.Symbol, top.Growth, want[r].sym, want[r].g)
		}
		var rows int
		if err := db.Get(&rows, `SELECT COUNT(*) FROM hmm_regime_top WHERE regime = ?`, r); err != nil || rows != 2 {
			t.Errorf("regime %d keeps %d rows, want TopN of 2 (%v)", r, rows, err)
		}
	}
	var stocks int
	if err := db.Get(&stocks, `SELECT COUNT(*) FROM hmm_regime_top WHERE symbol = 'STK'`); err != nil || stocks != 0 {
		t.Errorf("a stock entered the top table (%v)", err)
	}
	var picks int
	if err := db.Get(&picks, `SELECT COUNT(*) FROM hmm_regime_pick WHERE rank_oos IS NOT NULL AND (rank_oos_pct <= 0 OR rank_oos_pct > 100)`); err != nil || picks != 0 {
		t.Errorf("held-out rank share outside (0,100]: %d (%v)", picks, err)
	}
}
