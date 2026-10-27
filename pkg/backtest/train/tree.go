package train

// The tree family: trains one depth-3 CloudForest decision tree per signal symbol
// and persists it in SQLite (appenv.TreeDB). Backtests walk the saved tree in SQL
// (sql/strategies/tree_strategy); they never fit one.
//
// The features and the label are calculated by SQL (sql/stages/tree_features).
// The label is the next bar's return in 5 buckets:
//
//	class -2  at or below -5 percent     severe drop (the downside tail)
//	class -1  above -5 and below -1      moderate drop
//	class  0  from -1 to 1 inclusive     neutral, the dense centre
//	class  1  above 1 and below 5        moderate gain
//	class  2  at or above 5 percent      severe gain (the target)
//
// Growing the tree is CloudForest's recursive control flow, which stays in Go.
// The neutral class usually dominates, so the tree is grown on every bar of the
// four other classes plus an evenly spaced sample of neutral bars no larger than
// the biggest of those four classes. Classes are weighted to balance the grown
// set. A symbol needs MinSamples labelled bars and MinTarget bars of class 2.
import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/jmoiron/sqlx"
	"github.com/ryanbressler/CloudForest"
)

const (
	// MinSamples is the fewest labelled bars a tree is fit on.
	MinSamples = 250
	// MinTarget is the fewest class 2 bars (the next bar up 5 percent or more) a
	// tree is fit on: without them it has nothing to learn the target from.
	MinTarget = 10
)

// classLabels are the CloudForest category names of the 5 buckets, in order.
var classLabels = []string{"-2", "-1", "0", "1", "2"}

// TreeConfig holds every setting of a `train tree` run.
type TreeConfig struct {
	MarketDB string   // bars to train on; empty = appenv.MarketDB()
	ModelDB  string   // persisted trees, created if missing; empty = appenv.TreeDB()
	RefDB    string   // strategies DB naming the symbols when Symbols is empty; empty = appenv.RefDB()
	Symbols  []string // symbols to train; empty = every signal_symbol in tree_strategy
	Through  string   // train only on bars up to and including this date (YYYY-MM-DD); empty = all history
	CalcDir  string   // keep the feature slice tables of the last symbol here; empty = scratch, removed
	DryRun   bool     // only apply the minimum-data rules: nothing is fitted or saved (Trained lists who would get a tree)
	Out      io.Writer
}

// DefaultTreeConfig returns the defaults of `train tree`.
func DefaultTreeConfig() TreeConfig {
	return TreeConfig{MarketDB: appenv.MarketDB(), ModelDB: appenv.TreeDB(), RefDB: appenv.RefDB()}
}

// TreeResult reports what a run trained.
type TreeResult struct {
	Requested     int
	Trained       []string
	Skipped       map[string]string // symbol -> why no tree was fit
	MarketThrough string            // latest bar date any trained symbol was trained through
}

// treeSample is one row of decision_tree_features_slice.
type treeSample struct {
	Date            string  `db:"date"`
	Class           *int    `db:"class"`
	Return1d        float64 `db:"return_1d"`
	Return3d        float64 `db:"return_3d"`
	Return5d        float64 `db:"return_5d"`
	Return10d       float64 `db:"return_10d"`
	RSI14           float64 `db:"rsi14"`
	PriceVsSMA20    float64 `db:"price_vs_sma20"`
	PriceVsSMA50    float64 `db:"price_vs_sma50"`
	PriceVsSMA200   float64 `db:"price_vs_sma200"`
	SMA20Vs50       float64 `db:"sma20_vs_50"`
	VolRatio20      float64 `db:"vol_ratio20"`
	RangeVsATR14    float64 `db:"range_vs_atr14"`
	CloseNearHigh   float64 `db:"close_near_high"`
	ConsecutiveDown float64 `db:"consecutive_down"`
}

// treeNode is one row of tree_node.
type treeNode struct {
	Path      string
	Feature   string  // empty on a leaf
	Threshold float64 // meaningful on an inner node
	Pred      string  // class label, on a leaf
}

// fittedTree is a fitted tree and what it was fit on.
type fittedTree struct {
	Nodes       []treeNode
	Pred        map[string]string // date -> predicted class, over every sample
	ClassCounts map[string]int    // labelled bars per class
	Samples     int               // labelled bars
	Grown       int               // bars the tree was grown on
	FirstDate   string
	LastDate    string
}

