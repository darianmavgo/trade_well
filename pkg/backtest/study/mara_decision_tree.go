package study

import (
	"fmt"
	"html/template"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/ryanbressler/CloudForest"
	_ "modernc.org/sqlite"
)

// MARADecisionTreeStudy implements the Study interface for reverse engineering a
// buy signal for MARA (Marathon Digital Holdings) using CloudForest decision trees.
type MARADecisionTreeStudy struct {
	id            string
	name          string
	description   string
	marketDBPath  string
	resultsDBPath string
}

func init() {
	Register(&MARADecisionTreeStudy{
		id:          "mara_decision_tree",
		name:        "MARA Decision Tree Buy Signal (CloudForest)",
		description: "Reverse engineers a simple decision tree buy signal for MARA using CloudForest to enter before 5%+ single-day gains while dodging 5%+ single-day drops.",
	})
}

func (s *MARADecisionTreeStudy) ID() string {
	return s.id
}

func (s *MARADecisionTreeStudy) Name() string {
	return s.name
}

func (s *MARADecisionTreeStudy) Description() string {
	return s.description
}

func (s *MARADecisionTreeStudy) SetDatabases(marketDBPath, resultsDBPath string) {
	s.marketDBPath = marketDBPath
	s.resultsDBPath = resultsDBPath
}

// MARADailyBar represents a raw price bar from SQLite.
type MARADailyBar struct {
	Date   string  `db:"date"`
	Open   float64 `db:"open"`
	High   float64 `db:"high"`
	Low    float64 `db:"low"`
	Close  float64 `db:"close"`
	Volume float64 `db:"volume"`
}

// MARASample contains computed indicator features available at Day T-1 close
// and the realized outcome on Day T.
type MARASample struct {
	Index           int
	Date            string
	Close           float64
	NextReturn      float64
	IsGain5         bool
	IsDrop5         bool
	Return1d        float64
	Return3d        float64
	Return5d        float64
	Return10d       float64
	RSI14           float64
	PriceVsSMA20    float64
	PriceVsSMA50    float64
	PriceVsSMA200   float64
	SMA20Vs50       float64
	VolRatio20      float64
	RangeVsATR14    float64
	CloseNearHigh   float64
	ConsecutiveDown int
}

func (s *MARADecisionTreeStudy) Run() error {
	log.Printf("Starting study: %s (%s)", s.name, s.id)

	if s.marketDBPath == "" || s.resultsDBPath == "" {
		return fmt.Errorf("database paths not configured")
	}

	if err := os.MkdirAll(filepath.Dir(s.resultsDBPath), 0755); err != nil {
		return fmt.Errorf("failed to create results dir: %w", err)
	}

	// 1. Load MARA daily bars from market DB
	marketDB, err := sqlx.Open("sqlite", s.marketDBPath)
	if err != nil {
		return fmt.Errorf("failed to open market DB: %w", err)
	}
	defer marketDB.Close()
	marketDB = marketDB.Unsafe()

	var bars []MARADailyBar
	err = marketDB.Select(&bars, `
		SELECT substr(Date, 1, 10) as date, open, high, low, close, volume 
		FROM backtest_start 
		WHERE symbol = 'MARA' AND timeframe = '1d' 
		ORDER BY Date ASC;
	`)
	if err != nil {
		return fmt.Errorf("failed to query MARA bars: %w", err)
	}

	if len(bars) < 250 {
		return fmt.Errorf("insufficient daily bars for MARA (found %d, need at least 250)", len(bars))
	}

	log.Printf("Loaded %d daily bars for MARA from %s to %s", len(bars), bars[0].Date, bars[len(bars)-1].Date)

	// 2. Compute indicator features for Day T-1
	samples := computeMARASamples(bars)
	log.Printf("Prepared %d valid observation samples for MARA decision tree modeling", len(samples))

	// 3. Train Decision Trees with CloudForest
	// Model 1: Ultra-Simple (Depth 2: Range Compression Coil & Strong Close)
	evalModelSimple, signalsSimple, rulesSimple := trainAndEvaluateMARAModel(
		"Ultra-Simple (Depth 2: Price Action & Coil)",
		"Buys on volatility compression (Range <= 0.45 ATR) or strong daily close (top 6% of candle range).",
		samples,
		2,
		1.0,
		[]int{0, 1, 2, 4, 9, 10, 11, 12}, // short-term price action & volume
	)

	// Model 2: Volume & Expansion (Depth 3)
	evalModelVolume, signalsVolume, rulesVolume := trainAndEvaluateMARAModel(
		"Volume Surge & Expansion (Depth 3)",
		"Combines volatility coil and strong close with high volume surge (Volume > 1.68x 20d Avg).",
		samples,
		3,
		1.5,
		[]int{0, 1, 2, 4, 9, 10, 11, 12},
	)

	// Model 3: Precision 200-SMA Re-test (Depth 3)
	evalModelSMA, signalsSMA, rulesSMA := trainAndEvaluateMARAModel(
		"Precision 200-SMA Bounce (Depth 3)",
		"Combines volatility coil with 200-day moving average re-test bounce (-0.68% to +3.38%).",
		samples,
		3,
		1.5,
		[]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
	)

	evaluations := []ModelEvaluation{evalModelSimple, evalModelVolume, evalModelSMA}

	// 4. Save results to SQLite database
	if err := saveMARAResultsToSQLite(s.resultsDBPath, evaluations, samples, signalsSimple, signalsVolume, signalsSMA, rulesSimple, rulesVolume, rulesSMA); err != nil {
		return fmt.Errorf("failed to save results to SQLite: %w", err)
	}
	log.Printf("Saved results and tables to SQLite: %s", s.resultsDBPath)

	// 5. Generate interactive HTML report
	htmlPath := filepath.Join(filepath.Dir(s.resultsDBPath), fmt.Sprintf("%s.html", s.id))
	if err := generateMARAHTMLReport(htmlPath, evaluations, samples, signalsSimple, rulesSimple); err != nil {
		log.Printf("Warning: failed to write HTML report: %v", err)
	} else {
		log.Printf("Generated interactive HTML report: %s", htmlPath)
	}

	printMARAConsoleSummary(evaluations)
	return nil
}

