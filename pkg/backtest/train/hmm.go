package train

// The hmm family: a Gaussian hidden Markov model of a symbol's regimes, fit on the daily bars
// in the daily bars database (appenv.DailyBarsDB) and persisted in appenv.HMMDB.
//
//	train hmm                      fit and save (default symbol QQQ, 3 states, 12 month holdout)
//	train hmm -dry-run             fit and print, save nothing
//	train hmm compare              fit 2, 3 and 4 states in memory, print LL and BIC
//	train hmm extend               append predictions for new sessions with the saved parameters
//	train hmm report               in-sample versus out-of-sample table (read-only)
//	train hmm export -out FILE     copy the model database for trade_orchestrator to embed
//
// The observations are built by SQL (sql/stages/hmm_inputs). The EM fit is Go (pkg/hmm)
// because Baum-Welch is iterative. A saved model is never refit by extend: only a deliberate
// `train hmm` replaces it.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/hmm"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// DefaultHMMMinBars is the fewest in-sample sessions a fit may learn from (about four years).
const DefaultHMMMinBars = 1000

// HMMConfig holds every setting of a training run.
type HMMConfig struct {
	BarsDB  string   // daily bars database; empty = appenv.DailyBarsDB()
	ModelDB string   // persisted HMM; empty = appenv.HMMDB()
	Symbols []string // empty = QQQ
	States  int      // 0 = 3
	// Through is the last session the fit may learn from, From the first. Through "" = the
	// newest bar minus HoldoutMonths. Later sessions are filtered with the frozen parameters.
	Through, From string
	HoldoutMonths int // 0 = storage.DefaultHoldoutMonths; ignored when Through is set
	MinBars       int // 0 = DefaultHMMMinBars
	Seed          int64
	DryRun        bool   // fit and report, write nothing
	CalcDir       string // keep the SQL slice tables here for inspection
	Out           io.Writer
}

// HMMResult reports one fit.
type HMMResult struct {
	Symbol         string
	States         int
	Bars           int // in-sample observations
	Observations   int // all observations, in and out of sample
	First, Last    string
	TrainedThrough string
	LogLik, BIC    float64
	Model          *hmm.Model
	Saved          bool
}

func (c *HMMConfig) defaults() error {
	if c.BarsDB == "" {
		c.BarsDB = appenv.DailyBarsDB()
	}
	if c.ModelDB == "" {
		c.ModelDB = appenv.HMMDB()
	}
	if len(c.Symbols) == 0 {
		c.Symbols = []string{"QQQ"}
	}
	c.Symbols = cleanSymbols(c.Symbols)
	if c.States == 0 {
		c.States = 3
	}
	if c.States < 2 || c.States > 6 {
		return fmt.Errorf("train hmm: -states %d: use 2 to 6", c.States)
	}
	if c.HoldoutMonths == 0 {
		c.HoldoutMonths = storage.DefaultHoldoutMonths
	}
	if c.MinBars == 0 {
		c.MinBars = DefaultHMMMinBars
	}
	if c.Out == nil {
		c.Out = io.Discard
	}
	for _, d := range []string{c.Through, c.From} {
		if d != "" && !isoDate.MatchString(d) {
			return fmt.Errorf("train hmm: %q is not a YYYY-MM-DD date", d)
		}
	}
	if len(c.Symbols) == 0 {
		return fmt.Errorf("train hmm: no symbols")
	}
	return nil
}

