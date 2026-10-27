package train

// The markov family: trains the Markov regime models on the daily bars in the
// market database and persists them in SQLite (appenv.MarkovDB). Backtests read
// the persisted model; they never train one.
//
// The calculation is SQL (sql/stages/markov_train): 20-bar return, bull/bear/
// sideways state, transitions, walk-forward counts and probabilities, one slice
// table per stage. Go orders the stages, batches the symbols and publishes the
// result into the persisted model.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// DefaultBatch is how many symbols are trained per pass of the SQL stages.
const DefaultBatch = 200

// Config holds every setting of a training run.
type MarkovConfig struct {
	MarketDB string   // bars to train on; empty = appenv.MarketDB()
	ModelDB  string   // persisted model, created if missing; empty = appenv.MarkovDB()
	RefDB    string   // strategies DB naming the symbols when Symbols is empty; empty = appenv.RefDB()
	Symbols  []string // symbols to train; empty = every signal_symbol in markov_strategy
	Batch    int      // symbols per pass; 0 = DefaultBatch
	CalcDir  string   // keep the slice tables of the last batch here; empty = scratch dir, removed
	// Through is the last session (YYYY-MM-DD) the model may learn from, From the first;
	// "" = no limit. Sessions after Through are still scored, with the counts frozen at
	// Through, so the model never learns from the held-out period. This matches
	// backtestgoduckdb's train markov -through/-from.
	Through, From string
	Out           io.Writer
}

var isoDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// DefaultMarkovConfig returns the defaults of `train markov`.
func DefaultMarkovConfig() MarkovConfig {
	return MarkovConfig{MarketDB: appenv.MarketDB(), ModelDB: appenv.MarkovDB(), RefDB: appenv.RefDB()}
}

// MarkovResult reports what a run trained.
type MarkovResult struct {
	Requested int // symbols asked for
	Trained   int // symbols that now have predictions
	Skipped   int // symbols with too little history (no state is defined before 21 bars)
	Rows      int // prediction rows written
	// MarketThrough is the latest daily bar date any trained symbol was trained through.
	MarketThrough string
}

// TrainMarkov trains and persists the model for cfg.Symbols. Symbols already in the
// model are replaced.
func TrainMarkov(ctx context.Context, cfg MarkovConfig) (MarkovResult, error) {
	var res MarkovResult
	d := DefaultMarkovConfig()
	if cfg.MarketDB == "" {
		cfg.MarketDB = d.MarketDB
	}
	if cfg.ModelDB == "" {
		cfg.ModelDB = d.ModelDB
	}
	if cfg.RefDB == "" {
		cfg.RefDB = d.RefDB
	}
	if cfg.Batch <= 0 {
		cfg.Batch = DefaultBatch
	}
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	for _, d := range []string{cfg.Through, cfg.From} {
		if d != "" && !isoDate.MatchString(d) {
			return res, fmt.Errorf("train markov: %q is not a YYYY-MM-DD date", d)
		}
	}
	if fi, err := os.Stat(cfg.MarketDB); err != nil || fi.Size() == 0 {
		return res, fmt.Errorf("train markov: market database %s not found or empty", cfg.MarketDB)
	}

	symbols := cfg.Symbols
	if len(symbols) == 0 {
		var err error
		if symbols, err = signalSymbols(cfg.RefDB); err != nil {
			return res, err
		}
	}
	symbols = cleanSymbols(symbols)
	res.Requested = len(symbols)
	if len(symbols) == 0 {
		return res, fmt.Errorf("train markov: no symbols to train")
	}

	// The persisted model: schema first.
	model, err := storage.OpenSQLite(cfg.ModelDB)
	if err != nil {
		return res, err
	}
	if err := storage.RunStage(model, "markov_model_schema", nil); err != nil {
		model.Close()
		return res, err
	}
	if err := addColumnIfMissing(model, "markov_model_meta", "trained_through", "TEXT"); err != nil {
		model.Close()
		return res, err
	}
	model.Close()

	// Scratch calc database: the slice tables of each batch. The market
	// database is attached as backtest_start, the model as model.
	calcDir := cfg.CalcDir
	if calcDir == "" {
		tmp, err := os.MkdirTemp("", "train-markov-")
		if err != nil {
			return res, err
		}
		defer os.RemoveAll(tmp)
		calcDir = tmp
	}
	calc, err := strategy.OpenCalcDB(cfg.MarketDB, filepath.Join(calcDir, "markov_train.db"))
	if err != nil {
		return res, err
	}
	defer calc.Close()
	if _, err := calc.ExecContext(ctx, fmt.Sprintf("ATTACH DATABASE '%s' AS model", strings.ReplaceAll(cfg.ModelDB, "'", "''"))); err != nil {
		return res, fmt.Errorf("train markov: attach model database: %w", err)
	}

	for from := 0; from < len(symbols); from += cfg.Batch {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		to := from + cfg.Batch
		if to > len(symbols) {
			to = len(symbols)
		}
		list := strategy.SQLSymbolList(symbols[from:to])
		repl := map[string]string{"__SYMBOL_LIST__": list, "__THROUGH__": cfg.Through, "__FROM__": cfg.From}
		if err := storage.RunStage(calc, "markov_train", repl); err != nil {
			return res, err
		}
		if err := storage.RunStage(calc, "markov_publish", repl); err != nil {
			return res, err
		}
		var rows, trained int
		if err := calc.GetContext(ctx, &rows, "SELECT COUNT(*) FROM markov_batch_prediction"); err != nil {
			return res, err
		}
		if err := calc.GetContext(ctx, &trained, "SELECT COUNT(DISTINCT symbol) FROM markov_batch_prediction"); err != nil {
			return res, err
		}
		var through string
		if err := calc.GetContext(ctx, &through, "SELECT COALESCE(MAX(date), '') FROM markov_batch_prediction"); err != nil {
			return res, err
		}
		if through > res.MarketThrough {
			res.MarketThrough = through
		}
		res.Rows += rows
		res.Trained += trained
		fmt.Fprintf(cfg.Out, "[train markov] %d/%d symbols\n", to, len(symbols))
	}
	res.Skipped = res.Requested - res.Trained
	return res, nil
}