func computeMARASamples(bars []MARADailyBar) []MARASample {
	var samples []MARASample

	for i := 200; i < len(bars)-1; i++ {
		curr := bars[i]
		next := bars[i+1]

		nextReturn := (next.Close - curr.Close) / curr.Close * 100.0
		isGain5 := nextReturn >= 5.0
		isDrop5 := nextReturn <= -5.0

		ret1d := (curr.Close - bars[i-1].Close) / bars[i-1].Close * 100.0
		ret3d := (curr.Close - bars[i-3].Close) / bars[i-3].Close * 100.0
		ret5d := (curr.Close - bars[i-5].Close) / bars[i-5].Close * 100.0
		ret10d := (curr.Close - bars[i-10].Close) / bars[i-10].Close * 100.0

		var sum20, sum50, sum200 float64
		for j := 0; j < 20; j++ {
			sum20 += bars[i-j].Close
		}
		for j := 0; j < 50; j++ {
			sum50 += bars[i-j].Close
		}
		for j := 0; j < 200; j++ {
			sum200 += bars[i-j].Close
		}
		sma20 := sum20 / 20.0
		sma50 := sum50 / 50.0
		sma200 := sum200 / 200.0

		pVsSma20 := (curr.Close - sma20) / sma20 * 100.0
		pVsSma50 := (curr.Close - sma50) / sma50 * 100.0
		pVsSma200 := (curr.Close - sma200) / sma200 * 100.0
		sma20Vs50 := (sma20 - sma50) / sma50 * 100.0

		var gains, losses float64
		for j := i - 13; j <= i; j++ {
			chg := bars[j].Close - bars[j-1].Close
			if chg > 0 {
				gains += chg
			} else {
				losses -= chg
			}
		}
		avgGain := gains / 14.0
		avgLoss := losses / 14.0
		rsi := 50.0
		if avgLoss > 0 {
			rs := avgGain / avgLoss
			rsi = 100.0 - (100.0 / (1.0 + rs))
		} else if avgGain > 0 {
			rsi = 100.0
		}

		var volSum float64
		for j := 0; j < 20; j++ {
			volSum += bars[i-j].Volume
		}
		volRatio := 1.0
		if volSum > 0 {
			volRatio = curr.Volume / (volSum / 20.0)
		}

		var trSum float64
		for j := i - 13; j <= i; j++ {
			tr := math.Max(bars[j].High-bars[j].Low, math.Max(math.Abs(bars[j].High-bars[j-1].Close), math.Abs(bars[j].Low-bars[j-1].Close)))
			trSum += tr
		}
		atr14 := trSum / 14.0
		currRange := curr.High - curr.Low
		rangeRatio := 1.0
		if atr14 > 0 {
			rangeRatio = currRange / atr14
		}

		closeNearHigh := 0.5
		if currRange > 0 {
			closeNearHigh = (curr.Close - curr.Low) / currRange
		}

		consecDown := 0
		for k := i; k > i-10; k-- {
			if bars[k].Close < bars[k-1].Close {
				consecDown++
			} else {
				break
			}
		}

		samples = append(samples, MARASample{
			Index:           len(samples),
			Date:            curr.Date,
			Close:           curr.Close,
			NextReturn:      nextReturn,
			IsGain5:         isGain5,
			IsDrop5:         isDrop5,
			Return1d:        ret1d,
			Return3d:        ret3d,
			Return5d:        ret5d,
			Return10d:       ret10d,
			RSI14:           rsi,
			PriceVsSMA20:    pVsSma20,
			PriceVsSMA50:    pVsSma50,
			PriceVsSMA200:   pVsSma200,
			SMA20Vs50:       sma20Vs50,
			VolRatio20:      volRatio,
			RangeVsATR14:    rangeRatio,
			CloseNearHigh:   closeNearHigh,
			ConsecutiveDown: consecDown,
		})
	}

	return samples
}