// fitOne builds the observations of symbol, picks the in-sample end, fits, and filters every
// observation with the fitted parameters.
func fitOne(ctx context.Context, cfg HMMConfig, symbol string, states int) (fitted, error) {
	var f fitted
	dates, obs, err := hmmObs(ctx, cfg.BarsDB, cfg.CalcDir, symbol)
	if err != nil {
		return f, err
	}
	if len(obs) == 0 {
		return f, fmt.Errorf("train hmm: no observations for %s in %s", symbol, cfg.BarsDB)
	}
	through := cfg.Through
	if through == "" {
		end, ok, err := storage.InSampleEnd("", "", dates[0], dates[len(dates)-1], cfg.HoldoutMonths)
		if err != nil {
			return f, err
		}
		if !ok {
			return f, fmt.Errorf("train hmm: %s has too little history for a %d month holdout; pass -through", symbol, cfg.HoldoutMonths)
		}
		through = end
	}
	var in [][]float64
	var firstIn, lastIn string
	for i, d := range dates {
		if d > through || (cfg.From != "" && d < cfg.From) {
			continue
		}
		if firstIn == "" {
			firstIn = d
		}
		lastIn = d
		in = append(in, obs[i])
	}
	if len(in) < cfg.MinBars {
		return f, fmt.Errorf("train hmm: %s has %d in-sample sessions (%s to %s), under the %d minimum (-min-bars)", symbol, len(in), firstIn, through, cfg.MinBars)
	}
	model, st, err := hmm.Fit(ctx, in, states, hmm.Options{Seed: cfg.Seed})
	if err != nil {
		return f, fmt.Errorf("train hmm: %s: %w", symbol, err)
	}
	filt, err := model.Filter(obs)
	if err != nil {
		return f, fmt.Errorf("train hmm: %s: %w", symbol, err)
	}
	f = fitted{
		model: model, dates: dates, obs: obs, filtered: filt,
		meta: hmmMeta{
			Symbol: symbol, States: states, Dims: hmmDims, VolWindow: HMMVolWindow, Bars: len(in),
			FirstDate: dates[0], LastDate: dates[len(dates)-1], TrainedThrough: lastIn,
			TrainedAt: nowUTC(), LogLik: st.LogLik, BIC: st.BIC, Source: sourceName,
		},
	}
	return f, nil
}

func (f fitted) result() HMMResult {
	return HMMResult{
		Symbol: f.meta.Symbol, States: f.meta.States, Bars: f.meta.Bars, Observations: len(f.obs),
		First: f.meta.FirstDate, Last: f.meta.LastDate, TrainedThrough: f.meta.TrainedThrough,
		LogLik: f.meta.LogLik, BIC: f.meta.BIC, Model: f.model,
	}
}

// TrainHMM fits and saves a model for each symbol. Symbols already in the model database are
// replaced. With DryRun nothing is written.
func TrainHMM(ctx context.Context, cfg HMMConfig) ([]HMMResult, error) {
	if err := cfg.defaults(); err != nil {
		return nil, err
	}
	var out []HMMResult
	for _, sym := range cfg.Symbols {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		f, err := fitOne(ctx, cfg, sym, cfg.States)
		if err != nil {
			return out, err
		}
		res := f.result()
		if !cfg.DryRun {
			if err := storeHMM(ctx, cfg.ModelDB, f); err != nil {
				return out, err
			}
			res.Saved = true
		}
		out = append(out, res)
		fmt.Fprintf(cfg.Out, "[train hmm] %s: %d states, learned from %d sessions %s..%s, LL %.1f BIC %.1f\n",
			sym, res.States, res.Bars, f.meta.FirstDate, res.TrainedThrough, res.LogLik, res.BIC)
	}
	return out, nil
}

// CompareHMM fits each state count on the same in-sample sessions and writes nothing.
func CompareHMM(ctx context.Context, cfg HMMConfig, states []int) ([]HMMResult, error) {
	if err := cfg.defaults(); err != nil {
		return nil, err
	}
	var out []HMMResult
	for _, sym := range cfg.Symbols {
		for _, n := range states {
			c := cfg
			c.States = n
			if err := c.defaults(); err != nil {
				return out, err
			}
			f, err := fitOne(ctx, c, sym, n)
			if err != nil {
				return out, err
			}
			out = append(out, f.result())
		}
	}
	return out, nil
}

// ExtendHMMConfig says which saved models to bring up to date.
type ExtendHMMConfig struct {
	BarsDB, ModelDB string
	Symbols         []string // empty = QQQ
	// Through is the last completed session (YYYY-MM-DD). Later observations are ignored. "" = no limit.
	Through string
	DryRun  bool
	CalcDir string
}

