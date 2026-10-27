package train

import (
	"bytes"
	"context"
	"math"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/dailybars"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// synthBars draws n weekday bars of three sticky regimes (bear: falling and volatile,
// sideways, bull: rising and calm) as adjusted closes starting 2010-01-04.
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

func writeBars(t *testing.T, path, symbol string, bars []models.Bar) {
	t.Helper()
	db, err := dailybars.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := dailybars.Replace(db, symbol, bars); err != nil {
		t.Fatal(err)
	}
}

type hmmFiles struct{ bars, model string }

func newHMMFiles(t *testing.T) hmmFiles {
	d := t.TempDir()
	return hmmFiles{bars: filepath.Join(d, "daily_bars.db"), model: filepath.Join(d, "hmm_models.db")}
}

func (h hmmFiles) cfg(through string) HMMConfig {
	return HMMConfig{BarsDB: h.bars, ModelDB: h.model, Symbols: []string{"TST"}, Through: through, MinBars: 500, Seed: 1}
}

// The SQL stage must give the log return of the adjusted close and the sample standard
// deviation of the 20 most recent returns, and nothing before a full window exists.
func TestHMMInputsMatchAReferenceCalculation(t *testing.T) {
	f := newHMMFiles(t)
	bars := synthBars(120, 3)
	writeBars(t, f.bars, "TST", bars)
	dates, obs, err := hmmObs(context.Background(), f.bars, "", "TST")
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != len(bars)-20 {
		t.Fatalf("%d observations from %d bars; want %d (one lost to the first return, 19 more to the window)", len(obs), len(bars), len(bars)-20)
	}
	rets := make([]float64, len(bars))
	for i := 1; i < len(bars); i++ {
		rets[i] = math.Log(bars[i].AdjClose / bars[i-1].AdjClose)
	}
	for k, d := range dates {
		i := k + 20 // first observation is the 21st bar
		if bars[i].Date != d {
			t.Fatalf("observation %d is %s, want %s", k, d, bars[i].Date)
		}
		window := rets[i-19 : i+1]
		mean := 0.0
		for _, r := range window {
			mean += r
		}
		mean /= 20
		ss := 0.0
		for _, r := range window {
			ss += (r - mean) * (r - mean)
		}
		wantVol := math.Sqrt(ss / 19)
		if math.Abs(obs[k][0]-rets[i]) > 1e-12 || math.Abs(obs[k][1]-wantVol) > 1e-9 {
			t.Fatalf("%s: got (%v, %v), want (%v, %v)", d, obs[k][0], obs[k][1], rets[i], wantVol)
		}
	}
}

func TestTrainHMMSavesSortedModelAndFilteredPredictions(t *testing.T) {
	f := newHMMFiles(t)
	bars := synthBars(2200, 5)
	writeBars(t, f.bars, "TST", bars)
	through := bars[1700].Date

	res, err := TrainHMM(context.Background(), f.cfg(through))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].Saved || res[0].TrainedThrough != through {
		t.Fatalf("result %+v", res)
	}
	db, err := storage.OpenSQLiteReadOnly(f.model)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var mus []float64
	if err := db.Select(&mus, "SELECT mu_ret FROM hmm_params WHERE symbol = 'TST' ORDER BY state"); err != nil || len(mus) != 3 {
		t.Fatalf("params: %v %v", mus, err)
	}
	if !(mus[0] < mus[1] && mus[1] < mus[2]) {
		t.Fatalf("states not sorted by mean return: %v", mus)
	}
	var n, in, oos int
	db.Get(&n, "SELECT COUNT(*) FROM hmm_prediction WHERE symbol = 'TST'")
	db.Get(&in, "SELECT COUNT(*) FROM hmm_prediction WHERE symbol = 'TST' AND in_sample = 1 AND date <= ?", through)
	db.Get(&oos, "SELECT COUNT(*) FROM hmm_prediction WHERE symbol = 'TST' AND in_sample = 0 AND date > ?", through)
	if n != len(bars)-20 || in+oos != n || oos == 0 {
		t.Fatalf("prediction rows %d (in %d, oos %d) for %d bars", n, in, oos, len(bars))
	}
	var bad int
	db.Get(&bad, `SELECT COUNT(*) FROM (SELECT date, SUM(prob) s, SUM(next_prob) ns FROM hmm_state_prob WHERE symbol = 'TST' GROUP BY date) WHERE abs(s - 1) > 1e-9 OR abs(ns - 1) > 1e-9`)
	if bad != 0 {
		t.Fatalf("%d dates whose state probabilities do not sum to 1", bad)
	}

	latest, err := ReadHMMLatest(f.model, "TST")
	if err != nil {
		t.Fatal(err)
	}
	if latest.Date != bars[len(bars)-1].Date || len(latest.Probs) != 3 || len(latest.NextProbs) != 3 || latest.TrainedThrough != through {
		t.Fatalf("latest %+v", latest)
	}
}