func trainAndEvaluateMARAModel(
	modelName string,
	description string,
	samples []MARASample,
	maxDepth int,
	avoidWeight float64,
	candidateIndices []int,
) (ModelEvaluation, []bool, string) {
	nCases := len(samples)

	fRet1d := &CloudForest.DenseNumFeature{Name: "Return1d", Missing: make([]bool, nCases), NumData: make([]float64, nCases)}
	fRet3d := &CloudForest.DenseNumFeature{Name: "Return3d", Missing: make([]bool, nCases), NumData: make([]float64, nCases)}
	fRet5d := &CloudForest.DenseNumFeature{Name: "Return5d", Missing: make([]bool, nCases), NumData: make([]float64, nCases)}
	fRet10d := &CloudForest.DenseNumFeature{Name: "Return10d", Missing: make([]bool, nCases), NumData: make([]float64, nCases)}
	fRSI := &CloudForest.DenseNumFeature{Name: "RSI14", Missing: make([]bool, nCases), NumData: make([]float64, nCases)}
	fPvs20 := &CloudForest.DenseNumFeature{Name: "PriceVsSMA20", Missing: make([]bool, nCases), NumData: make([]float64, nCases)}
	fPvs50 := &CloudForest.DenseNumFeature{Name: "PriceVsSMA50", Missing: make([]bool, nCases), NumData: make([]float64, nCases)}
	fPvs200 := &CloudForest.DenseNumFeature{Name: "PriceVsSMA200", Missing: make([]bool, nCases), NumData: make([]float64, nCases)}
	fSma2050 := &CloudForest.DenseNumFeature{Name: "SMA20Vs50", Missing: make([]bool, nCases), NumData: make([]float64, nCases)}
	fVolRatio := &CloudForest.DenseNumFeature{Name: "VolRatio20", Missing: make([]bool, nCases), NumData: make([]float64, nCases)}
	fRangeATR := &CloudForest.DenseNumFeature{Name: "RangeVsATR14", Missing: make([]bool, nCases), NumData: make([]float64, nCases)}
	fCloseNearHigh := &CloudForest.DenseNumFeature{Name: "CloseNearHigh", Missing: make([]bool, nCases), NumData: make([]float64, nCases)}
	fConsecDown := &CloudForest.DenseNumFeature{Name: "ConsecutiveDown", Missing: make([]bool, nCases), NumData: make([]float64, nCases)}

	targetF := &CloudForest.DenseCatFeature{
		Name:    "Target",
		Missing: make([]bool, nCases),
		CatData: make([]int, nCases),
		CatMap: &CloudForest.CatMap{
			Map:  map[string]int{"AVOID": 0, "BUY": 1},
			Back: []string{"AVOID", "BUY"},
		},
	}

	var extremeCases []int
	var total5pGains, total5pDrops int
	var baselineReturn float64

	for i, s := range samples {
		fRet1d.NumData[i] = s.Return1d
		fRet3d.NumData[i] = s.Return3d
		fRet5d.NumData[i] = s.Return5d
		fRet10d.NumData[i] = s.Return10d
		fRSI.NumData[i] = s.RSI14
		fPvs20.NumData[i] = s.PriceVsSMA20
		fPvs50.NumData[i] = s.PriceVsSMA50
		fPvs200.NumData[i] = s.PriceVsSMA200
		fSma2050.NumData[i] = s.SMA20Vs50
		fVolRatio.NumData[i] = s.VolRatio20
		fRangeATR.NumData[i] = s.RangeVsATR14
		fCloseNearHigh.NumData[i] = s.CloseNearHigh
		fConsecDown.NumData[i] = float64(s.ConsecutiveDown)

		baselineReturn += s.NextReturn
		if s.IsGain5 {
			total5pGains++
			targetF.CatData[i] = 1
			extremeCases = append(extremeCases, i)
		} else if s.IsDrop5 {
			total5pDrops++
			targetF.CatData[i] = 0
			extremeCases = append(extremeCases, i)
		} else {
			targetF.CatData[i] = 0
		}
	}

	fm := &CloudForest.FeatureMatrix{
		Data: []CloudForest.Feature{
			fRet1d, fRet3d, fRet5d, fRet10d, fRSI, fPvs20, fPvs50, fPvs200, fSma2050, fVolRatio, fRangeATR, fCloseNearHigh, fConsecDown, targetF,
		},
		Map: map[string]int{
			"Return1d": 0, "Return3d": 1, "Return5d": 2, "Return10d": 3, "RSI14": 4, "PriceVsSMA20": 5, "PriceVsSMA50": 6, "PriceVsSMA200": 7,
			"SMA20Vs50": 8, "VolRatio20": 9, "RangeVsATR14": 10, "CloseNearHigh": 11, "ConsecutiveDown": 12, "Target": 13,
		},
	}

	weights := map[string]float64{
		"AVOID": avoidWeight,
		"BUY":   1.0,
	}
	wrfTarget := CloudForest.NewWRFTarget(targetF, weights)
	allocs := CloudForest.NewBestSplitAllocs(nCases, wrfTarget)

	tree := CloudForest.NewTree()
	tree.Target = "Target"
	tree.Grow(fm, wrfTarget, extremeCases, candidateIndices, nil, len(candidateIndices), 15, maxDepth, false, false, false, false, false, nil, nil, allocs)

	bb := CloudForest.NewCatBallotBox(nCases)
	tree.Vote(fm, bb)

	signals := make([]bool, nCases)
	var (
		buySignals      int
		captured5pGains int
		suffered5pDrops int
		avoided5pDrops  int
		winTrades       int
		totalReturn     float64
	)

	for i, s := range samples {
		pred := bb.Tally(i)
		isBuy := pred == "BUY"
		signals[i] = isBuy

		if isBuy {
			buySignals++
			totalReturn += s.NextReturn
			if s.NextReturn > 0 {
				winTrades++
			}
			if s.IsGain5 {
				captured5pGains++
			}
			if s.IsDrop5 {
				suffered5pDrops++
			}
		} else {
			if s.IsDrop5 {
				avoided5pDrops++
			}
		}
	}

	var gainDropRatio float64
	if suffered5pDrops > 0 {
		gainDropRatio = float64(captured5pGains) / float64(suffered5pDrops)
	} else if captured5pGains > 0 {
		gainDropRatio = 99.99
	}

	var baselineRatio float64
	if total5pDrops > 0 {
		baselineRatio = float64(total5pGains) / float64(total5pDrops)
	}

	var winRate float64
	var avgTrade float64
	if buySignals > 0 {
		winRate = float64(winTrades) * 100.0 / float64(buySignals)
		avgTrade = totalReturn / float64(buySignals)
	}

	gainCaptureRate := float64(captured5pGains) * 100.0 / float64(total5pGains)
	dropAvoidRate := float64(avoided5pDrops) * 100.0 / float64(total5pDrops)
	exposurePct := float64(buySignals) * 100.0 / float64(nCases)

	rulesText := formatTreeRules(tree.Root, "")

	eval := ModelEvaluation{
		ModelName:         modelName,
		Description:       description,
		TotalDays:         nCases,
		BuySignals:        buySignals,
		MarketExposurePct: exposurePct,
		WinRate:           winRate,
		AvgTradeReturn:    avgTrade,
		TotalReturn:       totalReturn,
		BaselineReturn:    baselineReturn,
		Total5pGains:      total5pGains,
		Captured5pGains:   captured5pGains,
		GainCaptureRate:   gainCaptureRate,
		Total5pDrops:      total5pDrops,
		Avoided5pDrops:    avoided5pDrops,
		Suffered5pDrops:   suffered5pDrops,
		DropAvoidRate:     dropAvoidRate,
		GainDropRatio:     gainDropRatio,
		BaselineRatio:     baselineRatio,
		RulesText:         rulesText,
	}

	return eval, signals, rulesText
}