// downsampleNeutral returns the case indices to grow on: every labelled bar of
// the four non-neutral classes plus evenly spaced neutral bars, no more than the
// largest non-neutral class has.
func downsampleNeutral(samples []treeSample) []int {
	counts := map[int]int{}
	for _, s := range samples {
		if s.Class != nil {
			counts[*s.Class]++
		}
	}
	limit := 0
	for c, n := range counts {
		if c != 0 && n > limit {
			limit = n
		}
	}
	neutral := counts[0]
	var cases []int
	seen := 0
	kept := 0
	for i, s := range samples {
		if s.Class == nil {
			continue
		}
		if *s.Class != 0 {
			cases = append(cases, i)
			continue
		}
		// keep the neutral bar when it is the next one due on an even spacing
		seen++
		if neutral <= limit || kept < seen*limit/neutral {
			cases = append(cases, i)
			kept++
		}
	}
	sort.Ints(cases)
	return cases
}

// checkSamples applies the minimum-data rules a symbol must meet to get a tree.
func checkSamples(samples []treeSample) error {
	counts := map[string]int{}
	labelled := 0
	for _, s := range samples {
		if s.Class == nil {
			continue
		}
		counts[strconv.Itoa(*s.Class)]++
		labelled++
	}
	if labelled < MinSamples {
		return fmt.Errorf("insufficient labelled bars (%d, need >= %d)", labelled, MinSamples)
	}
	if counts["2"] < MinTarget {
		return fmt.Errorf("too few class 2 bars, next bar up 5%% or more (%d, need >= %d)", counts["2"], MinTarget)
	}
	return nil
}

// fitTree grows the tree for one symbol's samples.
func fitTree(samples []treeSample) (*fittedTree, error) {
	if err := checkSamples(samples); err != nil {
		return nil, err
	}
	counts := map[string]int{}
	labelled := 0
	var first, last string
	for _, s := range samples {
		if s.Class == nil {
			continue
		}
		counts[strconv.Itoa(*s.Class)]++
		labelled++
		if first == "" {
			first = s.Date
		}
		last = s.Date
	}

	n := len(samples)
	num := func(name string) *CloudForest.DenseNumFeature {
		return &CloudForest.DenseNumFeature{Name: name, Missing: make([]bool, n), NumData: make([]float64, n)}
	}
	f := []*CloudForest.DenseNumFeature{
		num("Return1d"), num("Return3d"), num("Return5d"), num("Return10d"), num("RSI14"),
		num("PriceVsSMA20"), num("PriceVsSMA50"), num("PriceVsSMA200"), num("SMA20Vs50"),
		num("VolRatio20"), num("RangeVsATR14"), num("CloseNearHigh"), num("ConsecutiveDown"),
	}
	catMap := map[string]int{}
	for i, l := range classLabels {
		catMap[l] = i
	}
	target := &CloudForest.DenseCatFeature{
		Name: "Target", Missing: make([]bool, n), CatData: make([]int, n),
		CatMap: &CloudForest.CatMap{Map: catMap, Back: classLabels},
	}
	for i, s := range samples {
		for k, v := range []float64{s.Return1d, s.Return3d, s.Return5d, s.Return10d, s.RSI14, s.PriceVsSMA20,
			s.PriceVsSMA50, s.PriceVsSMA200, s.SMA20Vs50, s.VolRatio20, s.RangeVsATR14, s.CloseNearHigh, s.ConsecutiveDown} {
			f[k].NumData[i] = v
		}
		if s.Class == nil {
			target.Missing[i] = true
			continue
		}
		target.CatData[i] = *s.Class + 2
	}

	cases := downsampleNeutral(samples)
	grownCounts := map[string]int{}
	for _, i := range cases {
		grownCounts[strconv.Itoa(*samples[i].Class)]++
	}
	// Balance the classes of the grown set: weight = cases / (classes * class cases).
	weights := map[string]float64{}
	for _, l := range classLabels {
		if c := grownCounts[l]; c > 0 {
			weights[l] = float64(len(cases)) / (float64(len(grownCounts)) * float64(c))
		} else {
			weights[l] = 1.0
		}
	}

	data := make([]CloudForest.Feature, 0, len(f)+1)
	fmap := map[string]int{}
	candidates := make([]int, len(f))
	for i, ft := range f {
		data = append(data, ft)
		fmap[ft.Name] = i
		candidates[i] = i
	}
	data = append(data, target)
	fmap["Target"] = len(f)
	fm := &CloudForest.FeatureMatrix{Data: data, Map: fmap}

	wrf := CloudForest.NewWRFTarget(target, weights)
	allocs := CloudForest.NewBestSplitAllocs(n, wrf)
	tree := CloudForest.NewTree()
	tree.Target = "Target"
	tree.Grow(fm, wrf, cases, candidates, nil, len(candidates), 15, 3, false, false, false, false, false, nil, nil, allocs)

	bb := CloudForest.NewCatBallotBox(n)
	tree.Vote(fm, bb)
	t := &fittedTree{
		Pred: map[string]string{}, ClassCounts: counts, Samples: labelled, Grown: len(cases),
		FirstDate: first, LastDate: last,
	}
	for i, s := range samples {
		t.Pred[s.Date] = bb.Tally(i)
	}
	var walk func(nd *CloudForest.Node, path string)
	walk = func(nd *CloudForest.Node, path string) {
		if nd.Left == nil && nd.Right == nil || nd.Splitter == nil {
			t.Nodes = append(t.Nodes, treeNode{Path: path, Pred: nd.Pred})
			return
		}
		t.Nodes = append(t.Nodes, treeNode{Path: path, Feature: nd.Splitter.Feature, Threshold: nd.Splitter.Value})
		walk(nd.Left, path+"L")
		walk(nd.Right, path+"R")
	}
	walk(tree.Root, "")
	sort.Slice(t.Nodes, func(i, j int) bool { return t.Nodes[i].Path < t.Nodes[j].Path })
	return t, nil
}

