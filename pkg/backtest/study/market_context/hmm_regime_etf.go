package market_context

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/study"
)

// HMMRegimeETF measures, for every active ETF in the market database, how the next session's
// return depends on the regime the hidden Markov model of one symbol (QQQ by default) had at the
// previous close. The model is read as `train hmm` saved it and never refit. The calculation is the
// SQL stages in sql/stages/hmm_regime_etf. Results go to hmm_regime_etf in the study's results
// database, recreated on every run.
type HMMRegimeETF struct {
	marketDBPath  string
	resultsDBPath string
	hmmSymbol     string

	// HMMDB and UniverseDB override appenv.HMMDB and appenv.UniverseDB (tests point them at
	// temp files).
	HMMDB, UniverseDB string
	// MinStress is the fewest sessions after a stressed close, and after a not-stressed close, an
	// ETF needs to get a row. 0 means 20.
	MinStress int
	// SplitRatio is the one-session close ratio past which a move is a split, not price action. 0 means 1.8.
	SplitRatio float64
	// MinRegime is the fewest whole-period sessions after a close in one regime an ETF needs to be
	// ranked for that regime. 0 means 100.
	MinRegime int
	// TopN is how many ETFs per regime the top tables keep. 0 means 10.
	TopN int
}

func init() { study.Register(&HMMRegimeETF{}) }

func (s *HMMRegimeETF) ID() string { return "hmm_regime_etf" }

func (s *HMMRegimeETF) Name() string { return "HMM Regime vs ETF Returns" }

func (s *HMMRegimeETF) Description() string {
	return "Next-session return and volatility of every ETF by the regime the QQQ hidden Markov model had at the prior close (-symbol picks another model). Regimes are ranked by volatility. Reports the stressed-vs-not t statistic, the share of variance explained and the return of holding only after not-stressed closes."
}

func (s *HMMRegimeETF) SetDatabases(marketDBPath, resultsDBPath string) {
	s.marketDBPath, s.resultsDBPath = marketDBPath, resultsDBPath
}

// SetSymbol picks the symbol whose model is read. Empty means QQQ.
func (s *HMMRegimeETF) SetSymbol(sym string) { s.hmmSymbol = sym }

func (s *HMMRegimeETF) Run() error {
	log.Printf("Running study: %s", s.Name())
	sym := strings.ToUpper(strings.TrimSpace(s.hmmSymbol))
	if sym == "" {
		sym = "QQQ"
	}
	hmmDB, uniDB := s.HMMDB, s.UniverseDB
	if hmmDB == "" {
		hmmDB = appenv.HMMDB()
	}
	if uniDB == "" {
		uniDB = appenv.UniverseDB()
	}
	minStress, split := s.MinStress, s.SplitRatio
	if minStress <= 0 {
		minStress = 20
	}
	if split <= 0 {
		split = 1.8
	}
	minRegime, topN := s.MinRegime, s.TopN
	if minRegime <= 0 {
		minRegime = 100
	}
	if topN <= 0 {
		topN = 10
	}
	for _, p := range []string{s.marketDBPath, hmmDB, uniDB} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("%w (train the model with `train hmm` and refresh the universe if one is missing)", err)
		}
	}

	// The regimes are ranked as calm, middle, stressed, so the model must have three states.
	model, err := storage.OpenSQLiteReadOnly(hmmDB)
	if err != nil {
		return err
	}
	var states int
	err = model.Get(&states, `SELECT states FROM hmm_model_meta WHERE symbol = ?`, sym)
	model.Close()
	if err != nil {
		return fmt.Errorf("no hidden Markov model for %s in %s: run `train hmm -symbols %s`", sym, hmmDB, sym)
	}
	if states != 3 {
		return fmt.Errorf("the %s model has %d states; this study ranks three regimes (calm, middle, stressed)", sym, states)
	}

	if err := os.MkdirAll(filepath.Dir(s.resultsDBPath), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	if err := os.Remove(s.resultsDBPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	db, err := sqlx.Open("sqlite", s.resultsDBPath)
	if err != nil {
		return fmt.Errorf("open results db: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // the ATTACHes must be seen by every statement that follows
	for alias, path := range map[string]string{"market": s.marketDBPath, "hmm": hmmDB, "universe": uniDB} {
		if _, err := db.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS %s", sqlQuote(path), alias)); err != nil {
			return fmt.Errorf("attach %s: %w", alias, err)
		}
	}
	if err := storage.RunStage(db, "hmm_regime_etf", map[string]string{
		"__HMM_SYMBOL__":  sym,
		"__SPLIT_RATIO__": strconv.FormatFloat(split, 'f', -1, 64),
		"__MIN_STRESS__":  strconv.Itoa(minStress),
		"__MIN_REGIME__":  strconv.Itoa(minRegime),
		"__TOP_N__":       strconv.Itoa(topN),
	}); err != nil {
		return err
	}

	var n, etfs, dropped int
	if err := db.Get(&n, `SELECT COUNT(*) FROM hmm_regime_etf WHERE sample = 'all'`); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no ETF had at least %d sessions after a stressed and a not-stressed %s close in %s", minStress, sym, s.marketDBPath)
	}
	if err := db.Get(&etfs, `SELECT COUNT(DISTINCT symbol) FROM etf_ret`); err != nil {
		return err
	}
	if err := db.Get(&dropped, `SELECT COUNT(*) FROM etf_dropped`); err != nil {
		return err
	}
	log.Printf("%d of %d ETFs have enough sessions after a stressed and a not-stressed %s close. %d session(s) dropped as splits (close ratio past %gx).", n, etfs, sym, dropped, split)
	log.Printf("Results saved to %s (tables hmm_regime_etf, hmm_regime_top, hmm_regime_pick, hmm_regime_etf_info, etf_dropped)", s.resultsDBPath)
	return nil
}