// ExtendHMMResult reports one symbol.
type ExtendHMMResult struct {
	Symbol  string
	NewRows int
	Latest  HMMLatest
}

// ExtendHMM appends a prediction for every session after each saved model's last_date. It never
// refits: the saved initial probabilities, transitions, means and covariances are used as they
// are, and the filter runs from the first observation so every row is exactly what a full run
// would give. Rows already saved are kept. A symbol with no saved model is an error.
func ExtendHMM(ctx context.Context, cfg ExtendHMMConfig) ([]ExtendHMMResult, error) {
	if cfg.BarsDB == "" {
		cfg.BarsDB = appenv.DailyBarsDB()
	}
	if cfg.ModelDB == "" {
		cfg.ModelDB = appenv.HMMDB()
	}
	if len(cfg.Symbols) == 0 {
		cfg.Symbols = []string{"QQQ"}
	}
	if !isoDateOrEmpty(cfg.Through) {
		return nil, fmt.Errorf("extend hmm: %q is not a YYYY-MM-DD date", cfg.Through)
	}
	if fi, err := os.Stat(cfg.ModelDB); err != nil || fi.Size() == 0 {
		return nil, fmt.Errorf("extend hmm: model database %s not found or empty (run: train hmm)", cfg.ModelDB)
	}
	db, err := storage.OpenSQLite(cfg.ModelDB)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var out []ExtendHMMResult
	for _, sym := range cleanSymbols(cfg.Symbols) {
		model, meta, err := loadHMM(db, sym)
		if err != nil {
			return out, fmt.Errorf("extend hmm: %w", err)
		}
		dates, obs, err := hmmObs(ctx, cfg.BarsDB, cfg.CalcDir, sym)
		if err != nil {
			return out, err
		}
		if cfg.Through != "" {
			n := 0
			for n < len(dates) && dates[n] <= cfg.Through {
				n++
			}
			dates, obs = dates[:n], obs[:n]
		}
		if len(dates) == 0 {
			return out, fmt.Errorf("extend hmm: no observations for %s in %s", sym, cfg.BarsDB)
		}
		if dates[0] != meta.FirstDate {
			return out, fmt.Errorf("extend hmm: %s bars now start %s but the model was fit from %s; retrain (train hmm)", sym, dates[0], meta.FirstDate)
		}
		filt, err := model.Filter(obs)
		if err != nil {
			return out, fmt.Errorf("extend hmm: %s: %w", sym, err)
		}
		f := fitted{meta: meta, model: model, dates: dates, obs: obs, filtered: filt}
		res := ExtendHMMResult{Symbol: sym}
		for _, d := range dates {
			if d > meta.LastDate {
				res.NewRows++
			}
		}
		if res.NewRows > 0 && !cfg.DryRun {
			tx, err := db.BeginTxx(ctx, nil)
			if err != nil {
				return out, err
			}
			if err := insertPredictions(tx, sym, f, meta.LastDate); err != nil {
				tx.Rollback()
				return out, err
			}
			if _, err := tx.Exec("UPDATE hmm_model_meta SET last_date = ? WHERE symbol = ?", dates[len(dates)-1], sym); err != nil {
				tx.Rollback()
				return out, err
			}
			if err := tx.Commit(); err != nil {
				return out, err
			}
		}
		if !cfg.DryRun {
			if res.Latest, err = readHMMLatest(db, sym); err != nil {
				return out, err
			}
		} else {
			t := len(dates) - 1
			p, nx := filt.Probs[t], filt.Next[t]
			res.Latest = HMMLatest{Symbol: sym, Date: dates[t], Regime: hmm.Argmax(p), RegimeProb: p[hmm.Argmax(p)],
				Probs: p, NextRegime: hmm.Argmax(nx), NextRegimeProb: nx[hmm.Argmax(nx)], NextProbs: nx,
				TrainedThrough: meta.TrainedThrough, States: meta.States}
		}
		out = append(out, res)
	}
	return out, nil
}

// HMMReportConfig names the model to report on.
type HMMReportConfig struct {
	ModelDB string
	Symbols []string // empty = QQQ
	CalcDir string
}

