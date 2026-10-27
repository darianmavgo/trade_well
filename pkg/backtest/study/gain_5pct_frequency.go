package study

import (
	"fmt"
	"html/template"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/olekukonko/tablewriter"
	_ "modernc.org/sqlite"
)

// LeveragedETFInfo stores metadata about known leveraged ETFs/ETNs.
type LeveragedETFInfo struct {
	Symbol      string
	Factor      string
	Category    string
	FundName    string
	IsLeveraged bool
}

// knownLeveragedETFs contains metadata for known leveraged/inverse ETFs.
var knownLeveragedETFs = map[string]LeveragedETFInfo{
	"BITX": {Symbol: "BITX", Factor: "2x Bull", Category: "Leveraged ETF", FundName: "2x Bitcoin Strategy ETF", IsLeveraged: true},
	"CONL": {Symbol: "CONL", Factor: "2x Bull", Category: "Leveraged ETF", FundName: "GraniteShares 2x Long COIN Daily", IsLeveraged: true},
	"DFEN": {Symbol: "DFEN", Factor: "3x Bull", Category: "Leveraged ETF", FundName: "Direxion Daily Aerospace & Defense Bull 3X", IsLeveraged: true},
	"DPST": {Symbol: "DPST", Factor: "3x Bull", Category: "Leveraged ETF", FundName: "Direxion Daily Regional Banks Bull 3X", IsLeveraged: true},
	"FAS":  {Symbol: "FAS", Factor: "3x Bull", Category: "Leveraged ETF", FundName: "Direxion Daily Financial Bull 3X", IsLeveraged: true},
	"FNGU": {Symbol: "FNGU", Factor: "3x Bull", Category: "Leveraged ETN", FundName: "MicroSectors FANG+ Index 3X Leveraged ETN", IsLeveraged: true},
	"GUSH": {Symbol: "GUSH", Factor: "2x Bull", Category: "Leveraged ETF", FundName: "Direxion Daily S&P Oil & Gas Exp & Prod Bull 2X", IsLeveraged: true},
	"LABU": {Symbol: "LABU", Factor: "3x Bull", Category: "Leveraged ETF", FundName: "Direxion Daily S&P Biotech Bull 3X", IsLeveraged: true},
	"NUGT": {Symbol: "NUGT", Factor: "2x Bull", Category: "Leveraged ETF", FundName: "Direxion Daily Gold Miners Index Bull 2X", IsLeveraged: true},
	"NVDL": {Symbol: "NVDL", Factor: "2x Bull", Category: "Leveraged ETF", FundName: "GraniteShares 2x Long NVDA Daily", IsLeveraged: true},
	"SOXL": {Symbol: "SOXL", Factor: "3x Bull", Category: "Leveraged ETF", FundName: "Direxion Daily Semiconductor Bull 3X", IsLeveraged: true},
	"SPXU": {Symbol: "SPXU", Factor: "-3x Bear", Category: "Leveraged Inverse ETF", FundName: "ProShares UltraPro Short S&P 500 (-3x)", IsLeveraged: true},
	"TECL": {Symbol: "TECL", Factor: "3x Bull", Category: "Leveraged ETF", FundName: "Direxion Daily Technology Bull 3X", IsLeveraged: true},
	"TNA":  {Symbol: "TNA", Factor: "3x Bull", Category: "Leveraged ETF", FundName: "Direxion Daily Small Cap Bull 3X", IsLeveraged: true},
	"TQQQ": {Symbol: "TQQQ", Factor: "3x Bull", Category: "Leveraged ETF", FundName: "ProShares UltraPro QQQ (3x)", IsLeveraged: true},
	"TSLL": {Symbol: "TSLL", Factor: "2x Bull", Category: "Leveraged ETF", FundName: "Direxion Daily TSLA Bull 2X", IsLeveraged: true},
	"UVXY": {Symbol: "UVXY", Factor: "1.5x Vol", Category: "Leveraged Volatility ETF", FundName: "ProShares Ultra VIX Short-Term Futures (1.5x)", IsLeveraged: true},
	"UPRO": {Symbol: "UPRO", Factor: "3x Bull", Category: "Leveraged ETF", FundName: "ProShares UltraPro S&P 500 (3x)", IsLeveraged: true},
	"UDOW": {Symbol: "UDOW", Factor: "3x Bull", Category: "Leveraged ETF", FundName: "ProShares UltraPro Dow30 (3x)", IsLeveraged: true},
	"URTY": {Symbol: "URTY", Factor: "3x Bull", Category: "Leveraged ETF", FundName: "ProShares UltraPro Russell2000 (3x)", IsLeveraged: true},
	"SQQQ": {Symbol: "SQQQ", Factor: "-3x Bear", Category: "Leveraged Inverse ETF", FundName: "ProShares UltraPro Short QQQ (-3x)", IsLeveraged: true},
	"SOXS": {Symbol: "SOXS", Factor: "-3x Bear", Category: "Leveraged Inverse ETF", FundName: "Direxion Daily Semiconductor Bear 3X (-3x)", IsLeveraged: true},
	"FAZ":  {Symbol: "FAZ", Factor: "-3x Bear", Category: "Leveraged Inverse ETF", FundName: "Direxion Daily Financial Bear 3X (-3x)", IsLeveraged: true},
	"TZA":  {Symbol: "TZA", Factor: "-3x Bear", Category: "Leveraged Inverse ETF", FundName: "Direxion Daily Small Cap Bear 3X (-3x)", IsLeveraged: true},
	"LABD": {Symbol: "LABD", Factor: "-3x Bear", Category: "Leveraged Inverse ETF", FundName: "Direxion Daily S&P Biotech Bear 3X (-3x)", IsLeveraged: true},
	"DUST": {Symbol: "DUST", Factor: "-2x Bear", Category: "Leveraged Inverse ETF", FundName: "Direxion Daily Gold Miners Bear 2X (-2x)", IsLeveraged: true},
	"SSO":  {Symbol: "SSO", Factor: "2x Bull", Category: "Leveraged ETF", FundName: "ProShares Ultra S&P500 (2x)", IsLeveraged: true},
	"QLD":  {Symbol: "QLD", Factor: "2x Bull", Category: "Leveraged ETF", FundName: "ProShares Ultra QQQ (2x)", IsLeveraged: true},
	"YINN": {Symbol: "YINN", Factor: "3x Bull", Category: "Leveraged ETF", FundName: "Direxion Daily FTSE China Bull 3X", IsLeveraged: true},
	"YANG": {Symbol: "YANG", Factor: "-3x Bear", Category: "Leveraged Inverse ETF", FundName: "Direxion Daily FTSE China Bear 3X", IsLeveraged: true},
	"BOIL": {Symbol: "BOIL", Factor: "2x Bull", Category: "Leveraged ETF", FundName: "ProShares Ultra Bloomberg Natural Gas (2x)", IsLeveraged: true},
	"KOLD": {Symbol: "KOLD", Factor: "-2x Bear", Category: "Leveraged Inverse ETF", FundName: "ProShares UltraShort Bloomberg Natural Gas (-2x)", IsLeveraged: true},
}