// loadTreeSamples builds the feature slice for sym in the calc database and reads it.
func loadTreeSamples(ctx context.Context, calc *sqlx.DB, sym string) ([]treeSample, error) {
	if !safeSymbol(sym) {
		return nil, fmt.Errorf("unsafe symbol %q", sym)
	}
	if err := storage.RunStage(calc, "tree_features", map[string]string{"__SYMBOL__": sym}); err != nil {
		return nil, err
	}
	var samples []treeSample
	err := calc.SelectContext(ctx, &samples, `SELECT date, class, return_1d, return_3d, return_5d, return_10d,
		rsi14, price_vs_sma20, price_vs_sma50, price_vs_sma200, sma20_vs_50, vol_ratio20, range_vs_atr14,
		close_near_high, consecutive_down FROM decision_tree_features_slice ORDER BY date`)
	return samples, err
}

func safeSymbol(s string) bool {
	return strategy.SQLSymbolList([]string{s}) != ""
}

// TrainTree fits and persists a tree for each symbol. Symbols already in the
// model are replaced. A symbol that cannot be fit is reported in
// TreeResult.Skipped and keeps whatever tree it had.
func TrainTree(ctx context.Context, cfg TreeConfig) (TreeResult, error) {
	res := TreeResult{Skipped: map[string]string{}}
	d := DefaultTreeConfig()
	if cfg.MarketDB == "" {
		cfg.MarketDB = d.MarketDB
	}
	if cfg.ModelDB == "" {
		cfg.ModelDB = d.ModelDB
	}
	if cfg.RefDB == "" {
		cfg.RefDB = d.RefDB
	}
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	if fi, err := os.Stat(cfg.MarketDB); err != nil || fi.Size() == 0 {
		return res, fmt.Errorf("train tree: market database %s not found or empty", cfg.MarketDB)
	}
	symbols := cfg.Symbols
	if len(symbols) == 0 {
		var err error
		if symbols, err = treeSymbols(cfg.RefDB); err != nil {
			return res, err
		}
	}
	symbols = cleanSymbols(symbols)
	res.Requested = len(symbols)
	if len(symbols) == 0 {
		return res, fmt.Errorf("train tree: no symbols to train")
	}

	model, err := storage.OpenSQLite(cfg.ModelDB)
	if err != nil {
		return res, err
	}
	defer model.Close()
	if err := storage.RunStage(model, "tree_model_schema", nil); err != nil {
		return res, err
	}

	calcDir := cfg.CalcDir
	if calcDir == "" {
		tmp, err := os.MkdirTemp("", "train-tree-")
		if err != nil {
			return res, err
		}
		defer os.RemoveAll(tmp)
		calcDir = tmp
	}
	calc, err := strategy.OpenCalcDB(cfg.MarketDB, filepath.Join(calcDir, "train_tree.db"))
	if err != nil {
		return res, err
	}
	defer calc.Close()

	for i, sym := range symbols {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		samples, err := loadTreeSamples(ctx, calc, sym)
		if err != nil {
			res.Skipped[sym] = err.Error()
			continue
		}
		if cfg.Through != "" {
			// Fit on the window up to -through only: a backtest of the later months is then
			// out of sample for the tree. Bars after it are still scored by the saved tree.
			kept := samples[:0:0]
			for _, s := range samples {
				if s.Date <= cfg.Through {
					kept = append(kept, s)
				}
			}
			samples = kept
		}
		if cfg.DryRun {
			if err := checkSamples(samples); err != nil {
				res.Skipped[sym] = err.Error()
			} else {
				res.Trained = append(res.Trained, sym)
			}
			continue
		}
		tree, err := fitTree(samples)
		if err != nil {
			res.Skipped[sym] = err.Error()
			continue
		}
		if err := saveTree(ctx, model, sym, tree); err != nil {
			return res, fmt.Errorf("train tree %s: %w", sym, err)
		}
		res.Trained = append(res.Trained, sym)
		if tree.LastDate > res.MarketThrough {
			res.MarketThrough = tree.LastDate
		}
		if (i+1)%250 == 0 || len(symbols) <= 20 {
			fmt.Fprintf(cfg.Out, "[train tree] %d/%d %s: %d nodes, grown on %d of %d labelled bars\n", i+1, len(symbols), sym, len(tree.Nodes), tree.Grown, tree.Samples)
		}
	}
	return res, nil
}