// ReportHMM prints, for each symbol, the fitted regimes and how each regime looked in sample
// versus out of sample. It reads the model database and never writes it.
func ReportHMM(ctx context.Context, cfg HMMReportConfig, out io.Writer) error {
	if cfg.ModelDB == "" {
		cfg.ModelDB = appenv.HMMDB()
	}
	if len(cfg.Symbols) == 0 {
		cfg.Symbols = []string{"QQQ"}
	}
	dir, cleanup, err := scratchDir(cfg.CalcDir, "hmm-report-")
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := os.Stat(cfg.ModelDB); err != nil {
		return fmt.Errorf("report hmm: model database %s not found (run: train hmm)", cfg.ModelDB)
	}
	calc, err := openCalc(filepath.Join(dir, "hmm_report.db"), map[string]string{"model": cfg.ModelDB})
	if err != nil {
		return err
	}
	defer calc.Close()
	for _, sym := range cleanSymbols(cfg.Symbols) {
		list := "'" + sym + "'"
		if err := storage.RunStage(calc, "hmm_report", map[string]string{"__SYMBOL_LIST__": list}); err != nil {
			return err
		}
		var m hmmMeta
		if err := calc.GetContext(ctx, &m, "SELECT * FROM model.hmm_model_meta WHERE symbol = ?", sym); err != nil {
			return fmt.Errorf("report hmm: no saved HMM for %s: %w", sym, err)
		}
		fmt.Fprintf(out, "%s HMM: %d states, learned from %d sessions %s..%s, predictions through %s\n", sym, m.States, m.Bars, m.FirstDate, m.TrainedThrough, m.LastDate)
		fmt.Fprintf(out, "log-likelihood %.1f, BIC %.1f, fit on %s bars at %s\n\n", m.LogLik, m.BIC, m.Source, m.TrainedAt)

		var fit []struct {
			State    int     `db:"state"`
			Pi       float64 `db:"pi"`
			MuRet    float64 `db:"mu_ret"`
			MuVol    float64 `db:"mu_vol"`
			Stay     float64 `db:"stay"`
			Duration float64 `db:"duration"`
		}
		if err := calc.SelectContext(ctx, &fit, `
			SELECT p.state, p.pi, p.mu_ret, p.mu_vol, t.prob AS stay, 1.0 / (1.0 - t.prob) AS duration
			FROM model.hmm_params p
			JOIN model.hmm_transition t ON t.symbol = p.symbol AND t.from_state = p.state AND t.to_state = p.state
			WHERE p.symbol = ? ORDER BY p.state`, sym); err != nil {
			return err
		}
		fmt.Fprintln(out, "fitted regimes (state 0 = lowest mean return)")
		fmt.Fprintf(out, "%-6s %-9s %-10s %-9s %-8s %s\n", "state", "mean ret", "mean vol", "P(stay)", "expected", "")
		for _, r := range fit {
			fmt.Fprintf(out, "%-6d %+8.3f%% %9.3f%% %8.3f %7.1fd\n", r.State, r.MuRet*100, r.MuVol*100, r.Stay, r.Duration)
		}

		var rows []struct {
			Regime   int      `db:"regime"`
			InSample int      `db:"in_sample"`
			Days     int      `db:"days"`
			MeanRet  float64  `db:"mean_ret"`
			MeanVol  float64  `db:"mean_vol"`
			MeanLL   float64  `db:"mean_loglik"`
			AvgRun   *float64 `db:"avg_run"`
		}
		if err := calc.SelectContext(ctx, &rows, "SELECT regime, in_sample, days, mean_ret, mean_vol, mean_loglik, avg_run FROM hmm_rep_regime WHERE symbol = ? ORDER BY in_sample DESC, regime", sym); err != nil {
			return err
		}
		fmt.Fprintln(out, "\nobserved by filtered regime (days, mean daily return, mean realized vol, avg run length, mean log-lik per bar)")
		fmt.Fprintf(out, "%-12s %-6s %-6s %-10s %-10s %-9s %s\n", "sample", "state", "days", "mean ret", "mean vol", "avg run", "LL/bar")
		oos := 0
		for _, r := range rows {
			label := "in-sample"
			if r.InSample == 0 {
				label, oos = "out-of-sample", oos+r.Days
			}
			run := 0.0
			if r.AvgRun != nil {
				run = *r.AvgRun
			}
			fmt.Fprintf(out, "%-12s %-6d %-6d %+9.3f%% %9.3f%% %8.1fd %8.3f\n", label, r.Regime, r.Days, r.MeanRet*100, r.MeanVol*100, run, r.MeanLL)
		}
		if oos == 0 {
			fmt.Fprintln(out, "\nno out-of-sample sessions: this model learned from every bar, so there is no honest held-out result.")
		}
		var ll []struct {
			InSample int     `db:"in_sample"`
			Mean     float64 `db:"m"`
		}
		if err := calc.SelectContext(ctx, &ll, "SELECT in_sample, AVG(loglik_bar) AS m FROM model.hmm_prediction WHERE symbol = ? GROUP BY in_sample ORDER BY in_sample DESC", sym); err != nil {
			return err
		}
		for _, l := range ll {
			side := "in-sample"
			if l.InSample == 0 {
				side = "out-of-sample"
			}
			fmt.Fprintf(out, "mean log-likelihood per bar, %s: %.4f\n", side, l.Mean)
		}
		fmt.Fprintln(out)
	}
	return nil
}