func signalSymbols(refPath string) ([]string, error) {
	db, err := refdb.OpenExisting(refPath)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, fmt.Errorf("train markov: strategies database %s not found; pass -symbols", refPath)
	}
	defer db.Close()
	var out []string
	if err := db.Select(&out, "SELECT DISTINCT signal_symbol FROM markov_strategy ORDER BY signal_symbol"); err != nil {
		return nil, fmt.Errorf("train markov: read markov_strategy: %w", err)
	}
	return out, nil
}

func cleanSymbols(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.ToUpper(strings.TrimSpace(s))
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// runMarkov is `train markov`: parse flags, train, print the result.
func runMarkov(args []string, stdout, stderr io.Writer) int {
	cfg := DefaultMarkovConfig()
	var symbols string
	fs := flag.NewFlagSet("train markov", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.MarketDB, "db", cfg.MarketDB, "market database holding the bars to train on")
	fs.StringVar(&cfg.ModelDB, "model-db", cfg.ModelDB, "persisted Markov model database (created if missing)")
	fs.StringVar(&cfg.RefDB, "ref-db", cfg.RefDB, "strategies database naming the symbols when -symbols is not given")
	fs.StringVar(&symbols, "symbols", "", "comma-separated symbols to train (default: every signal_symbol in markov_strategy)")
	fs.IntVar(&cfg.Batch, "batch", DefaultBatch, "symbols per pass of the SQL stages")
	fs.StringVar(&cfg.Through, "through", "", "last session the model may learn from, YYYY-MM-DD (default: no limit; the in-sample end for a model that will be backtested out of sample)")
	fs.StringVar(&cfg.From, "from", "", "first session the model may learn from, YYYY-MM-DD (default: the start of the data)")
	fs.StringVar(&cfg.CalcDir, "calc-dir", "", "keep the last batch's slice tables here for inspection (default: scratch, removed)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if symbols != "" {
		cfg.Symbols = strings.Split(symbols, ",")
	} else {
		cfg.Symbols = fs.Args()
	}
	cfg.Out = stdout

	res, err := TrainMarkov(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "trained %d of %d symbols (%d skipped: under 21 bars), %d prediction rows -> %s\n",
		res.Trained, res.Requested, res.Skipped, res.Rows, cfg.ModelDB)
	fmt.Fprintf(stdout, "trained on %s daily bars through %s\n", cfg.MarketDB, res.MarketThrough)
	if cfg.Through != "" {
		fmt.Fprintf(stdout, "learned from sessions %s to %s; later sessions are scored with the counts frozen at %s\n", orStart(cfg.From), cfg.Through, cfg.Through)
	}
	return 0
}

func orStart(from string) string {
	if from == "" {
		return "the start"
	}
	return from
}

// addColumnIfMissing upgrades a model database made before the column existed.
func addColumnIfMissing(db *sqlx.DB, table, column, typ string) error {
	var n int
	if err := db.Get(&n, "SELECT COUNT(*) FROM pragma_table_info('"+table+"') WHERE name = ?", column); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + typ)
	return err
}