// knownStandardETFs contains known 1x unleveraged ETFs.
var knownStandardETFs = map[string]string{
	"SPY":  "SPDR S&P 500 ETF Trust",
	"VOO":  "Vanguard S&P 500 ETF",
	"QQQ":  "Invesco QQQ Trust (Nasdaq-100)",
	"GLD":  "SPDR Gold Shares",
	"USO":  "United States Oil Fund",
	"UTEN": "US Treasury 10 Year Note ETF",
	"IWM":  "iShares Russell 2000 ETF",
	"DIA":  "SPDR Dow Jones Industrial Average ETF",
	"TLT":  "iShares 20+ Year Treasury Bond ETF",
	"XLE":  "Energy Select Sector SPDR Fund",
	"XLF":  "Financial Select Sector SPDR Fund",
	"XLK":  "Technology Select Sector SPDR Fund",
}

// ClassifyTicker inspects a symbol and classifies its leverage and asset category.
func ClassifyTicker(symbol string) LeveragedETFInfo {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if info, ok := knownLeveragedETFs[sym]; ok {
		return info
	}
	if name, ok := knownStandardETFs[sym]; ok {
		return LeveragedETFInfo{
			Symbol:      sym,
			Factor:      "1x Unleveraged",
			Category:    "1x ETF",
			FundName:    name,
			IsLeveraged: false,
		}
	}
	return LeveragedETFInfo{
		Symbol:      sym,
		Factor:      "1x Non-Leveraged",
		Category:    "Stock / Equity",
		FundName:    sym + " Common Stock",
		IsLeveraged: false,
	}
}

// TickerGain5PctStat holds aggregated study results for a single ticker.
type TickerGain5PctStat struct {
	Rank                  int     `db:"rank" json:"rank"`
	Symbol                string  `db:"symbol" json:"symbol"`
	IsLeveragedETF        int     `db:"is_leveraged_etf" json:"is_leveraged_etf"` // 1 = Yes, 0 = No
	LeverageFactor        string  `db:"leverage_factor" json:"leverage_factor"`
	AssetCategory         string  `db:"asset_category" json:"asset_category"`
	FundName              string  `db:"fund_name" json:"fund_name"`
	TotalSessions         int     `db:"total_sessions" json:"total_sessions"`
	StartDate             string  `db:"start_date" json:"start_date"`
	EndDate               string  `db:"end_date" json:"end_date"`
	DaysGain5Pct          int     `db:"days_gain_5pct" json:"days_gain_5pct"`
	Gain5PctFrequency     float64 `db:"gain_5pct_frequency" json:"gain_5pct_frequency"` // percentage %
	DaysDrop5Pct          int     `db:"days_drop_5pct" json:"days_drop_5pct"`
	Drop5PctFrequency     float64 `db:"drop_5pct_frequency" json:"drop_5pct_frequency"` // percentage %
	GainDropRatio         float64 `db:"gain_drop_ratio" json:"gain_drop_ratio"`         // 5% gains / 5% drops
	DaysIntraday5Pct      int     `db:"days_intraday_5pct" json:"days_intraday_5pct"`
	Intraday5PctFrequency float64 `db:"intraday_5pct_frequency" json:"intraday_5pct_frequency"` // percentage %
	DaysIntradayDrop5Pct  int     `db:"days_intraday_drop_5pct" json:"days_intraday_drop_5pct"`
	IntradayDrop5PctFreq  float64 `db:"intraday_drop_5pct_frequency" json:"intraday_drop_5pct_frequency"` // percentage %
	DaysHigh5Pct          int     `db:"days_high_5pct" json:"days_high_5pct"`
	High5PctFrequency     float64 `db:"high_5pct_frequency" json:"high_5pct_frequency"` // percentage %
	MaxDayGainPct         float64 `db:"max_day_gain_pct" json:"max_day_gain_pct"`
	MaxDayDropPct         float64 `db:"max_day_drop_pct" json:"max_day_drop_pct"`
	AvgGainOn5PctDays     float64 `db:"avg_gain_on_5pct_days" json:"avg_gain_on_5pct_days"`
	AvgDropOn5PctDays     float64 `db:"avg_drop_on_5pct_days" json:"avg_drop_on_5pct_days"`
	AvgDailyReturnPct     float64 `db:"avg_daily_return_pct" json:"avg_daily_return_pct"`
	AnnualizedVolPct      float64 `db:"annualized_vol_pct" json:"annualized_vol_pct"`
}

// Gain5PctFrequencyStudy implements the study.Study interface.
type Gain5PctFrequencyStudy struct {
	id            string
	name          string
	description   string
	marketDBPath  string
	resultsDBPath string
}

func init() {
	s := &Gain5PctFrequencyStudy{
		id:          "gain_5pct_frequency",
		name:        "Daily 5% Gain Frequency & Leveraged ETF Study",
		description: "Ranks all available tickers by frequency of >= 5% single-day gains and marks Leveraged ETFs.",
	}
	Register(s)
}

func (s *Gain5PctFrequencyStudy) ID() string {
	return s.id
}

func (s *Gain5PctFrequencyStudy) Name() string {
	return s.name
}

func (s *Gain5PctFrequencyStudy) Description() string {
	return s.description
}

func (s *Gain5PctFrequencyStudy) SetDatabases(marketDBPath, resultsDBPath string) {
	s.marketDBPath = marketDBPath
	s.resultsDBPath = resultsDBPath
}

type rawBar struct {
	Date  string  `db:"date"`
	Open  float64 `db:"open"`
	High  float64 `db:"high"`
	Low   float64 `db:"low"`
	Close float64 `db:"close"`
}