// What the model learns must not depend on bars after Through.
func TestTrainHMMNeverSeesTheHeldOutBars(t *testing.T) {
	bars := synthBars(2200, 9)
	through := bars[1700].Date
	fit := func(n int) (float64, float64) {
		f := newHMMFiles(t)
		writeBars(t, f.bars, "TST", bars[:n])
		if _, err := TrainHMM(context.Background(), f.cfg(through)); err != nil {
			t.Fatal(err)
		}
		db, err := storage.OpenSQLiteReadOnly(f.model)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var m, ll float64
		db.Get(&m, "SELECT mu_ret FROM hmm_params WHERE symbol = 'TST' AND state = 0")
		db.Get(&ll, "SELECT loglik FROM hmm_model_meta WHERE symbol = 'TST'")
		return m, ll
	}
	m1, ll1 := fit(1800)
	m2, ll2 := fit(2200)
	if m1 != m2 || ll1 != ll2 {
		t.Fatalf("held-out bars changed the fit: mu %v vs %v, loglik %v vs %v", m1, m2, ll1, ll2)
	}
}

// Extending a saved model with new sessions must give exactly the rows a full train with the
// same cutoff gives, because parameters stay frozen and the filter is causal.
func TestExtendHMMEqualsAFullTrainWithTheSameCutoff(t *testing.T) {
	bars := synthBars(2200, 11)
	through := bars[1700].Date

	ext := newHMMFiles(t)
	writeBars(t, ext.bars, "TST", bars[:1900])
	if _, err := TrainHMM(context.Background(), ext.cfg(through)); err != nil {
		t.Fatal(err)
	}
	writeBars(t, ext.bars, "TST", bars) // 300 new sessions arrive
	res, err := ExtendHMM(context.Background(), ExtendHMMConfig{BarsDB: ext.bars, ModelDB: ext.model, Symbols: []string{"TST"}})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].NewRows != 300 || res[0].Latest.Date != bars[len(bars)-1].Date {
		t.Fatalf("extend result %+v", res[0])
	}
	again, err := ExtendHMM(context.Background(), ExtendHMMConfig{BarsDB: ext.bars, ModelDB: ext.model, Symbols: []string{"TST"}})
	if err != nil || again[0].NewRows != 0 {
		t.Fatalf("a second extend added %+v, err %v", again, err)
	}

	full := newHMMFiles(t)
	writeBars(t, full.bars, "TST", bars)
	if _, err := TrainHMM(context.Background(), full.cfg(through)); err != nil {
		t.Fatal(err)
	}

	q := "SELECT date, regime, regime_prob, next_regime, next_regime_prob, in_sample FROM hmm_prediction WHERE symbol = 'TST' ORDER BY date"
	type row struct {
		Date     string  `db:"date"`
		Regime   int     `db:"regime"`
		RP       float64 `db:"regime_prob"`
		Next     int     `db:"next_regime"`
		NP       float64 `db:"next_regime_prob"`
		InSample int     `db:"in_sample"`
	}
	get := func(path string) []row {
		db, err := storage.OpenSQLiteReadOnly(path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var rows []row
		if err := db.Select(&rows, q); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	a, b := get(ext.model), get(full.model)
	if len(a) != len(b) || len(a) != len(bars)-20 {
		t.Fatalf("%d extended rows vs %d full rows", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("row %d differs: extended %+v, full %+v", i, a[i], b[i])
		}
	}
	var last string
	storage.OpenSQLite(ext.model)
	db, _ := storage.OpenSQLiteReadOnly(ext.model)
	defer db.Close()
	db.Get(&last, "SELECT last_date FROM hmm_model_meta WHERE symbol = 'TST'")
	if last != bars[len(bars)-1].Date {
		t.Fatalf("meta last_date %s", last)
	}
}

func TestExtendHMMDryRunWritesNothing(t *testing.T) {
	bars := synthBars(2200, 13)
	f := newHMMFiles(t)
	writeBars(t, f.bars, "TST", bars[:1900])
	if _, err := TrainHMM(context.Background(), f.cfg(bars[1700].Date)); err != nil {
		t.Fatal(err)
	}
	writeBars(t, f.bars, "TST", bars)
	res, err := ExtendHMM(context.Background(), ExtendHMMConfig{BarsDB: f.bars, ModelDB: f.model, Symbols: []string{"TST"}, DryRun: true})
	if err != nil || res[0].NewRows != 300 {
		t.Fatalf("%+v %v", res, err)
	}
	latest, err := ReadHMMLatest(f.model, "TST")
	if err != nil || latest.Date != bars[1899].Date {
		t.Fatalf("dry run changed the saved model: %+v %v", latest, err)
	}
}

func TestExtendHMMRefusesAMissingModel(t *testing.T) {
	f := newHMMFiles(t)
	writeBars(t, f.bars, "TST", synthBars(300, 1))
	// a model database that exists but holds no model for TST
	if _, err := TrainHMM(context.Background(), HMMConfig{BarsDB: f.bars, ModelDB: f.model, Symbols: []string{"TST"}, DryRun: true, Through: "2011-01-03", MinBars: 100}); err != nil {
		t.Fatal(err)
	}
	db, _ := storage.OpenSQLite(f.model)
	storage.RunStage(db, "hmm_model_schema", nil)
	db.Close()
	if _, err := ExtendHMM(context.Background(), ExtendHMMConfig{BarsDB: f.bars, ModelDB: f.model, Symbols: []string{"TST"}}); err == nil || !strings.Contains(err.Error(), "train hmm") {
		t.Fatalf("got %v; want an error telling the user to run train hmm", err)
	}
}

func TestTrainHMMRefusesThinHistoryAndBadDates(t *testing.T) {
	f := newHMMFiles(t)
	writeBars(t, f.bars, "TST", synthBars(600, 2))
	c := f.cfg(synthBars(600, 2)[400].Date)
	c.MinBars = 1000
	if _, err := TrainHMM(context.Background(), c); err == nil || !strings.Contains(err.Error(), "minimum") {
		t.Fatalf("got %v; want the minimum-bars error", err)
	}
	c.Through = "2011/01/01"
	if _, err := TrainHMM(context.Background(), c); err == nil {
		t.Fatal("a malformed -through should be an error")
	}
	missing := HMMConfig{BarsDB: filepath.Join(t.TempDir(), "none.db"), ModelDB: f.model}
	if _, err := TrainHMM(context.Background(), missing); err == nil || !strings.Contains(err.Error(), "daily_bars") {
		t.Fatalf("got %v; want an error pointing at daily_bars", err)
	}
}

func TestTrainHMMDryRunSavesNothing(t *testing.T) {
	bars := synthBars(2200, 4)
	f := newHMMFiles(t)
	writeBars(t, f.bars, "TST", bars)
	c := f.cfg(bars[1700].Date)
	c.DryRun = true
	res, err := TrainHMM(context.Background(), c)
	if err != nil || res[0].Saved {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := ReadHMMLatest(f.model, "TST"); err == nil {
		t.Fatal("a dry run created a model")
	}
}

func TestTrainHMMDefaultHoldoutIsTwelveMonths(t *testing.T) {
	bars := synthBars(2200, 6)
	f := newHMMFiles(t)
	writeBars(t, f.bars, "TST", bars)
	c := f.cfg("")
	res, err := TrainHMM(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	last, _ := time.Parse("2006-01-02", bars[len(bars)-1].Date)
	cut := last.AddDate(0, -12, 0).Format("2006-01-02")
	if res[0].TrainedThrough > cut || res[0].TrainedThrough < last.AddDate(0, -12, -7).Format("2006-01-02") {
		t.Fatalf("trained through %s; want the last session on or before %s", res[0].TrainedThrough, cut)
	}
}

func TestCompareAndReportHMM(t *testing.T) {
	bars := synthBars(2200, 8)
	f := newHMMFiles(t)
	writeBars(t, f.bars, "TST", bars)
	c := f.cfg(bars[1700].Date)
	cmp, err := CompareHMM(context.Background(), c, []int{2, 3})
	if err != nil || len(cmp) != 2 || cmp[0].States != 2 || cmp[1].States != 3 {
		t.Fatalf("%+v %v", cmp, err)
	}
	if _, err := ReadHMMLatest(f.model, "TST"); err == nil {
		t.Fatal("compare wrote a model")
	}

	if _, err := TrainHMM(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := ReportHMM(context.Background(), HMMReportConfig{ModelDB: f.model, Symbols: []string{"TST"}}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"TST HMM: 3 states", "in-sample", "out-of-sample", "fitted regimes", "mean log-likelihood per bar, out-of-sample"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("report lacks %q:\n%s", want, out.String())
		}
	}
}

func TestRunHMMCommandAndFamilyRegistration(t *testing.T) {
	bars := synthBars(2200, 10)
	f := newHMMFiles(t)
	writeBars(t, f.bars, "TST", bars)
	var out, errb bytes.Buffer
	args := []string{"hmm", "-bars-db", f.bars, "-model-db", f.model, "-symbols", "TST", "-through", bars[1700].Date, "-min-bars", "500", "-dry-run"}
	if code := Run(args, &out, &errb); code != 0 || !strings.Contains(out.String(), "dry run: TST not saved") {
		t.Fatalf("code %d\nout: %s\nerr: %s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := Run([]string{"hmm", "bogus"}, &out, &errb); code != 2 {
		t.Fatalf("unknown subcommand returned %d", code)
	}
}

func TestExportHMMWritesAnIndependentCopy(t *testing.T) {
	bars := synthBars(2200, 14)
	f := newHMMFiles(t)
	writeBars(t, f.bars, "TST", bars)
	if _, err := TrainHMM(context.Background(), f.cfg(bars[1700].Date)); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out", "hmm_models.db")
	if err := ExportHMM(context.Background(), f.model, dst); err != nil {
		t.Fatal(err)
	}
	if err := ExportHMM(context.Background(), f.model, dst); err != nil { // replaces
		t.Fatalf("second export: %v", err)
	}
	a, err := ReadHMMLatest(f.model, "TST")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ReadHMMLatest(dst, "TST")
	if err != nil || a.Date != b.Date || a.Regime != b.Regime || a.NextProbs[0] != b.NextProbs[0] {
		t.Fatalf("copy differs: %+v vs %+v (%v)", a, b, err)
	}
	if err := ExportHMM(context.Background(), filepath.Join(t.TempDir(), "none.db"), dst); err == nil {
		t.Fatal("exporting a missing model should be an error")
	}
}