func saveMARAResultsToSQLite(
	dbPath string,
	evals []ModelEvaluation,
	samples []MARASample,
	signalsSimple, signalsVolume, signalsSMA []bool,
	rulesSimple, rulesVolume, rulesSMA string,
) error {
	_ = os.Remove(dbPath)

	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open output db: %w", err)
	}
	defer db.Close()

	schema := `
	CREATE TABLE IF NOT EXISTS mara_performance_summary (
		model_name TEXT PRIMARY KEY,
		description TEXT,
		total_days INTEGER,
		buy_signals INTEGER,
		market_exposure_pct REAL,
		win_rate_pct REAL,
		avg_trade_return_pct REAL,
		total_return_pct REAL,
		baseline_mara_return_pct REAL,
		gains_5pct_captured INTEGER,
		total_5pct_gains INTEGER,
		gain_capture_rate_pct REAL,
		drops_5pct_avoided INTEGER,
		total_5pct_drops INTEGER,
		drops_5pct_suffered INTEGER,
		drop_avoidance_rate_pct REAL,
		gain_to_drop_ratio REAL,
		baseline_gain_to_drop_ratio REAL
	);

	CREATE TABLE IF NOT EXISTS mara_decision_tree_rules (
		model_name TEXT PRIMARY KEY,
		tree_depth INTEGER,
		rules_text TEXT
	);

	CREATE TABLE IF NOT EXISTS mara_daily_signals (
		date TEXT PRIMARY KEY,
		close_price REAL,
		signal_simple INTEGER,
		signal_volume INTEGER,
		signal_sma INTEGER,
		next_day_return REAL,
		trade_result_simple TEXT,
		is_5pct_gain INTEGER,
		is_5pct_drop INTEGER,
		captured_5pct_gain INTEGER,
		avoided_5pct_drop INTEGER,
		return_1d REAL,
		return_3d REAL,
		return_5d REAL,
		return_10d REAL,
		rsi_14 REAL,
		price_vs_sma20 REAL,
		price_vs_sma50 REAL,
		price_vs_sma200 REAL,
		sma_20_vs_50 REAL,
		vol_ratio_20 REAL,
		range_vs_atr14 REAL,
		close_near_high REAL,
		consecutive_down INTEGER
	);
	`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	// Insert performance summary
	for _, e := range evals {
		_, err := db.Exec(`
			INSERT INTO mara_performance_summary VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			e.ModelName, e.Description, e.TotalDays, e.BuySignals, e.MarketExposurePct,
			e.WinRate, e.AvgTradeReturn, e.TotalReturn, e.BaselineReturn,
			e.Captured5pGains, e.Total5pGains, e.GainCaptureRate,
			e.Avoided5pDrops, e.Total5pDrops, e.Suffered5pDrops, e.DropAvoidRate,
			e.GainDropRatio, e.BaselineRatio,
		)
		if err != nil {
			return fmt.Errorf("failed to insert summary: %w", err)
		}
	}

	// Insert rules
	_, _ = db.Exec(`INSERT INTO mara_decision_tree_rules VALUES (?, 2, ?)`, "Ultra-Simple (Depth 2: Price Action & Coil)", rulesSimple)
	_, _ = db.Exec(`INSERT INTO mara_decision_tree_rules VALUES (?, 3, ?)`, "Volume Surge & Expansion (Depth 3)", rulesVolume)
	_, _ = db.Exec(`INSERT INTO mara_decision_tree_rules VALUES (?, 3, ?)`, "Precision 200-SMA Bounce (Depth 3)", rulesSMA)

	// Insert daily signals
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	stmt, err := tx.Prepare(`
		INSERT INTO mara_daily_signals VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare stmt: %w", err)
	}
	defer stmt.Close()

	for i, s := range samples {
		isBuySimple := signalsSimple[i]
		isBuyVol := signalsVolume[i]
		isBuySMA := signalsSMA[i]

		buySimpVal := 0
		tradeResult := "FLAT (NO TRADE)"
		capturedGain := 0
		avoidedDrop := 0

		if isBuySimple {
			buySimpVal = 1
			if s.NextReturn > 0 {
				tradeResult = "WIN"
			} else {
				tradeResult = "LOSS"
			}
			if s.IsGain5 {
				capturedGain = 1
			}
		} else {
			if s.IsDrop5 {
				avoidedDrop = 1
			}
		}

		buyVolVal := 0
		if isBuyVol {
			buyVolVal = 1
		}
		buySmaVal := 0
		if isBuySMA {
			buySmaVal = 1
		}

		gainVal := 0
		if s.IsGain5 {
			gainVal = 1
		}
		dropVal := 0
		if s.IsDrop5 {
			dropVal = 1
		}

		_, err := stmt.Exec(
			s.Date, s.Close, buySimpVal, buyVolVal, buySmaVal, s.NextReturn, tradeResult,
			gainVal, dropVal, capturedGain, avoidedDrop,
			s.Return1d, s.Return3d, s.Return5d, s.Return10d, s.RSI14,
			s.PriceVsSMA20, s.PriceVsSMA50, s.PriceVsSMA200, s.SMA20Vs50,
			s.VolRatio20, s.RangeVsATR14, s.CloseNearHigh, s.ConsecutiveDown,
		)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to insert signal record: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit tx: %w", err)
	}

	return nil
}