func (s *Gain5PctFrequencyStudy) Run() error {
	fmt.Printf("\n🔬 Starting Study: %s (%s)\n", s.name, s.id)
	fmt.Printf("📂 Reading market data from: %s\n", s.marketDBPath)
	fmt.Printf("💾 Saving study output to:  %s\n\n", s.resultsDBPath)

	if err := os.MkdirAll(filepath.Dir(s.resultsDBPath), 0755); err != nil {
		return fmt.Errorf("failed to create results directory: %w", err)
	}

	srcDB, err := sqlx.Open("sqlite", s.marketDBPath)
	if err != nil {
		return fmt.Errorf("failed to open source market database: %w", err)
	}
	defer srcDB.Close()

	srcDB = srcDB.Unsafe()

	// Query distinct symbols available for timeframe 1d
	var symbols []string
	querySymbols := `
		SELECT DISTINCT symbol 
		FROM backtest_start 
		WHERE timeframe = '1d' 
		ORDER BY symbol ASC;
	`
	if err := srcDB.Select(&symbols, querySymbols); err != nil {
		// Try without timeframe filter in case table has different structure
		altQuery := `SELECT DISTINCT symbol FROM backtest_start ORDER BY symbol ASC;`
		if err2 := srcDB.Select(&symbols, altQuery); err2 != nil {
			return fmt.Errorf("failed to query symbols from backtest_start: %w", err)
		}
	}

	if len(symbols) == 0 {
		return fmt.Errorf("no symbols found in %s backtest_start", s.marketDBPath)
	}

	fmt.Printf("📊 Found %d available tickers. Analyzing daily returns...\n", len(symbols))

	var results []TickerGain5PctStat

	for _, sym := range symbols {
		var bars []rawBar
		q := `
			SELECT Date, open, high, low, close 
			FROM backtest_start 
			WHERE symbol = ? AND timeframe = '1d' 
			ORDER BY Date ASC;
		`
		if err := srcDB.Select(&bars, q, sym); err != nil || len(bars) == 0 {
			// Fallback without timeframe filter
			_ = srcDB.Select(&bars, `SELECT Date, open, high, low, close FROM backtest_start WHERE symbol = ? ORDER BY Date ASC;`, sym)
		}

		if len(bars) < 2 {
			continue
		}

		stat := computeTickerStats(sym, bars)
		results = append(results, stat)
	}

	// Sort results primarily by Gain5PctFrequency descending, then DaysGain5Pct descending
	sort.Slice(results, func(i, j int) bool {
		if math.Abs(results[i].Gain5PctFrequency-results[j].Gain5PctFrequency) > 0.0001 {
			return results[i].Gain5PctFrequency > results[j].Gain5PctFrequency
		}
		if results[i].DaysGain5Pct != results[i].DaysGain5Pct {
			return results[i].DaysGain5Pct > results[i].DaysGain5Pct
		}
		return results[i].MaxDayGainPct > results[j].MaxDayGainPct
	})

	// Assign Ranks
	for i := range results {
		results[i].Rank = i + 1
	}

	// Save to Results SQLite Database
	if err := s.saveResultsToDB(results); err != nil {
		return fmt.Errorf("failed to save study results to database: %w", err)
	}

	// Print ASCII Terminal Table
	s.printTerminalReport(results)

	// Generate Standalone HTML Dashboard
	htmlPath := filepath.Join(filepath.Dir(s.resultsDBPath), fmt.Sprintf("%s.html", s.id))
	if err := s.generateHTMLReport(results, htmlPath); err != nil {
		fmt.Printf("⚠️ Warning: Failed to generate HTML report: %v\n", err)
	} else {
		fmt.Printf("\n🌐 Interactive HTML Dashboard generated at: %s\n", htmlPath)
	}

	return nil
}

func computeTickerStats(sym string, bars []rawBar) TickerGain5PctStat {
	classification := ClassifyTicker(sym)
	isLev := 0
	if classification.IsLeveraged {
		isLev = 1
	}

	totalSessions := len(bars)
	startDate := bars[0].Date
	endDate := bars[len(bars)-1].Date

	var (
		daysGain5Pct         int
		daysDrop5Pct         int
		daysIntraday5Pct     int
		daysIntradayDrop5Pct int
		daysHigh5Pct         int
		maxDayGainPct        float64
		maxDayDropPct        float64
		sum5PctGains         float64
		sum5PctDrops         float64
		dailyReturns         []float64
		sumDailyReturnPct    float64
	)

	// Intraday gain / drop for bar 0
	if bars[0].Open > 0 {
		intra0 := (bars[0].Close - bars[0].Open) / bars[0].Open * 100.0
		if intra0 >= 5.0 {
			daysIntraday5Pct++
		} else if intra0 <= -5.0 {
			daysIntradayDrop5Pct++
		}
	}

	for i := 1; i < len(bars); i++ {
		prevClose := bars[i-1].Close
		currClose := bars[i].Close
		currOpen := bars[i].Open
		currHigh := bars[i].High

		if prevClose <= 0 {
			continue
		}

		dayReturnPct := (currClose - prevClose) / prevClose * 100.0
		dailyReturns = append(dailyReturns, dayReturnPct)
		sumDailyReturnPct += dayReturnPct

		if dayReturnPct > maxDayGainPct {
			maxDayGainPct = dayReturnPct
		}
		if dayReturnPct < maxDayDropPct {
			maxDayDropPct = dayReturnPct
		}

		if dayReturnPct >= 5.0 {
			daysGain5Pct++
			sum5PctGains += dayReturnPct
		} else if dayReturnPct <= -5.0 {
			daysDrop5Pct++
			sum5PctDrops += dayReturnPct
		}

		if currOpen > 0 {
			intraPct := (currClose - currOpen) / currOpen * 100.0
			if intraPct >= 5.0 {
				daysIntraday5Pct++
			} else if intraPct <= -5.0 {
				daysIntradayDrop5Pct++
			}
		}

		highPct := (currHigh - prevClose) / prevClose * 100.0
		if highPct >= 5.0 {
			daysHigh5Pct++
		}
	}

	nReturns := len(dailyReturns)
	var (
		freq5Pct       float64
		freqDrop5Pct   float64
		gainDropRatio  float64
		freqIntraday   float64
		freqIntraDrop  float64
		freqHigh       float64
		avgGainOn5Pct  float64
		avgDropOn5Pct  float64
		avgDailyReturn float64
		annualizedVol  float64
	)

	if nReturns > 0 {
		freq5Pct = float64(daysGain5Pct) * 100.0 / float64(nReturns)
		freqDrop5Pct = float64(daysDrop5Pct) * 100.0 / float64(nReturns)
		freqHigh = float64(daysHigh5Pct) * 100.0 / float64(nReturns)
		avgDailyReturn = sumDailyReturnPct / float64(nReturns)

		// Volatility
		var varianceSum float64
		for _, r := range dailyReturns {
			diff := r - avgDailyReturn
			varianceSum += diff * diff
		}
		if nReturns > 1 {
			stdDev := math.Sqrt(varianceSum / float64(nReturns-1))
			annualizedVol = stdDev * math.Sqrt(252.0)
		}
	}

	if totalSessions > 0 {
		freqIntraday = float64(daysIntraday5Pct) * 100.0 / float64(totalSessions)
		freqIntraDrop = float64(daysIntradayDrop5Pct) * 100.0 / float64(totalSessions)
	}

	if daysGain5Pct > 0 {
		avgGainOn5Pct = sum5PctGains / float64(daysGain5Pct)
	}
	if daysDrop5Pct > 0 {
		avgDropOn5Pct = sum5PctDrops / float64(daysDrop5Pct)
		gainDropRatio = float64(daysGain5Pct) / float64(daysDrop5Pct)
	} else if daysGain5Pct > 0 {
		gainDropRatio = 99.99
	}

	return TickerGain5PctStat{
		Symbol:                sym,
		IsLeveragedETF:        isLev,
		LeverageFactor:        classification.Factor,
		AssetCategory:         classification.Category,
		FundName:              classification.FundName,
		TotalSessions:         totalSessions,
		StartDate:             startDate,
		EndDate:               endDate,
		DaysGain5Pct:          daysGain5Pct,
		Gain5PctFrequency:     math.Round(freq5Pct*100) / 100,
		DaysDrop5Pct:          daysDrop5Pct,
		Drop5PctFrequency:     math.Round(freqDrop5Pct*100) / 100,
		GainDropRatio:         math.Round(gainDropRatio*100) / 100,
		DaysIntraday5Pct:      daysIntraday5Pct,
		Intraday5PctFrequency: math.Round(freqIntraday*100) / 100,
		DaysIntradayDrop5Pct:  daysIntradayDrop5Pct,
		IntradayDrop5PctFreq:  math.Round(freqIntraDrop*100) / 100,
		DaysHigh5Pct:          daysHigh5Pct,
		High5PctFrequency:     math.Round(freqHigh*100) / 100,
		MaxDayGainPct:         math.Round(maxDayGainPct*100) / 100,
		MaxDayDropPct:         math.Round(maxDayDropPct*100) / 100,
		AvgGainOn5PctDays:     math.Round(avgGainOn5Pct*100) / 100,
		AvgDropOn5PctDays:     math.Round(avgDropOn5Pct*100) / 100,
		AvgDailyReturnPct:     math.Round(avgDailyReturn*100) / 100,
		AnnualizedVolPct:      math.Round(annualizedVol*100) / 100,
	}
}

