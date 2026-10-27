package train

// Persistence for the HMM family: the observation build (SQL stage hmm_inputs over the daily
// bars database) and the model tables in appenv.HMMDB (SQL stage hmm_model_schema).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/dailybars"
	"github.com/darianmavgo/trade_well/pkg/backtest/hmm"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

const (
	// HMMVolWindow is the number of returns in the realized-volatility observation. The SQL
	// stage hmm_inputs hardcodes the same 20.
	HMMVolWindow = 20
	// hmmDims is the observation size: (return, realized volatility).
	hmmDims = 2
)

// openCalc opens a scratch SQLite database on one connection (so an ATTACH is seen by every
// statement) with each name -> path in attach attached.
func openCalc(path string, attach map[string]string) (*sqlx.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(path)
	db, err := sqlx.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for name, p := range attach {
		if _, err := db.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS %s", strings.ReplaceAll(p, "'", "''"), name)); err != nil {
			db.Close()
			return nil, fmt.Errorf("attach %s: %w", p, err)
		}
	}
	return db, nil
}

// scratchDir returns dir, or a new temp dir and a cleanup func when dir is "".
func scratchDir(dir, prefix string) (string, func(), error) {
	if dir != "" {
		return dir, func() {}, nil
	}
	tmp, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", nil, err
	}
	return tmp, func() { os.RemoveAll(tmp) }, nil
}

// hmmObs builds the (return, volatility) observations of one symbol from the daily bars
// database with the hmm_inputs SQL stage and returns them in date order.
func hmmObs(ctx context.Context, barsDB, calcDir, symbol string) ([]string, [][]float64, error) {
	if fi, err := os.Stat(barsDB); err != nil || fi.Size() == 0 {
		return nil, nil, fmt.Errorf("daily bars database %s not found or empty: run daily_bars first", barsDB)
	}
	dir, cleanup, err := scratchDir(calcDir, "hmm-inputs-")
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()
	calc, err := openCalc(filepath.Join(dir, "hmm_inputs.db"), map[string]string{"bars": barsDB})
	if err != nil {
		return nil, nil, err
	}
	defer calc.Close()
	list := strategy.SQLSymbolList([]string{symbol})
	if list == "" {
		return nil, nil, fmt.Errorf("hmm: bad symbol %q", symbol)
	}
	if err := storage.RunStage(calc, "hmm_inputs", map[string]string{"__SYMBOL_LIST__": list}); err != nil {
		return nil, nil, err
	}
	var rows []struct {
		Date string  `db:"date"`
		Ret  float64 `db:"ret"`
		Vol  float64 `db:"vol"`
	}
	if err := calc.SelectContext(ctx, &rows, "SELECT date, ret, vol FROM hmm_obs ORDER BY date"); err != nil {
		return nil, nil, err
	}
	dates := make([]string, len(rows))
	obs := make([][]float64, len(rows))
	for i, r := range rows {
		dates[i] = r.Date
		obs[i] = []float64{r.Ret, r.Vol}
	}
	return dates, obs, nil
}

// hmmMeta is one hmm_model_meta row.
type hmmMeta struct {
	Symbol         string  `db:"symbol"`
	States         int     `db:"states"`
	Dims           int     `db:"dims"`
	VolWindow      int     `db:"vol_window"`
	Bars           int     `db:"bars"`
	FirstDate      string  `db:"first_date"`
	LastDate       string  `db:"last_date"`
	TrainedThrough string  `db:"trained_through"`
	TrainedAt      string  `db:"trained_at"`
	LogLik         float64 `db:"loglik"`
	BIC            float64 `db:"bic"`
	Source         string  `db:"source"`
}

// fitted is everything a fit produced for one symbol.
type fitted struct {
	meta     hmmMeta
	model    *hmm.Model
	dates    []string
	obs      [][]float64
	filtered hmm.Filtered
}