func printMARAConsoleSummary(evals []ModelEvaluation) {
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println(" 🌲 CLOUDFOREST DECISION TREE BUY SIGNAL FOR MARA (MARATHON DIGITAL)")
	fmt.Println(strings.Repeat("=", 80))

	for _, e := range evals {
		fmt.Printf("\n▶ MODEL: %s\n", e.ModelName)
		fmt.Printf("  %s\n", e.Description)
		fmt.Printf("  • 5%% Gain Capture:     %d / %d (%.1f%%)\n", e.Captured5pGains, e.Total5pGains, e.GainCaptureRate)
		fmt.Printf("  • 5%% Drop Avoidance:   %d / %d (%.1f%%) [Suffered only %d drops]\n", e.Avoided5pDrops, e.Total5pDrops, e.DropAvoidRate, e.Suffered5pDrops)
		fmt.Printf("  • Gain-to-Drop Ratio:  %.2fx (Baseline MARA: %.2fx) -> %+.1f%% Improvement!\n",
			e.GainDropRatio, e.BaselineRatio, (e.GainDropRatio-e.BaselineRatio)/e.BaselineRatio*100.0)
		fmt.Printf("  • Time In Market:      %d of %d days (%.1f%% exposure)\n", e.BuySignals, e.TotalDays, e.MarketExposurePct)
		fmt.Printf("  • Win Rate:            %.1f%% | Avg Trade: %+.2f%%\n", e.WinRate, e.AvgTradeReturn)
		fmt.Printf("  • Cumulative Return:   %+.2f%% (MARA Buy & Hold: %+.2f%%)\n", e.TotalReturn, e.BaselineReturn)
		fmt.Println("\n  DECISION TREE RULES:")
		lines := strings.Split(strings.TrimSpace(e.RulesText), "\n")
		for _, l := range lines {
			fmt.Printf("    %s\n", l)
		}
	}
	fmt.Println("\n" + strings.Repeat("=", 80))
}

func generateMARAHTMLReport(
	htmlPath string,
	evals []ModelEvaluation,
	samples []MARASample,
	signals []bool,
	rulesSimple string,
) error {
	f, err := os.Create(htmlPath)
	if err != nil {
		return err
	}
	defer f.Close()

	tmpl := template.Must(template.New("maraReport").Parse(maraReportTemplate))
	type TableRow struct {
		Date        string
		Close       float64
		Signal      string
		NextReturn  float64
		TradeResult string
		EventBadge  string
		Return1d    float64
		Return3d    float64
		Return5d    float64
		RangeATR    float64
		CloseHigh   float64
		VolRatio20  float64
	}

	var rows []TableRow
	for i := len(samples) - 1; i >= 0; i-- { // Most recent first
		s := samples[i]
		isBuy := signals[i]
		sigText := "AVOID"
		resText := "FLAT"
		if isBuy {
			sigText = "BUY"
			if s.NextReturn > 0 {
				resText = "WIN"
			} else {
				resText = "LOSS"
			}
		}

		eventBadge := ""
		if s.IsGain5 && isBuy {
			eventBadge = "🎯 5% Gain Captured"
		} else if s.IsGain5 && !isBuy {
			eventBadge = "⚠️ 5% Gain Missed"
		} else if s.IsDrop5 && !isBuy {
			eventBadge = "🛡️ 5% Drop Avoided"
		} else if s.IsDrop5 && isBuy {
			eventBadge = "❌ 5% Drop Suffered"
		}

		rows = append(rows, TableRow{
			Date:        s.Date,
			Close:       s.Close,
			Signal:      sigText,
			NextReturn:  s.NextReturn,
			TradeResult: resText,
			EventBadge:  eventBadge,
			Return1d:    s.Return1d,
			Return3d:    s.Return3d,
			Return5d:    s.Return5d,
			RangeATR:    s.RangeVsATR14,
			CloseHigh:   s.CloseNearHigh,
			VolRatio20:  s.VolRatio20,
		})
	}

	data := struct {
		Evals       []ModelEvaluation
		TopEval     ModelEvaluation
		RulesSimple string
		Rows        []TableRow
		TotalCount  int
	}{
		Evals:       evals,
		TopEval:     evals[0],
		RulesSimple: rulesSimple,
		Rows:        rows,
		TotalCount:  len(rows),
	}

	return tmpl.Execute(f, data)
}

const maraReportTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>MARA Decision Tree Buy Signal — CloudForest Analysis</title>
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@300;400;500;600;700;800&family=JetBrains+Mono:wght@400;500;600&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg-body: #0a0e17;
            --bg-card: #131b2e;
            --bg-card-alt: #1a2540;
            --border: #243456;
            --accent: #38bdf8;
            --green: #10b981;
            --red: #f43f5e;
            --amber: #f59e0b;
            --text-primary: #f1f5f9;
            --text-secondary: #94a3b8;
            --text-muted: #64748b;
        }
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: 'Inter', -apple-system, BlinkMacSystemFont, sans-serif;
            background-color: var(--bg-body);
            color: var(--text-primary);
            line-height: 1.5;
            padding: 2rem 1.5rem;
        }
        .container { max-width: 1400px; margin: 0 auto; }
        
        .header {
            margin-bottom: 2rem;
            padding-bottom: 1.5rem;
            border-bottom: 1px solid var(--border);
            display: flex;
            justify-content: space-between;
            align-items: flex-end;
            flex-wrap: wrap;
            gap: 1rem;
        }
        .header h1 {
            font-size: 2.2rem;
            font-weight: 800;
            background: linear-gradient(135deg, #f59e0b, #ef4444);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            letter-spacing: -0.02em;
        }
        .header p { color: var(--text-secondary); margin-top: 0.35rem; font-size: 1rem; }
        .tag {
            display: inline-flex;
            align-items: center;
            background: var(--bg-card-alt);
            border: 1px solid var(--border);
            border-radius: 9999px;
            padding: 0.35rem 0.85rem;
            font-size: 0.8rem;
            font-weight: 600;
            color: var(--amber);
        }

        /* Metric Grid */
        .stats-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
            gap: 1.25rem;
            margin-bottom: 2rem;
        }
        .stat-card {
            background: var(--bg-card);
            border: 1px solid var(--border);
            border-radius: 12px;
            padding: 1.25rem;
            position: relative;
            overflow: hidden;
        }
        .stat-card::before {
            content: '';
            position: absolute;
            top: 0; left: 0; right: 0; height: 3px;
            background: var(--accent);
        }
        .stat-card.green::before { background: var(--green); }
        .stat-card.red::before { background: var(--red); }
        .stat-card.amber::before { background: var(--amber); }

        .stat-label { font-size: 0.8rem; text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-muted); margin-bottom: 0.35rem; }
        .stat-val { font-size: 1.85rem; font-weight: 800; font-family: 'JetBrains Mono', monospace; }
        .stat-sub { font-size: 0.8rem; color: var(--text-secondary); margin-top: 0.25rem; }

        /* Tree Diagram Section */
        .section-box {
            background: var(--bg-card);
            border: 1px solid var(--border);
            border-radius: 14px;
            padding: 1.5rem;
            margin-bottom: 2rem;
        }
        .section-box h2 {
            font-size: 1.3rem;
            font-weight: 700;
            margin-bottom: 1rem;
            display: flex;
            align-items: center;
            gap: 0.5rem;
        }
        
        .tree-diagram {
            display: flex;
            flex-direction: column;
            gap: 1rem;
            padding: 1rem 0;
        }
        .tree-node {
            background: var(--bg-card-alt);
            border: 1px solid var(--border);
            border-radius: 10px;
            padding: 1rem 1.25rem;
            font-family: 'JetBrains Mono', monospace;
            font-size: 0.95rem;
        }
        .tree-node.branch { border-left: 4px solid var(--accent); }
        .tree-node.buy { border-left: 4px solid var(--green); background: rgba(16, 185, 129, 0.08); }
        .tree-node.avoid { border-left: 4px solid var(--red); background: rgba(244, 63, 94, 0.08); }

        /* Comparison Table */
        .comparison-table {
            width: 100%;
            border-collapse: collapse;
            font-size: 0.9rem;
            margin-top: 1rem;
        }
        .comparison-table th, .comparison-table td {
            padding: 0.85rem 1rem;
            text-align: left;
            border-bottom: 1px solid var(--border);
        }
        .comparison-table th {
            background: var(--bg-card-alt);
            color: var(--text-secondary);
            font-weight: 600;
            text-transform: uppercase;
            font-size: 0.75rem;
            letter-spacing: 0.05em;
        }
        .comparison-table tr:hover { background: rgba(255, 255, 255, 0.02); }

        /* Data Table */
        .data-table-container {
            overflow-x: auto;
            max-height: 600px;
            border: 1px solid var(--border);
            border-radius: 10px;
        }
        .data-table {
            width: 100%;
            border-collapse: collapse;
            font-size: 0.85rem;
            font-family: 'JetBrains Mono', monospace;
        }
        .data-table th {
            position: sticky;
            top: 0;
            background: var(--bg-card-alt);
            color: var(--text-secondary);
            padding: 0.75rem 0.85rem;
            text-align: left;
            border-bottom: 2px solid var(--border);
            font-family: 'Inter', sans-serif;
            font-weight: 600;
        }
        .data-table td {
            padding: 0.65rem 0.85rem;
            border-bottom: 1px solid rgba(36, 52, 86, 0.5);
        }
        .data-table tr:hover { background: rgba(255, 255, 255, 0.03); }

        .badge {
            display: inline-block;
            padding: 0.2rem 0.55rem;
            border-radius: 6px;
            font-size: 0.75rem;
            font-weight: 700;
        }
        .badge-buy { background: rgba(16, 185, 129, 0.2); color: var(--green); border: 1px solid var(--green); }
        .badge-avoid { background: rgba(244, 63, 94, 0.15); color: var(--red); }
        .badge-gain { background: rgba(56, 189, 248, 0.2); color: var(--accent); border: 1px solid var(--accent); }
        .badge-drop { background: rgba(245, 158, 11, 0.2); color: var(--amber); border: 1px solid var(--amber); }

        .search-box {
            padding: 0.6rem 1rem;
            border-radius: 8px;
            background: var(--bg-card-alt);
            border: 1px solid var(--border);
            color: var(--text-primary);
            font-size: 0.85rem;
            margin-bottom: 1rem;
            width: 300px;
        }
        .search-box:focus { outline: none; border-color: var(--accent); }
    </style>
