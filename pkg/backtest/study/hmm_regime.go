package study

import (
	"fmt"
	"math"
	"sort"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

type HMMRegimeStudy struct {
	marketDBPath  string
	resultsDBPath string
	symbol        string
}

func init() {
	Register(&HMMRegimeStudy{})
}

func (s *HMMRegimeStudy) ID() string {
	return "hmm_regime"
}

func (s *HMMRegimeStudy) Name() string {
	return "HMM Regime Detection"
}

func (s *HMMRegimeStudy) Description() string {
	return "Uses a Hidden Markov Model to detect Bull, Sideways, and Bear regimes for a target asset (defaults to QQQ) based on returns."
}

func (s *HMMRegimeStudy) SetDatabases(marketDB, resultsDB string) {
	s.marketDBPath = marketDB
	s.resultsDBPath = resultsDB
}

func (s *HMMRegimeStudy) SetSymbol(sym string) {
	s.symbol = sym
}

func (s *HMMRegimeStudy) Run() error {
	targetSymbol := "QQQ"
	if s.symbol != "" {
		targetSymbol = s.symbol
	}
	fmt.Printf("Running HMM Regime Detection Study for %s...\n", targetSymbol)

	marketDB, err := storage.OpenSQLite(s.marketDBPath)
	if err != nil {
		return fmt.Errorf("failed to open market DB: %w", err)
	}
	defer marketDB.Close()

	barsBySymbol, _, err := storage.FetchBars(marketDB, "backtest_start", []string{targetSymbol}, "1d", "equity")
	if err != nil {
		return fmt.Errorf("failed to fetch bars for %s: %w", targetSymbol, err)
	}
	bars := barsBySymbol[targetSymbol]

	if len(bars) < 100 {
		return fmt.Errorf("not enough bars for %s", targetSymbol)
	}

	sort.Slice(bars, func(i, j int) bool { return bars[i].Date < bars[j].Date })

	var returns []float64
	for i := 1; i < len(bars); i++ {
		ret := math.Log(bars[i].Close / bars[i-1].Close)
		returns = append(returns, ret)
	}

	// Initialize HMM
	hmm := &HMM{
		N:  3,
		Pi: []float64{0.33, 0.33, 0.34},
		A: [][]float64{
			{0.9, 0.05, 0.05},
			{0.05, 0.9, 0.05},
			{0.05, 0.05, 0.9},
		},
		Mu:    []float64{-0.01, 0.0, 0.01}, // Bear, Sideways, Bull initial guesses
		Sigma: []float64{0.02, 0.01, 0.01},
	}

	fmt.Println("Fitting HMM (this may take a moment)...")
	hmm.Fit(returns, 50) // 50 EM iterations

	path := hmm.Viterbi(returns)

	// Save to results DB
	resultsDB, err := sqlx.Connect("sqlite", s.resultsDBPath)
	if err != nil {
		return fmt.Errorf("failed to create results DB: %w", err)
	}
	defer resultsDB.Close()

	_, err = resultsDB.Exec(`
        DROP TABLE IF EXISTS hmm_params;
        DROP TABLE IF EXISTS hmm_transition_matrix;
        DROP TABLE IF EXISTS hmm_regime_history;
        CREATE TABLE hmm_params (
            state INTEGER,
            mean FLOAT,
            variance FLOAT
        );
        CREATE TABLE hmm_transition_matrix (
            from_state INTEGER,
            to_state INTEGER,
            probability FLOAT
        );
        CREATE TABLE hmm_regime_history (
            date TEXT,
            symbol TEXT,
            return FLOAT,
            predicted_state INTEGER
        );
    `)
	if err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	// Insert params
	for i := 0; i < hmm.N; i++ {
		_, err = resultsDB.Exec("INSERT INTO hmm_params (state, mean, variance) VALUES (?, ?, ?)", i, hmm.Mu[i], hmm.Sigma[i]*hmm.Sigma[i])
		if err != nil {
			return fmt.Errorf("failed to insert param: %w", err)
		}
	}

	// Insert transition matrix
	for i := 0; i < hmm.N; i++ {
		for j := 0; j < hmm.N; j++ {
			_, err = resultsDB.Exec("INSERT INTO hmm_transition_matrix (from_state, to_state, probability) VALUES (?, ?, ?)", i, j, hmm.A[i][j])
			if err != nil {
				return fmt.Errorf("failed to insert transition: %w", err)
			}
		}
	}

	// Insert history
	tx, err := resultsDB.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT INTO hmm_regime_history (date, symbol, return, predicted_state) VALUES (?, ?, ?, ?)")
	if err != nil {
		return err
	}
	for i := 0; i < len(returns); i++ {
		_, err = stmt.Exec(bars[i+1].Date, targetSymbol, returns[i], path[i])
		if err != nil {
			return err
		}
	}
	err = stmt.Close()
	if err != nil {
		return err
	}
	err = tx.Commit()
	if err != nil {
		return err
	}

	fmt.Println("HMM Regime Detection completed successfully.")
	return nil
}

type HMM struct {
	N     int
	Pi    []float64
	A     [][]float64
	Mu    []float64
	Sigma []float64
}

func normalPDF(x, mu, sigma float64) float64 {
	if sigma <= 0.0 {
		return 0.0
	}
	return (1.0 / (math.Sqrt(2.0*math.Pi) * sigma)) * math.Exp(-0.5*math.Pow((x-mu)/sigma, 2))
}

// Fit trains the HMM using the Baum-Welch (EM) algorithm.
func (hmm *HMM) Fit(obs []float64, iterations int) {
	T := len(obs)
	if T == 0 {
		return
	}

	for iter := 0; iter < iterations; iter++ {
		// Forward pass (alpha)
		alpha := make([][]float64, T)
		for t := 0; t < T; t++ {
			alpha[t] = make([]float64, hmm.N)
		}
		c := make([]float64, T)

		for i := 0; i < hmm.N; i++ {
			alpha[0][i] = hmm.Pi[i] * normalPDF(obs[0], hmm.Mu[i], hmm.Sigma[i])
			c[0] += alpha[0][i]
		}

		// Scale alpha[0]
		if c[0] > 0 {
			c[0] = 1.0 / c[0]
			for i := 0; i < hmm.N; i++ {
				alpha[0][i] *= c[0]
			}
		} else {
			// Prevent division by zero
			c[0] = 1.0
		}

		for t := 1; t < T; t++ {
			for i := 0; i < hmm.N; i++ {
				sum := 0.0
				for j := 0; j < hmm.N; j++ {
					sum += alpha[t-1][j] * hmm.A[j][i]
				}
				alpha[t][i] = sum * normalPDF(obs[t], hmm.Mu[i], hmm.Sigma[i])
				c[t] += alpha[t][i]
			}
			if c[t] > 0 {
				c[t] = 1.0 / c[t]
				for i := 0; i < hmm.N; i++ {
					alpha[t][i] *= c[t]
				}
			} else {
				c[t] = 1.0
			}
		}

		// Backward pass (beta)
		beta := make([][]float64, T)
		for t := 0; t < T; t++ {
			beta[t] = make([]float64, hmm.N)
		}

		for i := 0; i < hmm.N; i++ {
			beta[T-1][i] = c[T-1]
		}

		for t := T - 2; t >= 0; t-- {
			for i := 0; i < hmm.N; i++ {
				sum := 0.0
				for j := 0; j < hmm.N; j++ {
					sum += hmm.A[i][j] * normalPDF(obs[t+1], hmm.Mu[j], hmm.Sigma[j]) * beta[t+1][j]
				}
				beta[t][i] = sum * c[t]
			}
		}

		// Calculate gamma and xi
		gamma := make([][]float64, T)
		xi := make([][][]float64, T-1)
		for t := 0; t < T; t++ {
			gamma[t] = make([]float64, hmm.N)
			if t < T-1 {
				xi[t] = make([][]float64, hmm.N)
				for i := 0; i < hmm.N; i++ {
					xi[t][i] = make([]float64, hmm.N)
				}
			}
		}

		for t := 0; t < T-1; t++ {
			denom := 0.0
			for i := 0; i < hmm.N; i++ {
				for j := 0; j < hmm.N; j++ {
					denom += alpha[t][i] * hmm.A[i][j] * normalPDF(obs[t+1], hmm.Mu[j], hmm.Sigma[j]) * beta[t+1][j]
				}
			}
			if denom > 0 {
				for i := 0; i < hmm.N; i++ {
					gamma[t][i] = 0.0
					for j := 0; j < hmm.N; j++ {
						xi[t][i][j] = (alpha[t][i] * hmm.A[i][j] * normalPDF(obs[t+1], hmm.Mu[j], hmm.Sigma[j]) * beta[t+1][j]) / denom
						gamma[t][i] += xi[t][i][j]
					}
				}
			}
		}

		// Special case for gamma[T-1]
		denom := 0.0
		for i := 0; i < hmm.N; i++ {
			denom += alpha[T-1][i]
		}
		if denom > 0 {
			for i := 0; i < hmm.N; i++ {
				gamma[T-1][i] = alpha[T-1][i] / denom
			}
		}

		// Update Pi
		for i := 0; i < hmm.N; i++ {
			hmm.Pi[i] = gamma[0][i]
		}

		// Update A
		for i := 0; i < hmm.N; i++ {
			denomA := 0.0
			for t := 0; t < T-1; t++ {
				denomA += gamma[t][i]
			}
			for j := 0; j < hmm.N; j++ {
				numA := 0.0
				for t := 0; t < T-1; t++ {
					numA += xi[t][i][j]
				}
				if denomA > 0 {
					hmm.A[i][j] = numA / denomA
				}
			}
		}

		// Update Mu and Sigma
		for i := 0; i < hmm.N; i++ {
			numMu := 0.0
			denomMu := 0.0
			for t := 0; t < T; t++ {
				numMu += gamma[t][i] * obs[t]
				denomMu += gamma[t][i]
			}
			if denomMu > 0 {
				hmm.Mu[i] = numMu / denomMu
			}

			numSig := 0.0
			for t := 0; t < T; t++ {
				numSig += gamma[t][i] * math.Pow(obs[t]-hmm.Mu[i], 2)
			}
			if denomMu > 0 {
				variance := numSig / denomMu
				if variance < 1e-8 {
					variance = 1e-8 // small minimum variance
				}
				hmm.Sigma[i] = math.Sqrt(variance)
			}
		}
	}
}

// Viterbi path decoding
func (hmm *HMM) Viterbi(obs []float64) []int {
	T := len(obs)
	if T == 0 {
		return nil
	}

	delta := make([][]float64, T)
	psi := make([][]int, T)
	for t := 0; t < T; t++ {
		delta[t] = make([]float64, hmm.N)
		psi[t] = make([]int, hmm.N)
	}

	for i := 0; i < hmm.N; i++ {
		logPi := math.Log(hmm.Pi[i])
		if hmm.Pi[i] == 0 {
			logPi = -math.MaxFloat64
		}
		logPDF := math.Log(normalPDF(obs[0], hmm.Mu[i], hmm.Sigma[i]))
		if normalPDF(obs[0], hmm.Mu[i], hmm.Sigma[i]) == 0 {
			logPDF = -math.MaxFloat64
		}
		delta[0][i] = logPi + logPDF
		psi[0][i] = 0
	}

	for t := 1; t < T; t++ {
		for j := 0; j < hmm.N; j++ {
			maxDelta := -math.MaxFloat64
			maxI := 0
			for i := 0; i < hmm.N; i++ {
				logA := math.Log(hmm.A[i][j])
				if hmm.A[i][j] == 0 {
					logA = -math.MaxFloat64
				}
				val := delta[t-1][i] + logA
				if val > maxDelta {
					maxDelta = val
					maxI = i
				}
			}
			logPDF := math.Log(normalPDF(obs[t], hmm.Mu[j], hmm.Sigma[j]))
			if normalPDF(obs[t], hmm.Mu[j], hmm.Sigma[j]) == 0 {
				logPDF = -math.MaxFloat64
			}
			delta[t][j] = maxDelta + logPDF
			psi[t][j] = maxI
		}
	}

	path := make([]int, T)
	maxDelta := -math.MaxFloat64
	maxI := 0
	for i := 0; i < hmm.N; i++ {
		if delta[T-1][i] > maxDelta {
			maxDelta = delta[T-1][i]
			maxI = i
		}
	}
	path[T-1] = maxI

	for t := T - 2; t >= 0; t-- {
		path[t] = psi[t+1][path[t+1]]
	}
	return path
}