// storeHMM replaces symbol's model, parameters and predictions in the model database.
func storeHMM(ctx context.Context, modelDB string, f fitted) error {
	db, err := storage.OpenSQLite(modelDB)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := storage.RunStage(db, "hmm_model_schema", nil); err != nil {
		return err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sym := f.meta.Symbol
	for _, t := range []string{"hmm_model_meta", "hmm_params", "hmm_transition", "hmm_state_prob", "hmm_prediction"} {
		if _, err := tx.Exec("DELETE FROM "+t+" WHERE symbol = ?", sym); err != nil {
			return err
		}
	}
	m := f.meta
	if _, err := tx.Exec(`INSERT INTO hmm_model_meta (symbol, states, dims, vol_window, bars, first_date, last_date, trained_through, trained_at, loglik, bic, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sym, m.States, m.Dims, m.VolWindow, m.Bars, m.FirstDate, m.LastDate, m.TrainedThrough, m.TrainedAt, m.LogLik, m.BIC, m.Source); err != nil {
		return err
	}
	for i := 0; i < f.model.N; i++ {
		c := f.model.Cov[i]
		if _, err := tx.Exec(`INSERT INTO hmm_params (symbol, state, pi, mu_ret, mu_vol, var_ret, cov_ret_vol, var_vol) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			sym, i, f.model.Pi[i], f.model.Mu[i][0], f.model.Mu[i][1], c[0][0], c[0][1], c[1][1]); err != nil {
			return err
		}
		for j := 0; j < f.model.N; j++ {
			if _, err := tx.Exec(`INSERT INTO hmm_transition (symbol, from_state, to_state, prob) VALUES (?, ?, ?, ?)`, sym, i, j, f.model.A[i][j]); err != nil {
				return err
			}
		}
	}
	if err := insertPredictions(tx, sym, f, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// insertPredictions writes the prediction and state-probability rows of every date after
// after ("" = all dates).
func insertPredictions(tx *sqlx.Tx, sym string, f fitted, after string) error {
	for t, d := range f.dates {
		if d <= after {
			continue
		}
		p, nx := f.filtered.Probs[t], f.filtered.Next[t]
		reg, nreg := hmm.Argmax(p), hmm.Argmax(nx)
		inSample := 0
		if d <= f.meta.TrainedThrough {
			inSample = 1
		}
		if _, err := tx.Exec(`INSERT INTO hmm_prediction (symbol, date, ret, vol, regime, regime_prob, next_regime, next_regime_prob, loglik_bar, in_sample)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sym, d, f.obs[t][0], f.obs[t][1], reg, p[reg], nreg, nx[nreg], f.filtered.LogLikBar[t], inSample); err != nil {
			return err
		}
		for i := range p {
			if _, err := tx.Exec(`INSERT INTO hmm_state_prob (symbol, date, state, prob, next_prob) VALUES (?, ?, ?, ?, ?)`, sym, d, i, p[i], nx[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// loadHMM reads a saved model and its meta row. A symbol with no model is an error that says
// how to make one.
func loadHMM(db *sqlx.DB, symbol string) (*hmm.Model, hmmMeta, error) {
	var meta hmmMeta
	if err := db.Get(&meta, "SELECT * FROM hmm_model_meta WHERE symbol = ?", symbol); err != nil {
		return nil, meta, fmt.Errorf("no saved HMM for %s (run: train hmm -symbols %s): %w", symbol, symbol, err)
	}
	if meta.Dims != hmmDims || meta.VolWindow != HMMVolWindow {
		return nil, meta, fmt.Errorf("saved HMM for %s uses %d dims and a %d-day volatility; this build expects %d and %d", symbol, meta.Dims, meta.VolWindow, hmmDims, HMMVolWindow)
	}
	var ps []struct {
		State  int     `db:"state"`
		Pi     float64 `db:"pi"`
		MuRet  float64 `db:"mu_ret"`
		MuVol  float64 `db:"mu_vol"`
		VarRet float64 `db:"var_ret"`
		Cov    float64 `db:"cov_ret_vol"`
		VarVol float64 `db:"var_vol"`
	}
	if err := db.Select(&ps, "SELECT state, pi, mu_ret, mu_vol, var_ret, cov_ret_vol, var_vol FROM hmm_params WHERE symbol = ? ORDER BY state", symbol); err != nil {
		return nil, meta, err
	}
	if len(ps) != meta.States {
		return nil, meta, fmt.Errorf("saved HMM for %s is incomplete: %d of %d states", symbol, len(ps), meta.States)
	}
	m := &hmm.Model{N: meta.States, D: hmmDims, Pi: make([]float64, meta.States), A: make([][]float64, meta.States), Mu: make([][]float64, meta.States), Cov: make([][][]float64, meta.States)}
	for i, p := range ps {
		m.Pi[i] = p.Pi
		m.Mu[i] = []float64{p.MuRet, p.MuVol}
		m.Cov[i] = [][]float64{{p.VarRet, p.Cov}, {p.Cov, p.VarVol}}
		m.A[i] = make([]float64, meta.States)
	}
	var ts []struct {
		From int     `db:"from_state"`
		To   int     `db:"to_state"`
		Prob float64 `db:"prob"`
	}
	if err := db.Select(&ts, "SELECT from_state, to_state, prob FROM hmm_transition WHERE symbol = ?", symbol); err != nil {
		return nil, meta, err
	}
	if len(ts) != meta.States*meta.States {
		return nil, meta, fmt.Errorf("saved HMM for %s is incomplete: %d transition rows", symbol, len(ts))
	}
	for _, t := range ts {
		m.A[t.From][t.To] = t.Prob
	}
	return m, meta, nil
}

// HMMLatest is the newest prediction of a saved model.
type HMMLatest struct {
	Symbol         string
	Date           string // session the regime is as of (its close)
	Regime         int    // most probable state on Date
	RegimeProb     float64
	Probs          []float64 // filtered P(state | bars through Date)
	NextRegime     int
	NextRegimeProb float64
	NextProbs      []float64 // P(state next session | bars through Date)
	TrainedThrough string
	States         int
}

// ReadHMMLatest returns the newest prediction row of symbol from the model database, read-only.
func ReadHMMLatest(modelDB, symbol string) (HMMLatest, error) {
	var out HMMLatest
	db, err := storage.OpenSQLiteReadOnly(modelDB)
	if err != nil {
		return out, err
	}
	defer db.Close()
	return readHMMLatest(db, symbol)
}

func readHMMLatest(db *sqlx.DB, symbol string) (HMMLatest, error) {
	out := HMMLatest{Symbol: symbol}
	var meta hmmMeta
	if err := db.Get(&meta, "SELECT * FROM hmm_model_meta WHERE symbol = ?", symbol); err != nil {
		return out, fmt.Errorf("no saved HMM for %s: %w", symbol, err)
	}
	var p struct {
		Date    string  `db:"date"`
		Regime  int     `db:"regime"`
		RProb   float64 `db:"regime_prob"`
		NRegime int     `db:"next_regime"`
		NProb   float64 `db:"next_regime_prob"`
	}
	if err := db.Get(&p, "SELECT date, regime, regime_prob, next_regime, next_regime_prob FROM hmm_prediction WHERE symbol = ? ORDER BY date DESC LIMIT 1", symbol); err != nil {
		return out, fmt.Errorf("no HMM predictions for %s: %w", symbol, err)
	}
	var sp []struct {
		Prob float64 `db:"prob"`
		Next float64 `db:"next_prob"`
	}
	if err := db.Select(&sp, "SELECT prob, next_prob FROM hmm_state_prob WHERE symbol = ? AND date = ? ORDER BY state", symbol, p.Date); err != nil {
		return out, err
	}
	out.Date, out.Regime, out.RegimeProb, out.NextRegime, out.NextRegimeProb = p.Date, p.Regime, p.RProb, p.NRegime, p.NProb
	out.TrainedThrough, out.States = meta.TrainedThrough, meta.States
	for _, s := range sp {
		out.Probs = append(out.Probs, s.Prob)
		out.NextProbs = append(out.NextProbs, s.Next)
	}
	return out, nil
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// sourceName is recorded in hmm_model_meta so a model says which bars it was fit on.
const sourceName = dailybars.Source

// ExportHMM writes a self-contained copy of the model database to dst (an existing file is
// replaced), for a program that embeds the trained model. It only reads src.
func ExportHMM(ctx context.Context, src, dst string) error {
	if fi, err := os.Stat(src); err != nil || fi.Size() == 0 {
		return fmt.Errorf("export hmm: model database %s not found or empty (run: train hmm)", src)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	db, err := storage.OpenSQLiteReadOnly(src)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s'", strings.ReplaceAll(dst, "'", "''"))); err != nil {
		return fmt.Errorf("export hmm: %w", err)
	}
	return nil
}
