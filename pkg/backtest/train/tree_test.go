package train

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/realbars"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/tree_strategy"
)

// bucket is the 5 class label written out from the definition, as the oracle.
func bucket(nextReturnPct float64) int {
	switch {
	case nextReturnPct <= -5.0:
		return -2
	case nextReturnPct < -1.0:
		return -1
	case nextReturnPct <= 1.0:
		return 0
	case nextReturnPct < 5.0:
		return 1
	default:
		return 2
	}
}

// On real MARA bars the class column must be the bucket of the real next-bar
// return, and the last bar must be left unlabelled but still given features.
func TestFeatureClassesOnRealBars(t *testing.T) {
	market := realbars.Copy(t, "MARA")
	closes := realbars.Closes(t, market, "MARA")
	dates := realbars.Dates(t, market, "MARA")
	calc, err := strategy.OpenCalcDB(market, filepath.Join(t.TempDir(), "calc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer calc.Close()
	samples, err := loadTreeSamples(context.Background(), calc, "MARA")
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != len(closes)-200 {
		t.Fatalf("%d feature rows for %d bars (the first 200 are warm-up)", len(samples), len(closes))
	}
	seen := map[int]int{}
	for k, s := range samples {
		i := 200 + k
		if s.Date != dates[i] {
			t.Fatalf("row %d is %s, want %s", k, s.Date, dates[i])
		}
		if i == len(closes)-1 {
			if s.Class != nil {
				t.Fatalf("the last bar %s must have no class, got %d", s.Date, *s.Class)
			}
			continue
		}
		want := bucket((closes[i+1] - closes[i]) / closes[i] * 100)
		if s.Class == nil || *s.Class != want {
			t.Fatalf("%s: class %v, want %d (next return %.4f%%)", s.Date, s.Class, want, (closes[i+1]-closes[i])/closes[i]*100)
		}
		seen[want]++
	}
	if len(seen) != 5 {
		t.Fatalf("real MARA history should reach every class, got %v", seen)
	}
}

// Closes chosen 0.01 percentage points either side of each bucket edge land on the right side.
func TestClassBucketEdges(t *testing.T) {
	market := filepath.Join(t.TempDir(), "market.db")
	db, err := storage.OpenSQLite(market)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE backtest_start (idx INTEGER, symbol TEXT, Date TEXT, timeframe TEXT, open REAL, high REAL, low REAL, close REAL, volume INTEGER, "Adj Close" REAL)`); err != nil {
		t.Fatal(err)
	}
	// 205 flat warm-up bars, then one bar per edge case, each bar's next return is the case.
	cases := []float64{-5.01, -4.99, -1.01, -0.99, 0.99, 1.01, 4.99, 5.01}
	want := []int{-2, -1, -1, 0, 0, 1, 1, 2}
	closes := make([]float64, 0, 205+len(cases)+1)
	for i := 0; i < 205; i++ {
		closes = append(closes, 100)
	}
	p := 100.0
	for _, r := range cases {
		p = p * (1 + r/100)
		closes = append(closes, p)
	}
	// the bar before each case ends at the previous close, so bar k's next return is cases[k]
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, c := range closes {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		if _, err := db.Exec(`INSERT INTO backtest_start VALUES (?, 'EDGE', ?, '1d', ?, ?, ?, ?, 1000, ?)`, i, d, c, c, c, c, c); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	calc, err := strategy.OpenCalcDB(market, filepath.Join(t.TempDir(), "calc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer calc.Close()
	samples, err := loadTreeSamples(context.Background(), calc, "EDGE")
	if err != nil {
		t.Fatal(err)
	}
	// rows from the first edge case's bar: that bar is closes[204], whose next close is the first case
	var got []int
	for _, s := range samples {
		if s.Date >= start.AddDate(0, 0, 204).Format("2006-01-02") && s.Class != nil {
			got = append(got, *s.Class)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("classes %v, want %v", got, want)
	}
}

func TestTrainTreePersistsTreeThatBacktestWalksInSQL(t *testing.T) {
	app := t.TempDir()
	t.Setenv("APP_FOLDER", app)
	short := realbars.ShortSymbol(t, 5, 150)
	market := realbars.Copy(t, "MARA", "GOOGL", short)
	modelPath := filepath.Join(app, "data", "tree_models.db")

	// The reference answer: fit the tree on the same features and take CloudForest's own votes.
	calc, err := strategy.OpenCalcDB(market, filepath.Join(t.TempDir(), "ref.db"))
	if err != nil {
		t.Fatal(err)
	}
	samples, err := loadTreeSamples(context.Background(), calc, "MARA")
	calc.Close()
	if err != nil {
		t.Fatal(err)
	}
	want, err := fitTree(samples)
	if err != nil {
		t.Fatal(err)
	}
	if len(want.Nodes) < 3 {
		t.Fatalf("a trivial tree: %d nodes", len(want.Nodes))
	}
	classes := map[string]bool{}
	for _, c := range want.Pred {
		classes[c] = true
	}
	var buyDates []string
	for d, c := range want.Pred {
		if c == "2" {
			buyDates = append(buyDates, d)
		}
	}
	sort.Strings(buyDates)
	if len(buyDates) == 0 {
		t.Fatalf("the MARA tree never predicts class 2 (predicts %v)", classes)
	}

	res, err := TrainTree(context.Background(), TreeConfig{MarketDB: market, ModelDB: modelPath, Symbols: []string{"mara", short, "GOOGL"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Requested != 3 || len(res.Trained) < 1 || res.Skipped[short] == "" {
		t.Fatalf("result %+v", res)
	}

	// A row for MARA trading MARA: its signals are the dates the tree predicts class 2.
	refdb.DefaultPath = filepath.Join(t.TempDir(), "unused.db")
	row := refdb.TreeStrategy{
		ID: "tree_mara_test", Name: "MARA tree", SignalSymbol: "MARA", TradeSymbol: "MARA", Direction: "LONG",
		HoldDays: 5, TakeProfitPct: 0.05, StopLossPct: 0.08, AllocationPct: 0.5,
	}
	s := &tree_strategy.Strategy{Row: row}
	s.SetDatabases(market, filepath.Join(t.TempDir(), "calc.db"))
	var got []string
	for _, sig := range s.GenerateSignals(nil) {
		got = append(got, sig.Date)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, buyDates) {
		t.Fatalf("SQL walk gave %d class 2 dates, CloudForest's votes gave %d\nsql: %v\ncf:  %v", len(got), len(buyDates), head(got), head(buyDates))
	}

	// The model records what it was trained on, from the real history.
	mdb, err := storage.OpenSQLiteReadOnly(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	defer mdb.Close()
	var meta struct {
		Samples int    `db:"samples"`
		Grown   int    `db:"grown_cases"`
		Counts  string `db:"class_counts"`
		Last    string `db:"last_date"`
	}
	if err := mdb.Get(&meta, "SELECT samples, grown_cases, class_counts, last_date FROM tree_model_meta WHERE symbol = 'MARA'"); err != nil {
		t.Fatal(err)
	}
	var counts map[string]int
	if err := json.Unmarshal([]byte(meta.Counts), &counts); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	dates := realbars.Dates(t, market, "MARA")
	if meta.Samples != total || meta.Samples != len(dates)-201 || meta.Last != dates[len(dates)-2] || meta.Grown > meta.Samples {
		t.Fatalf("meta %+v counts %v (MARA has %d bars, last bar %s)", meta, counts, len(dates), dates[len(dates)-1])
	}
	if len(counts) != 5 {
		t.Fatalf("MARA should have all 5 classes, got %v", counts)
	}

	// Retraining replaces rather than duplicates.
	if _, err := TrainTree(context.Background(), TreeConfig{MarketDB: market, ModelDB: modelPath, Symbols: []string{"MARA"}}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := mdb.Get(&n, "SELECT COUNT(*) FROM tree_node WHERE symbol = 'MARA'"); err != nil || n != len(want.Nodes) {
		t.Fatalf("tree_node rows %d, want %d (err %v)", n, len(want.Nodes), err)
	}
}

func TestDownsampleNeutralCapsTheDominantClass(t *testing.T) {
	cls := func(c int) *int { return &c }
	var samples []treeSample
	// 400 neutral bars, 30 class 2, 20 class -2, 10 class 1: the cap is the largest non-neutral class, 30.
	for i := 0; i < 400; i++ {
		samples = append(samples, treeSample{Date: fmt.Sprintf("d%04d", len(samples)), Class: cls(0)})
	}
	for _, c := range []struct{ class, n int }{{2, 30}, {-2, 20}, {1, 10}} {
		for i := 0; i < c.n; i++ {
			samples = append(samples, treeSample{Date: fmt.Sprintf("d%04d", len(samples)), Class: cls(c.class)})
		}
	}
	samples = append(samples, treeSample{Date: "last"}) // unlabelled
	counts := map[int]int{}
	for _, i := range downsampleNeutral(samples) {
		if samples[i].Class == nil {
			t.Fatal("an unlabelled bar was grown on")
		}
		counts[*samples[i].Class]++
	}
	if counts[0] != 30 || counts[2] != 30 || counts[-2] != 20 || counts[1] != 10 {
		t.Fatalf("grown counts %v, want neutral capped at 30 and the others untouched", counts)
	}
}

func head(s []string) []string {
	if len(s) > 5 {
		return s[:5]
	}
	return s
}

// With -through, the tree is fit only on bars up to that date.
func TestTrainTreeThrough(t *testing.T) {
	market := realbars.Copy(t, "MARA")
	modelPath := filepath.Join(t.TempDir(), "tree.db")
	if _, err := TrainTree(context.Background(), TreeConfig{MarketDB: market, ModelDB: modelPath, Symbols: []string{"MARA"}, Through: "2024-06-28"}); err != nil {
		t.Fatal(err)
	}
	db, err := storage.OpenSQLiteReadOnly(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var meta struct {
		Samples int    `db:"samples"`
		Last    string `db:"last_date"`
	}
	if err := db.Get(&meta, "SELECT samples, last_date FROM tree_model_meta WHERE symbol = 'MARA'"); err != nil {
		t.Fatal(err)
	}
	dates := realbars.Dates(t, market, "MARA")
	want := 0
	for i, d := range dates {
		if i >= 200 && d <= "2024-06-28" && i < len(dates)-1 {
			want++
		}
	}
	if meta.Last > "2024-06-28" || meta.Samples != want {
		t.Fatalf("trained through %s on %d bars, want through 2024-06-28 on %d", meta.Last, meta.Samples, want)
	}
}