func (s *Gain5PctFrequencyStudy) saveResultsToDB(results []TickerGain5PctStat) error {
	db, err := sqlx.Open("sqlite", s.resultsDBPath)
	if err != nil {
		return fmt.Errorf("open results DB error: %w", err)
	}
	defer db.Close()

	schema := `
	DROP TABLE IF EXISTS gain_5pct_frequency;
	CREATE TABLE gain_5pct_frequency (
		rank INTEGER PRIMARY KEY,
		symbol TEXT NOT NULL,
		is_leveraged_etf INTEGER NOT NULL,
		leverage_factor TEXT NOT NULL,
		asset_category TEXT NOT NULL,
		fund_name TEXT NOT NULL,
		total_sessions INTEGER NOT NULL,
		start_date TEXT NOT NULL,
		end_date TEXT NOT NULL,
		days_gain_5pct INTEGER NOT NULL,
		gain_5pct_frequency REAL NOT NULL,
		days_drop_5pct INTEGER NOT NULL,
		drop_5pct_frequency REAL NOT NULL,
		gain_drop_ratio REAL NOT NULL,
		days_intraday_5pct INTEGER NOT NULL,
		intraday_5pct_frequency REAL NOT NULL,
		days_intraday_drop_5pct INTEGER NOT NULL,
		intraday_drop_5pct_frequency REAL NOT NULL,
		days_high_5pct INTEGER NOT NULL,
		high_5pct_frequency REAL NOT NULL,
		max_day_gain_pct REAL NOT NULL,
		max_day_drop_pct REAL NOT NULL,
		avg_gain_on_5pct_days REAL NOT NULL,
		avg_drop_on_5pct_days REAL NOT NULL,
		avg_daily_return_pct REAL NOT NULL,
		annualized_vol_pct REAL NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_gain5_sym ON gain_5pct_frequency(symbol);
	CREATE INDEX IF NOT EXISTS idx_gain5_is_lev ON gain_5pct_frequency(is_leveraged_etf);
	CREATE INDEX IF NOT EXISTS idx_gain5_freq ON gain_5pct_frequency(gain_5pct_frequency DESC);
	CREATE INDEX IF NOT EXISTS idx_gain5_ratio ON gain_5pct_frequency(gain_drop_ratio DESC);

	DROP TABLE IF EXISTS gain_5pct_summary;
	CREATE TABLE gain_5pct_summary (
		group_name TEXT PRIMARY KEY,
		ticker_count INTEGER NOT NULL,
		avg_5pct_gain_frequency REAL NOT NULL,
		avg_days_up_5pct REAL NOT NULL,
		avg_5pct_drop_frequency REAL NOT NULL,
		avg_days_down_5pct REAL NOT NULL,
		avg_gain_drop_ratio REAL NOT NULL,
		avg_max_gain_pct REAL NOT NULL,
		avg_max_drop_pct REAL NOT NULL,
		avg_annualized_vol_pct REAL NOT NULL
	);
	`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("create tables failed: %w", err)
	}

	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("begin transaction failed: %w", err)
	}

	stmt, err := tx.Preparex(`
		INSERT INTO gain_5pct_frequency (
			rank, symbol, is_leveraged_etf, leverage_factor, asset_category, fund_name,
			total_sessions, start_date, end_date, days_gain_5pct, gain_5pct_frequency,
			days_drop_5pct, drop_5pct_frequency, gain_drop_ratio,
			days_intraday_5pct, intraday_5pct_frequency, days_intraday_drop_5pct, intraday_drop_5pct_frequency,
			days_high_5pct, high_5pct_frequency,
			max_day_gain_pct, max_day_drop_pct, avg_gain_on_5pct_days, avg_drop_on_5pct_days,
			avg_daily_return_pct, annualized_vol_pct
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("prepare insert failed: %w", err)
	}
	defer stmt.Close()

	for _, r := range results {
		_, err := stmt.Exec(
			r.Rank, r.Symbol, r.IsLeveragedETF, r.LeverageFactor, r.AssetCategory, r.FundName,
			r.TotalSessions, r.StartDate, r.EndDate, r.DaysGain5Pct, r.Gain5PctFrequency,
			r.DaysDrop5Pct, r.Drop5PctFrequency, r.GainDropRatio,
			r.DaysIntraday5Pct, r.Intraday5PctFrequency, r.DaysIntradayDrop5Pct, r.IntradayDrop5PctFreq,
			r.DaysHigh5Pct, r.High5PctFrequency,
			r.MaxDayGainPct, r.MaxDayDropPct, r.AvgGainOn5PctDays, r.AvgDropOn5PctDays,
			r.AvgDailyReturnPct, r.AnnualizedVolPct,
		)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert row failed for %s: %w", r.Symbol, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}

	// Populate group summary
	summarySQL := `
		INSERT INTO gain_5pct_summary (
			group_name, ticker_count, avg_5pct_gain_frequency, avg_days_up_5pct,
			avg_5pct_drop_frequency, avg_days_down_5pct, avg_gain_drop_ratio,
			avg_max_gain_pct, avg_max_drop_pct, avg_annualized_vol_pct
		)
		SELECT 
			CASE WHEN is_leveraged_etf = 1 THEN 'Leveraged ETFs' ELSE 'Equities & 1x ETFs' END as group_name,
			COUNT(*) as ticker_count,
			ROUND(AVG(gain_5pct_frequency), 2) as avg_5pct_gain_frequency,
			ROUND(AVG(days_gain_5pct), 1) as avg_days_up_5pct,
			ROUND(AVG(drop_5pct_frequency), 2) as avg_5pct_drop_frequency,
			ROUND(AVG(days_drop_5pct), 1) as avg_days_down_5pct,
			ROUND(AVG(gain_drop_ratio), 2) as avg_gain_drop_ratio,
			ROUND(AVG(max_day_gain_pct), 2) as avg_max_gain_pct,
			ROUND(AVG(max_day_drop_pct), 2) as avg_max_drop_pct,
			ROUND(AVG(annualized_vol_pct), 2) as avg_annualized_vol_pct
		FROM gain_5pct_frequency
		GROUP BY is_leveraged_etf;

		INSERT INTO gain_5pct_summary (
			group_name, ticker_count, avg_5pct_gain_frequency, avg_days_up_5pct,
			avg_5pct_drop_frequency, avg_days_down_5pct, avg_gain_drop_ratio,
			avg_max_gain_pct, avg_max_drop_pct, avg_annualized_vol_pct
		)
		SELECT 
			'All Tickers Combined' as group_name,
			COUNT(*) as ticker_count,
			ROUND(AVG(gain_5pct_frequency), 2) as avg_5pct_gain_frequency,
			ROUND(AVG(days_gain_5pct), 1) as avg_days_up_5pct,
			ROUND(AVG(drop_5pct_frequency), 2) as avg_5pct_drop_frequency,
			ROUND(AVG(days_drop_5pct), 1) as avg_days_down_5pct,
			ROUND(AVG(gain_drop_ratio), 2) as avg_gain_drop_ratio,
			ROUND(AVG(max_day_gain_pct), 2) as avg_max_gain_pct,
			ROUND(AVG(max_day_drop_pct), 2) as avg_max_drop_pct,
			ROUND(AVG(annualized_vol_pct), 2) as avg_annualized_vol_pct
		FROM gain_5pct_frequency;
	`
	_, _ = db.Exec(summarySQL)
	_, _ = db.Exec(summarySQL)

	return nil
}

func (s *Gain5PctFrequencyStudy) printTerminalReport(results []TickerGain5PctStat) {
	fmt.Printf("\n🏆 STUDY RESULTS: Tickers Ranked by 5%% Daily Gain Frequency\n")
	fmt.Printf("========================================================================================================================\n")
	fmt.Printf("Key: [⚡ LEV ETF] = Leveraged ETF/ETN | [ 1x ETF ] = Unleveraged ETF | [ EQUITY ] = Common Stock\n\n")

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{
		"Rank", "Symbol", "Type", "Leverage", "Gain 5%+ (%)", "Days Up", "Drop 5%+ (%)", "Days Down", "Ratio (G/D)", "Max Gain", "Max Drop",
	})
	table.SetBorder(true)
	table.SetAutoWrapText(false)

	var levCount, nonLevCount int
	var levGainFreqSum, nonLevGainFreqSum float64
	var levDropFreqSum, nonLevDropFreqSum float64
	var levRatioSum, nonLevRatioSum float64

	for _, r := range results {
		typeBadge := "EQUITY"
		if r.IsLeveragedETF == 1 {
			typeBadge = "⚡ LEV ETF"
			levCount++
			levGainFreqSum += r.Gain5PctFrequency
			levDropFreqSum += r.Drop5PctFrequency
			levRatioSum += r.GainDropRatio
		} else if r.AssetCategory == "1x ETF" {
			typeBadge = "1x ETF"
			nonLevCount++
			nonLevGainFreqSum += r.Gain5PctFrequency
			nonLevDropFreqSum += r.Drop5PctFrequency
			nonLevRatioSum += r.GainDropRatio
		} else {
			nonLevCount++
			nonLevGainFreqSum += r.Gain5PctFrequency
			nonLevDropFreqSum += r.Drop5PctFrequency
			nonLevRatioSum += r.GainDropRatio
		}

		table.Append([]string{
			fmt.Sprintf("#%d", r.Rank),
			r.Symbol,
			typeBadge,
			r.LeverageFactor,
			fmt.Sprintf("%.2f%%", r.Gain5PctFrequency),
			fmt.Sprintf("%d", r.DaysGain5Pct),
			fmt.Sprintf("%.2f%%", r.Drop5PctFrequency),
			fmt.Sprintf("%d", r.DaysDrop5Pct),
			fmt.Sprintf("%.2fx", r.GainDropRatio),
			fmt.Sprintf("+%.2f%%", r.MaxDayGainPct),
			fmt.Sprintf("%.2f%%", r.MaxDayDropPct),
		})
	}

	table.Render()

	fmt.Printf("\n📈 Summary Comparison:\n")
	if levCount > 0 {
		fmt.Printf("  • Leveraged ETFs (%d tickers): Avg Gain 5%%+ = %.2f%% | Avg Drop 5%%+ = %.2f%% | Avg Gain/Drop Ratio = %.2fx\n",
			levCount, levGainFreqSum/float64(levCount), levDropFreqSum/float64(levCount), levRatioSum/float64(levCount))
	}
	if nonLevCount > 0 {
		fmt.Printf("  • Equities / 1x ETFs (%d tickers): Avg Gain 5%%+ = %.2f%% | Avg Drop 5%%+ = %.2f%% | Avg Gain/Drop Ratio = %.2fx\n",
			nonLevCount, nonLevGainFreqSum/float64(nonLevCount), nonLevDropFreqSum/float64(nonLevCount), nonLevRatioSum/float64(nonLevCount))
	}
	fmt.Printf("========================================================================================================================\n")
}

func (s *Gain5PctFrequencyStudy) generateHTMLReport(results []TickerGain5PctStat, outputPath string) error {
	var (
		levCount, nonLevCount         int
		levGainFreqSum, nonLevGainSum float64
		levDropFreqSum, nonLevDropSum float64
		levRatioSum, nonLevRatioSum   float64
		total5PctDays                 int
		totalDrop5PctDays             int
		highestRatioTicker            string
		highestRatioVal               float64
	)

	for _, r := range results {
		total5PctDays += r.DaysGain5Pct
		totalDrop5PctDays += r.DaysDrop5Pct
		if r.IsLeveragedETF == 1 {
			levCount++
			levGainFreqSum += r.Gain5PctFrequency
			levDropFreqSum += r.Drop5PctFrequency
			levRatioSum += r.GainDropRatio
		} else {
			nonLevCount++
			nonLevGainSum += r.Gain5PctFrequency
			nonLevDropSum += r.Drop5PctFrequency
			nonLevRatioSum += r.GainDropRatio
		}

		if r.DaysDrop5Pct > 0 && r.GainDropRatio > highestRatioVal {
			highestRatioVal = r.GainDropRatio
			highestRatioTicker = r.Symbol
		}
	}

	levAvgGainFreq := 0.0
	levAvgDropFreq := 0.0
	levAvgRatio := 0.0
	if levCount > 0 {
		levAvgGainFreq = levGainFreqSum / float64(levCount)
		levAvgDropFreq = levDropFreqSum / float64(levCount)
		levAvgRatio = levRatioSum / float64(levCount)
	}
	nonLevAvgGainFreq := 0.0
	nonLevAvgDropFreq := 0.0
	nonLevAvgRatio := 0.0
	if nonLevCount > 0 {
		nonLevAvgGainFreq = nonLevGainSum / float64(nonLevCount)
		nonLevAvgDropFreq = nonLevDropSum / float64(nonLevCount)
		nonLevAvgRatio = nonLevRatioSum / float64(nonLevCount)
	}

	topTicker := "N/A"
	topFreq := 0.0
	if len(results) > 0 {
		topTicker = results[0].Symbol
		topFreq = results[0].Gain5PctFrequency
	}

	data := struct {
		GeneratedAt        string
		TotalTickers       int
		LevCount           int
		NonLevCount        int
		LevAvgGainFreq     float64
		LevAvgDropFreq     float64
		LevAvgRatio        float64
		NonLevAvgGainFreq  float64
		NonLevAvgDropFreq  float64
		NonLevAvgRatio     float64
		Total5PctDays      int
		TotalDrop5PctDays  int
		TopTicker          string
		TopFreq            float64
		HighestRatioTicker string
		HighestRatioVal    float64
		Results            []TickerGain5PctStat
	}{
		GeneratedAt:        time.Now().Format("2006-01-02 15:04:05 MST"),
		TotalTickers:       len(results),
		LevCount:           levCount,
		NonLevCount:        nonLevCount,
		LevAvgGainFreq:     math.Round(levAvgGainFreq*100) / 100,
		LevAvgDropFreq:     math.Round(levAvgDropFreq*100) / 100,
		LevAvgRatio:        math.Round(levAvgRatio*100) / 100,
		NonLevAvgGainFreq:  math.Round(nonLevAvgGainFreq*100) / 100,
		NonLevAvgDropFreq:  math.Round(nonLevAvgDropFreq*100) / 100,
		NonLevAvgRatio:     math.Round(nonLevAvgRatio*100) / 100,
		Total5PctDays:      total5PctDays,
		TotalDrop5PctDays:  totalDrop5PctDays,
		TopTicker:          topTicker,
		TopFreq:            topFreq,
		HighestRatioTicker: highestRatioTicker,
		HighestRatioVal:    highestRatioVal,
		Results:            results,
	}

	funcMap := template.FuncMap{
		"min": func(a, b float64) float64 {
			return math.Min(a, b)
		},
		"mult": func(a, b float64) float64 {
			return a * b
		},
	}

	tmpl, err := template.New("htmlReport").Funcs(funcMap).Parse(htmlReportTemplate)
	if err != nil {
		return fmt.Errorf("template parse error: %w", err)
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create file error: %w", err)
	}
	defer f.Close()

	return tmpl.Execute(f, data)
}

const htmlReportTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Daily 5% Gain Frequency & Leveraged ETF Study</title>
    
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@300;400;500;600;700;800&family=JetBrains+Mono:wght@400;500;600&display=swap" rel="stylesheet">

    <style>
        :root {
            --bg-body: #0b0f19;
            --bg-card: #131b2e;
            --bg-card-subtle: #1a243b;
            --border-card: #22304d;
            --text-main: #f1f5f9;
            --text-muted: #94a3b8;
            --text-subtle: #64748b;
            --accent-blue: #38bdf8;
            --accent-emerald: #10b981;
            --accent-amber: #f59e0b;
            --accent-rose: #f43f5e;
            --accent-purple: #a855f7;
            --lev-badge-bg: rgba(245, 158, 11, 0.15);
            --lev-badge-text: #fbbf24;
            --lev-badge-border: rgba(245, 158, 11, 0.35);
            --stock-badge-bg: rgba(56, 189, 248, 0.12);
            --stock-badge-text: #38bdf8;
            --stock-badge-border: rgba(56, 189, 248, 0.25);
        }

        * { box-sizing: border-box; margin: 0; padding: 0; }

        body {
            background-color: var(--bg-body);
            color: var(--text-main);
            font-family: 'Inter', -apple-system, BlinkMacSystemFont, sans-serif;
            line-height: 1.6;
            padding: 2.5rem 1.5rem;
            min-height: 100vh;
        }

        .max-container {
            max-width: 1300px;
            margin: 0 auto;
        }

        .hero {
            background: linear-gradient(135deg, rgba(30, 41, 59, 0.8) 0%, rgba(15, 23, 42, 0.95) 100%);
            border: 1px solid var(--border-card);
            border-radius: 1.25rem;
            padding: 2.5rem;
            margin-bottom: 2rem;
            box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.5);
        }

        .badge-pill {
            display: inline-flex;
            align-items: center;
            gap: 0.5rem;
            background: rgba(56, 189, 248, 0.1);
            color: var(--accent-blue);
            border: 1px solid rgba(56, 189, 248, 0.3);
            font-family: 'JetBrains Mono', monospace;
            font-size: 0.825rem;
            padding: 0.35rem 0.85rem;
            border-radius: 9999px;
            margin-bottom: 1rem;
        }

        .hero h1 {
            font-size: 2.25rem;
            font-weight: 800;
            margin-bottom: 0.75rem;
            letter-spacing: -0.025em;
            background: linear-gradient(to right, #f1f5f9, #94a3b8);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
        }

        .hero p {
            color: var(--text-muted);
            font-size: 1.05rem;
            max-width: 850px;
        }

        .meta-info {
            margin-top: 1rem;
            font-size: 0.85rem;
            color: var(--text-subtle);
            font-family: 'JetBrains Mono', monospace;
        }

        .stats-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(230px, 1fr));
            gap: 1.25rem;
            margin-bottom: 2rem;
        }

        .stat-card {
            background: var(--bg-card);
            border: 1px solid var(--border-card);
            border-radius: 1rem;
            padding: 1.5rem;
            box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.2);
            transition: transform 0.2s, border-color 0.2s;
        }

        .stat-card:hover {
            transform: translateY(-2px);
            border-color: #334155;
        }

        .stat-label {
            color: var(--text-muted);
            font-size: 0.85rem;
            font-weight: 500;
            text-transform: uppercase;
            letter-spacing: 0.05em;
            margin-bottom: 0.5rem;
        }

        .stat-val {
            font-size: 2rem;
            font-weight: 700;
            color: var(--text-main);
            font-family: 'JetBrains Mono', monospace;
        }

        .stat-sub {
            color: var(--text-subtle);
            font-size: 0.85rem;
            margin-top: 0.35rem;
        }

        .filter-bar {
            display: flex;
            flex-wrap: wrap;
            align-items: center;
            justify-content: space-between;
            gap: 1rem;
            background: var(--bg-card);
            border: 1px solid var(--border-card);
            border-radius: 1rem;
            padding: 1rem 1.5rem;
            margin-bottom: 1.5rem;
        }

        .filter-buttons {
            display: flex;
            gap: 0.5rem;
        }

        .btn-filter {
            background: var(--bg-card-subtle);
            color: var(--text-muted);
            border: 1px solid var(--border-card);
            padding: 0.5rem 1rem;
            border-radius: 0.5rem;
            font-size: 0.875rem;
            font-weight: 500;
            cursor: pointer;
            transition: all 0.2s;
        }

        .btn-filter:hover {
            color: var(--text-main);
            border-color: #475569;
        }

        .btn-filter.active {
            background: var(--accent-blue);
            color: #0b0f19;
            border-color: var(--accent-blue);
            font-weight: 600;
        }

        .search-box {
            background: var(--bg-card-subtle);
            border: 1px solid var(--border-card);
            border-radius: 0.5rem;
            padding: 0.5rem 1rem;
            color: var(--text-main);
            font-family: 'Inter', sans-serif;
            font-size: 0.875rem;
            width: 250px;
            outline: none;
            transition: border-color 0.2s;
        }

        .search-box:focus {
            border-color: var(--accent-blue);
        }

        .table-card {
            background: var(--bg-card);
            border: 1px solid var(--border-card);
            border-radius: 1.25rem;
            padding: 1.5rem;
            box-shadow: 0 10px 15px -3px rgba(0, 0, 0, 0.3);
            overflow-x: auto;
        }

        table {
            width: 100%;
            border-collapse: collapse;
            text-align: left;
            font-size: 0.925rem;
        }

        th {
            background: var(--bg-card-subtle);
            color: var(--text-muted);
            padding: 1rem 0.85rem;
            font-weight: 600;
            text-transform: uppercase;
            font-size: 0.775rem;
            letter-spacing: 0.05em;
            border-bottom: 2px solid var(--border-card);
            cursor: pointer;
            user-select: none;
        }

        th:hover {
            color: var(--text-main);
        }

        td {
            padding: 0.95rem 0.85rem;
            border-bottom: 1px solid #1e293b;
            color: var(--text-muted);
        }

        tr:hover td {
            background: rgba(255, 255, 255, 0.02);
            color: var(--text-main);
        }

        .sym-cell {
            font-family: 'JetBrains Mono', monospace;
            font-weight: 700;
            color: var(--text-main);
            font-size: 1.05rem;
        }

        .badge-lev {
            display: inline-flex;
            align-items: center;
            gap: 0.35rem;
            padding: 0.25rem 0.6rem;
            border-radius: 0.375rem;
            background: var(--lev-badge-bg);
            color: var(--lev-badge-text);
            border: 1px solid var(--lev-badge-border);
            font-weight: 600;
            font-size: 0.75rem;
        }

        .badge-stock {
            display: inline-flex;
            align-items: center;
            gap: 0.35rem;
            padding: 0.25rem 0.6rem;
            border-radius: 0.375rem;
            background: var(--stock-badge-bg);
            color: var(--stock-badge-text);
            border: 1px solid var(--stock-badge-border);
            font-weight: 500;
            font-size: 0.75rem;
        }

        .num-val {
            font-family: 'JetBrains Mono', monospace;
            font-weight: 600;
        }

        .freq-highlight {
            color: var(--accent-emerald);
            font-size: 1.05rem;
        }

        .drop-highlight {
            color: var(--accent-rose);
            font-size: 1.05rem;
        }

        .ratio-badge {
            display: inline-flex;
            align-items: center;
            padding: 0.25rem 0.6rem;
            border-radius: 0.375rem;
            font-family: 'JetBrains Mono', monospace;
            font-weight: 700;
            font-size: 0.85rem;
        }

        .ratio-high {
            background: rgba(16, 185, 129, 0.15);
            color: #34d399;
            border: 1px solid rgba(16, 185, 129, 0.35);
        }

        .ratio-low {
            background: rgba(244, 63, 94, 0.15);
            color: #fb7185;
            border: 1px solid rgba(244, 63, 94, 0.35);
        }

        .ratio-even {
            background: rgba(148, 163, 184, 0.15);
            color: #cbd5e1;
            border: 1px solid rgba(148, 163, 184, 0.35);
        }

        .freq-bar-bg {
            width: 100%;
            height: 6px;
            background: #1e293b;
            border-radius: 3px;
            margin-top: 4px;
            overflow: hidden;
        }

        .freq-bar-fill {
            height: 100%;
            background: linear-gradient(90deg, #10b981, #38bdf8);
            border-radius: 3px;
        }

        .freq-bar-fill.lev {
            background: linear-gradient(90deg, #f59e0b, #ec4899);
        }

        .freq-bar-fill.drop {
            background: linear-gradient(90deg, #f43f5e, #fb923c);
        }

        .footer {
            margin-top: 3rem;
            text-align: center;
            color: var(--text-subtle);
            font-size: 0.85rem;
        }
    </style>
</head>
<body>

<div class="max-container">

    <div class="hero">
        <div class="badge-pill">🔬 Quantitative Study • backtestgosqlite</div>
        <h1>Daily 5% Gain vs 5% Drop Frequency & Asymmetry Study</h1>
        <p>
            Comprehensive analysis ranking all available tickers by how often they gain $\ge 5\%$ or drop $\le -5\%$ in a single trading day, and calculating the <strong>Gain-to-Drop Ratio</strong> (%5 Gains / %5 Drops). Leveraged ETFs are explicitly tagged to distinguish structural leverage decay from organic equity skew.
        </p>
        <div class="meta-info">
            Generated: {{ .GeneratedAt }} | Evaluated: {{ .TotalTickers }} Tickers
        </div>
    </div>

    <div class="stats-grid">
        <div class="stat-card">
            <div class="stat-label">Total Tickers</div>
            <div class="stat-val">{{ .TotalTickers }}</div>
            <div class="stat-sub">{{ .LevCount }} Leveraged ETFs • {{ .NonLevCount }} Equities</div>
        </div>

        <div class="stat-card">
            <div class="stat-label">Lev ETF Avg 5%+ Gain / Drop</div>
            <div class="stat-val" style="color: var(--accent-amber);">{{ .LevAvgGainFreq }}% / {{ .LevAvgDropFreq }}%</div>
            <div class="stat-sub">Avg Ratio: {{ .LevAvgRatio }}x ({{ .LevCount }} funds)</div>
        </div>

        <div class="stat-card">
            <div class="stat-label">Stock & 1x ETF Avg Gain / Drop</div>
            <div class="stat-val" style="color: var(--accent-blue);">{{ .NonLevAvgGainFreq }}% / {{ .NonLevAvgDropFreq }}%</div>
            <div class="stat-sub">Avg Ratio: {{ .NonLevAvgRatio }}x ({{ .NonLevCount }} assets)</div>
        </div>

        <div class="stat-card">
            <div class="stat-label">Top Gain/Drop Ratio Ticker</div>
            <div class="stat-val" style="color: var(--accent-emerald);">{{ .HighestRatioTicker }} ({{ .HighestRatioVal }}x)</div>
            <div class="stat-sub">#1 Gain Freq: {{ .TopTicker }} ({{ .TopFreq }}%)</div>
        </div>
    </div>

    <div class="filter-bar">
        <div class="filter-buttons">
            <button class="btn-filter active" onclick="filterTable('all')">All Tickers ({{ .TotalTickers }})</button>
            <button class="btn-filter" onclick="filterTable('lev')">⚡ Leveraged ETFs Only ({{ .LevCount }})</button>
            <button class="btn-filter" onclick="filterTable('stock')">Stocks & 1x ETFs ({{ .NonLevCount }})</button>
        </div>
        <input type="text" id="searchInput" class="search-box" placeholder="Search ticker or name..." onkeyup="searchTable()">
    </div>

    <div class="table-card">
        <table id="studyTable">
            <thead>
                <tr>
                    <th onclick="sortTable(0, 'num')">Rank ↕</th>
                    <th onclick="sortTable(1, 'str')">Symbol ↕</th>
                    <th onclick="sortTable(2, 'str')">Type / Leverage ↕</th>
                    <th onclick="sortTable(3, 'str')">Asset Name ↕</th>
                    <th onclick="sortTable(4, 'num')">Gain 5%+ Freq ↕</th>
                    <th onclick="sortTable(5, 'num')">Days Up ↕</th>
                    <th onclick="sortTable(6, 'num')">Drop 5%+ Freq ↕</th>
                    <th onclick="sortTable(7, 'num')">Days Down ↕</th>
                    <th onclick="sortTable(8, 'num')">Gain/Drop Ratio ↕</th>
                    <th onclick="sortTable(9, 'num')">Max Gain ↕</th>
                    <th onclick="sortTable(10, 'num')">Max Drop ↕</th>
                    <th onclick="sortTable(11, 'num')">Sessions ↕</th>
                </tr>
            </thead>
            <tbody>
                {{ range .Results }}
                <tr data-is-lev="{{ .IsLeveragedETF }}">
                    <td class="num-val">#{{ .Rank }}</td>
                    <td class="sym-cell">{{ .Symbol }}</td>
                    <td>
                        {{ if eq .IsLeveragedETF 1 }}
                            <span class="badge-lev">⚡ {{ .LeverageFactor }}</span>
                        {{ else if eq .AssetCategory "1x ETF" }}
                            <span class="badge-stock">1x ETF</span>
                        {{ else }}
                            <span class="badge-stock">{{ .AssetCategory }}</span>
                        {{ end }}
                    </td>
                    <td style="color: var(--text-main);">{{ .FundName }}</td>
                    <td>
                        <span class="num-val freq-highlight">{{ printf "%.2f" .Gain5PctFrequency }}%</span>
                        <div class="freq-bar-bg">
                            <div class="freq-bar-fill {{ if eq .IsLeveragedETF 1 }}lev{{ end }}" style="width: {{ printf "%.1f" (min 100.0 (mult .Gain5PctFrequency 3.5)) }}%;"></div>
                        </div>
                    </td>
                    <td class="num-val">{{ .DaysGain5Pct }}</td>
                    <td>
                        <span class="num-val drop-highlight">{{ printf "%.2f" .Drop5PctFrequency }}%</span>
                        <div class="freq-bar-bg">
                            <div class="freq-bar-fill drop" style="width: {{ printf "%.1f" (min 100.0 (mult .Drop5PctFrequency 3.5)) }}%;"></div>
                        </div>
                    </td>
                    <td class="num-val">{{ .DaysDrop5Pct }}</td>
                    <td>
                        {{ if gt .GainDropRatio 1.05 }}
                            <span class="ratio-badge ratio-high">{{ printf "%.2f" .GainDropRatio }}x</span>
                        {{ else if lt .GainDropRatio 0.95 }}
                            <span class="ratio-badge ratio-low">{{ printf "%.2f" .GainDropRatio }}x</span>
                        {{ else }}
                            <span class="ratio-badge ratio-even">{{ printf "%.2f" .GainDropRatio }}x</span>
                        {{ end }}
                    </td>
                    <td class="num-val" style="color: var(--accent-emerald);">+{{ printf "%.2f" .MaxDayGainPct }}%</td>
                    <td class="num-val" style="color: var(--accent-rose);">{{ printf "%.2f" .MaxDayDropPct }}%</td>
                    <td class="num-val">{{ .TotalSessions }}</td>
                </tr>
                {{ end }}
            </tbody>
        </table>
    </div>

    <div class="footer">
        <p>Generated by <code>study.Gain5PctFrequencyStudy</code> • backtestgosqlite Study Engine</p>
    </div>

</div>

<script>
    let currentFilter = 'all';

    function filterTable(type) {
        currentFilter = type;
        const buttons = document.querySelectorAll('.btn-filter');
        buttons.forEach(btn => btn.classList.remove('active'));
        if (event && event.target) {
            event.target.classList.add('active');
        }

        applyFilters();
    }

    function searchTable() {
        applyFilters();
    }

    function applyFilters() {
        const query = document.getElementById('searchInput').value.toUpperCase();
        const rows = document.querySelectorAll('#studyTable tbody tr');

        rows.forEach(row => {
            const isLev = row.getAttribute('data-is-lev') === '1';
            const text = row.textContent.toUpperCase();

            let matchesType = true;
            if (currentFilter === 'lev' && !isLev) matchesType = false;
            if (currentFilter === 'stock' && isLev) matchesType = false;

            let matchesSearch = text.includes(query);

            if (matchesType && matchesSearch) {
                row.style.display = '';
            } else {
                row.style.display = 'none';
            }
        });
    }

    let sortDirections = {};

    function sortTable(colIdx, type) {
        const table = document.getElementById('studyTable');
        const tbody = table.querySelector('tbody');
        const rows = Array.from(tbody.querySelectorAll('tr'));
        
        sortDirections[colIdx] = !sortDirections[colIdx];
        const asc = sortDirections[colIdx];

        rows.sort((a, b) => {
            let valA = a.children[colIdx].innerText.replace(/[#%+,\$]/g, '').trim();
            let valB = b.children[colIdx].innerText.replace(/[#%+,\$]/g, '').trim();

            if (type === 'num') {
                let numA = parseFloat(valA) || 0;
                let numB = parseFloat(valB) || 0;
                return asc ? numA - numB : numB - numA;
            } else {
                return asc ? valA.localeCompare(valB) : valB.localeCompare(valA);
            }
        });

        rows.forEach(r => tbody.appendChild(r));
    }
</script>

</body>
</html>
`