// runHMM is `train hmm`: dispatch the subcommands, parse flags, print.
func runHMM(args []string, stdout, stderr io.Writer) int {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	if sub != "" && sub != "compare" && sub != "extend" && sub != "report" && sub != "export" {
		fmt.Fprintf(stderr, "train hmm: unknown subcommand %q (compare, extend, report, export)\n", sub)
		return 2
	}
	cfg := HMMConfig{Out: stdout}
	var symbols, states string
	fs := flag.NewFlagSet("train hmm "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: train hmm [compare|extend|report] [flags] [symbols]

  (none)   fit a hidden Markov model of daily returns and realized volatility and SAVE it to the
           model database, replacing the symbol's model. -dry-run fits and prints, saves nothing.
  compare  fit 2, 3 and 4 states in memory and print log-likelihood and BIC (read-only).
  extend   append predictions for new sessions with the saved parameters; never refits. -dry-run previews.
  report   print the fitted regimes and the in-sample versus out-of-sample table (read-only).
  export   write a self-contained copy of the model database to -out, for trade_orchestrator to embed.`)
		fs.PrintDefaults()
	}
	fs.StringVar(&cfg.BarsDB, "bars-db", appenv.DailyBarsDB(), "daily bars database (written by daily_bars)")
	fs.StringVar(&cfg.ModelDB, "model-db", appenv.HMMDB(), "persisted HMM database (created if missing)")
	fs.StringVar(&symbols, "symbols", "QQQ", "comma-separated symbols")
	fs.StringVar(&cfg.CalcDir, "calc-dir", "", "keep the SQL slice tables here for inspection (default: scratch, removed)")
	var exportOut string
	switch sub {
	case "export":
		fs.StringVar(&exportOut, "out", "", "file to write the model database copy to")
	case "":
		fs.IntVar(&cfg.States, "states", 3, "number of hidden states, 2 to 6")
		fs.StringVar(&cfg.Through, "through", "", "last session the fit may learn from, YYYY-MM-DD (default: newest bar minus the holdout)")
		fs.StringVar(&cfg.From, "from", "", "first session the fit may learn from, YYYY-MM-DD (default: the start of the data)")
		fs.IntVar(&cfg.HoldoutMonths, "holdout-months", storage.DefaultHoldoutMonths, "months kept out of the fit when -through is not given")
		fs.IntVar(&cfg.MinBars, "min-bars", DefaultHMMMinBars, "fewest in-sample sessions a fit may learn from")
		fs.Int64Var(&cfg.Seed, "seed", 0, "seed for the random restarts (same data and seed give the same model)")
		fs.BoolVar(&cfg.DryRun, "dry-run", false, "fit and print, save nothing")
	case "compare":
		fs.StringVar(&states, "states", "2,3,4", "comma-separated state counts to fit")
		fs.StringVar(&cfg.Through, "through", "", "last session the fit may learn from (default: newest bar minus the holdout)")
		fs.StringVar(&cfg.From, "from", "", "first session the fit may learn from")
		fs.IntVar(&cfg.HoldoutMonths, "holdout-months", storage.DefaultHoldoutMonths, "months kept out of the fit when -through is not given")
		fs.IntVar(&cfg.MinBars, "min-bars", DefaultHMMMinBars, "fewest in-sample sessions a fit may learn from")
		fs.Int64Var(&cfg.Seed, "seed", 0, "seed for the random restarts")
	case "extend":
		fs.StringVar(&cfg.Through, "through", "", "last completed session, YYYY-MM-DD (default: every bar in the daily bars database)")
		fs.BoolVar(&cfg.DryRun, "dry-run", false, "compute and print the latest prediction, save nothing")
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		symbols = strings.Join(fs.Args(), ",")
	}
	cfg.Symbols = strings.Split(symbols, ",")
	ctx := context.Background()

	switch sub {
	case "export":
		if exportOut == "" {
			fmt.Fprintln(stderr, "train hmm export: -out is required")
			return 2
		}
		if err := ExportHMM(ctx, cfg.ModelDB, exportOut); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "wrote %s from %s\n", exportOut, cfg.ModelDB)
	case "report":
		if err := ReportHMM(ctx, HMMReportConfig{ModelDB: cfg.ModelDB, Symbols: cfg.Symbols, CalcDir: cfg.CalcDir}, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	case "extend":
		res, err := ExtendHMM(ctx, ExtendHMMConfig{BarsDB: cfg.BarsDB, ModelDB: cfg.ModelDB, Symbols: cfg.Symbols, Through: cfg.Through, DryRun: cfg.DryRun, CalcDir: cfg.CalcDir})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		for _, r := range res {
			verb := "added"
			if cfg.DryRun {
				verb = "would add"
			}
			fmt.Fprintf(stdout, "[train hmm extend] %s: %s %d session(s)\n", r.Symbol, verb, r.NewRows)
			printLatest(stdout, r.Latest)
		}
	case "compare":
		var ns []int
		for _, s := range strings.Split(states, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil {
				fmt.Fprintf(stderr, "train hmm compare: bad -states %q\n", states)
				return 2
			}
			ns = append(ns, n)
		}
		res, err := CompareHMM(ctx, cfg, ns)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%-8s %-7s %-9s %-13s %-12s %s\n", "symbol", "states", "bars", "log-lik", "BIC", "(lower BIC is better)")
		for _, r := range res {
			fmt.Fprintf(stdout, "%-8s %-7d %-9d %-13.1f %-12.1f\n", r.Symbol, r.States, r.Bars, r.LogLik, r.BIC)
		}
	default:
		res, err := TrainHMM(ctx, cfg)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		for _, r := range res {
			if r.Saved {
				fmt.Fprintf(stdout, "saved %s to %s; sessions after %s are filtered with the frozen parameters (out of sample)\n", r.Symbol, cfg.ModelDB, r.TrainedThrough)
			} else {
				fmt.Fprintf(stdout, "dry run: %s not saved; it would learn through %s\n", r.Symbol, r.TrainedThrough)
			}
		}
	}
	return 0
}

func printLatest(w io.Writer, l HMMLatest) {
	if l.Date == "" {
		return
	}
	fmt.Fprintf(w, "  as of the close of %s: regime %d (%.0f%%), filtered probabilities %s\n", l.Date, l.Regime, l.RegimeProb*100, pct(l.Probs))
	fmt.Fprintf(w, "  next session: regime %d (%.0f%%), probabilities %s  (model learned through %s)\n", l.NextRegime, l.NextRegimeProb*100, pct(l.NextProbs), l.TrainedThrough)
}

func pct(p []float64) string {
	parts := make([]string, len(p))
	for i, v := range p {
		parts[i] = fmt.Sprintf("%d:%.0f%%", i, v*100)
	}
	return strings.Join(parts, " ")
}