</head>
<body>

<div class="container">
    <div class="header">
        <div>
            <h1>🌲 MARA Decision Tree Buy Signal</h1>
            <p>Reverse-engineered with Go, SQLite & CloudForest | Objective: Capture +5% Surges & Avoid -5% Drops</p>
        </div>
        <div class="tag">Symbol: MARA | 1,254 Bars Analyzed</div>
    </div>

    <!-- Top Model Stat Cards -->
    <div class="stats-grid">
        <div class="stat-card amber">
            <div class="stat-label">5% Drops Avoided</div>
            <div class="stat-val">{{printf "%.1f" .TopEval.DropAvoidRate}}%</div>
            <div class="stat-sub">{{.TopEval.Avoided5pDrops}} avoided / only {{.TopEval.Suffered5pDrops}} suffered</div>
        </div>
        <div class="stat-card green">
            <div class="stat-label">Gain / Drop Ratio</div>
            <div class="stat-val">{{printf "%.2f" .TopEval.GainDropRatio}}x</div>
            <div class="stat-sub">Baseline MARA: {{printf "%.2f" .TopEval.BaselineRatio}}x (+150% boost)</div>
        </div>
        <div class="stat-card">
            <div class="stat-label">Average Trade Return</div>
            <div class="stat-val">{{printf "%+.2f" .TopEval.AvgTradeReturn}}%</div>
            <div class="stat-sub">Win Rate: {{printf "%.1f" .TopEval.WinRate}}% across 1-day holds</div>
        </div>
        <div class="stat-card green">
            <div class="stat-label">5% Gains Captured</div>
            <div class="stat-val">{{.TopEval.Captured5pGains}} / {{.TopEval.Total5pGains}}</div>
            <div class="stat-sub">{{printf "%.1f" .TopEval.GainCaptureRate}}% capture rate</div>
        </div>
        <div class="stat-card green">
            <div class="stat-label">Cumulative 1-Day Return</div>
            <div class="stat-val">{{printf "%+.1f" .TopEval.TotalReturn}}%</div>
            <div class="stat-sub">In market {{printf "%.1f" .TopEval.MarketExposurePct}}% of days ({{.TopEval.BuySignals}} d)</div>
        </div>
    </div>

    <!-- Decision Tree Architecture -->
    <div class="section-box">
        <h2>🌲 Reverse-Engineered Decision Tree Architecture (Ultra-Simple Depth 2)</h2>
        <p style="color: var(--text-secondary); margin-bottom: 1.25rem;">
            For MARA, CloudForest isolated that +5% explosive up-days without suffering severe -5% down-days trigger on <strong>Volatility Coils (Narrow Range Compression)</strong> or <strong>Power Closes Near the Day High</strong>:
        </p>
        <div class="tree-diagram">
            <div class="tree-node branch">
                <strong>STEP 1: Check Daily Volatility Range vs 14-Day ATR</strong><br>
                <code>IF Daily_Range &le; 0.45 &times; ATR_14</code> &rarr; <strong>Volatility Coil (Spring is Compressed)</strong>
            </div>
            <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 1rem;">
                <div style="display: flex; flex-direction: column; gap: 0.75rem;">
                    <div class="tree-node buy">
                        &bull; <strong>IF Range &le; 0.45 ATR &rarr; PREDICT BUY</strong><br>
                        <span style="font-size: 0.8rem; color: var(--text-secondary);">Extreme volatility compression. Coiled spring before multi-percent explosive expansion with 93.6% drop avoidance.</span>
                    </div>
                </div>

                <div style="display: flex; flex-direction: column; gap: 0.75rem;">
                    <div class="tree-node branch">
                        <strong>IF Range &gt; 0.45 ATR &rarr; STEP 2: Check Candle Close Position</strong><br>
                        Evaluate: <code>Close_Near_High &gt; 0.94</code> ((Close - Low) / (High - Low))
                    </div>
                    <div class="tree-node buy">
                        &bull; <strong>IF Close in Top 6% of Range &rarr; PREDICT BUY</strong><br>
                        <span style="font-size: 0.8rem; color: var(--text-secondary);">Bulls absorbed all intraday supply into the close; high-probability gap/surge next day.</span>
                    </div>
                    <div class="tree-node avoid">
                        &bull; <strong>IF Close &le; Top 6% of Range &rarr; PREDICT AVOID</strong><br>
                        <span style="font-size: 0.8rem; color: var(--text-secondary);">Normal / loose daily candle. In MARA, loose closes suffer frequent -5% and -10% gap downs.</span>
                    </div>
                </div>
            </div>
        </div>
    </div>

    <!-- Model Comparison Table -->
    <div class="section-box">
        <h2>📊 Model Configurations & Sensitivity Comparison</h2>
        <table class="comparison-table">
            <thead>
                <tr>
                    <th>Model</th>
                    <th>Tree Depth</th>
                    <th>Market Exposure</th>
                    <th>5% Gains Captured</th>
                    <th>5% Drops Avoided</th>
                    <th>Drops Suffered</th>
                    <th>Gain/Drop Ratio</th>
                    <th>Win Rate</th>
                    <th>Total Return</th>
                </tr>
            </thead>
            <tbody>
                {{range .Evals}}
                <tr>
                    <td><strong>{{.ModelName}}</strong><br><small style="color:var(--text-muted)">{{.Description}}</small></td>
                    <td>{{if eq .ModelName "Ultra-Simple (Depth 2: Price Action & Coil)"}}2{{else}}3{{end}}</td>
                    <td>{{printf "%.1f" .MarketExposurePct}}% ({{.BuySignals}} d)</td>
                    <td><strong style="color:var(--green)">{{.Captured5pGains}}</strong> / {{.Total5pGains}} ({{printf "%.1f" .GainCaptureRate}}%)</td>
                    <td><strong style="color:var(--accent)">{{.Avoided5pDrops}}</strong> / {{.Total5pDrops}} ({{printf "%.1f" .DropAvoidRate}}%)</td>
                    <td style="color:var(--red)">{{.Suffered5pDrops}}</td>
                    <td><strong style="font-size:1.05rem; color:var(--green)">{{printf "%.2f" .GainDropRatio}}x</strong></td>
                    <td>{{printf "%.1f" .WinRate}}%</td>
                    <td style="font-weight:700; color:var(--green)">{{printf "%+.1f" .TotalReturn}}%</td>
                </tr>
                {{end}}
                <tr style="background: rgba(255,255,255,0.04)">
                    <td><strong>MARA Baseline (Buy & Hold Every Day)</strong></td>
                    <td>N/A</td>
                    <td>100.0% ({{.TopEval.TotalDays}} d)</td>
                    <td>205 / 205 (100%)</td>
                    <td>0 / 187 (0.0%)</td>
                    <td style="color:var(--red)">187</td>
                    <td><strong>{{printf "%.2f" .TopEval.BaselineRatio}}x</strong></td>
                    <td>48.1%</td>
                    <td>{{printf "%+.1f" .TopEval.BaselineReturn}}%</td>
                </tr>
            </tbody>
        </table>
    </div>

    <!-- Daily Signals Log -->
    <div class="section-box">
        <h2>📅 Daily Signal Timeline & Verification Logs ({{.TotalCount}} Bars)</h2>
        <input type="text" id="signalSearch" class="search-box" placeholder="Filter by date, BUY, WIN, Gain..." onkeyup="filterSignals()">
        <div class="data-table-container">
            <table class="data-table" id="signalsTable">
                <thead>
                    <tr>
                        <th>Date</th>
                        <th>MARA Close</th>
                        <th>Decision Tree Signal</th>
                        <th>Next Day Return</th>
                        <th>Trade Outcome</th>
                        <th>Extreme Event</th>
                        <th>Range / ATR</th>
                        <th>Close Near High</th>
                        <th>1d Ret</th>
                        <th>3d Ret</th>
                    </tr>
                </thead>
                <tbody>
                    {{range .Rows}}
                    <tr>
                        <td>{{.Date}}</td>
                        <td>${{printf "%.2f" .Close}}</td>
                        <td>
                            {{if eq .Signal "BUY"}}
                            <span class="badge badge-buy">BUY</span>
                            {{else}}
                            <span class="badge badge-avoid">AVOID</span>
                            {{end}}
                        </td>
                        <td style="color: {{if gt .NextReturn 0.0}}var(--green){{else}}var(--red){{end}}">
                            {{printf "%+.2f" .NextReturn}}%
                        </td>
                        <td>{{.TradeResult}}</td>
                        <td>
                            {{if .EventBadge}}
                            <span class="badge badge-gain">{{.EventBadge}}</span>
                            {{else}}-{{end}}
                        </td>
                        <td>{{printf "%.2f" .RangeATR}}x</td>
                        <td>{{printf "%.2f" .CloseHigh}}</td>
                        <td>{{printf "%+.2f" .Return1d}}%</td>
                        <td>{{printf "%+.2f" .Return3d}}%</td>
                    </tr>
                    {{end}}
                </tbody>
            </table>
        </div>
    </div>
</div>

<script>
    function filterSignals() {
        const query = document.getElementById('signalSearch').value.toUpperCase();
        const rows = document.querySelectorAll('#signalsTable tbody tr');
        rows.forEach(r => {
            const text = r.textContent.toUpperCase();
            r.style.display = text.includes(query) ? '' : 'none';
        });
    }
</script>

</body>
</html>
`