func saveTree(ctx context.Context, model *sqlx.DB, symbol string, t *fittedTree) error {
	counts, err := json.Marshal(t.ClassCounts)
	if err != nil {
		return err
	}
	tx, err := model.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM tree_node WHERE symbol = ?", symbol); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM tree_model_meta WHERE symbol = ?", symbol); err != nil {
		return err
	}
	for _, n := range t.Nodes {
		if n.Feature != "" {
			_, err = tx.Exec("INSERT INTO tree_node (symbol, path, feature, threshold, pred) VALUES (?, ?, ?, ?, NULL)", symbol, n.Path, n.Feature, n.Threshold)
		} else {
			_, err = tx.Exec("INSERT INTO tree_node (symbol, path, feature, threshold, pred) VALUES (?, ?, NULL, NULL, ?)", symbol, n.Path, n.Pred)
		}
		if err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO tree_model_meta (symbol, samples, grown_cases, class_counts, first_date, last_date, trained_at)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'))`, symbol, t.Samples, t.Grown, string(counts), t.FirstDate, t.LastDate); err != nil {
		return err
	}
	return tx.Commit()
}

func treeSymbols(refPath string) ([]string, error) {
	db, err := refdb.OpenExisting(refPath)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, fmt.Errorf("train tree: strategies database %s not found; pass -symbols", refPath)
	}
	defer db.Close()
	var out []string
	if err := db.Select(&out, "SELECT DISTINCT signal_symbol FROM tree_strategy ORDER BY signal_symbol"); err != nil {
		return nil, fmt.Errorf("train tree: read tree_strategy: %w", err)
	}
	return out, nil
}

// runTree is `train tree`: parse flags, train, print the result.
func runTree(args []string, stdout, stderr io.Writer) int {
	cfg := DefaultTreeConfig()
	var symbols string
	fs := flag.NewFlagSet("train tree", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.MarketDB, "db", cfg.MarketDB, "market database holding the bars to train on")
	fs.StringVar(&cfg.ModelDB, "model-db", cfg.ModelDB, "persisted tree database (created if missing)")
	fs.StringVar(&cfg.RefDB, "ref-db", cfg.RefDB, "strategies database naming the symbols when -symbols is not given")
	fs.StringVar(&symbols, "symbols", "", "comma-separated symbols to train (default: every signal_symbol in tree_strategy)")
	fs.StringVar(&cfg.Through, "through", "", "train only on bars up to this date (YYYY-MM-DD) so the later months are out of sample. Default: all history")
	fs.StringVar(&cfg.CalcDir, "calc-dir", "", "keep the feature slice tables here for inspection (default: scratch, removed)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if symbols != "" {
		cfg.Symbols = strings.Split(symbols, ",")
	} else {
		cfg.Symbols = fs.Args()
	}
	cfg.Out = stdout

	res, err := TrainTree(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	reasons := map[string]int{}
	for _, why := range res.Skipped {
		// group by the first words so thousands of skips print as a few lines
		if i := strings.Index(why, " ("); i > 0 {
			why = why[:i]
		}
		reasons[why]++
	}
	var keys []string
	for k := range reasons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(stdout, "skipped %d: %s\n", reasons[k], k)
	}
	fmt.Fprintf(stdout, "trained %d of %d symbols -> %s\n", len(res.Trained), res.Requested, cfg.ModelDB)
	fmt.Fprintf(stdout, "trained on %s daily bars through %s\n", cfg.MarketDB, res.MarketThrough)
	return 0
}
