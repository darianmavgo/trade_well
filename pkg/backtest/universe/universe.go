package universe

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/datasource"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/jmoiron/sqlx"
)

// DefaultPath is the universe database location (refdata/universe.db).
var DefaultPath = appenv.UniverseDB()

// CutoffDate defines the history threshold: January 1, 2021 UTC.
// Any symbol with first_trade_date on or before this cutoff is confirmed to have history back to Jan 2021.
var CutoffDate = time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
var CutoffUnix = CutoffDate.Unix()

// SymbolRecord represents an asset in the universe database with classification facts.
type SymbolRecord struct {
	Symbol         string `db:"symbol" json:"symbol"`
	Name           string `db:"name" json:"name"`
	AssetType      string `db:"asset_type" json:"asset_type"`             // ETF, CS (Common Stock), ADRC, etc.
	IsETF          bool   `db:"is_etf" json:"is_etf"`                     // 1 if ETF, 0 if stock/other
	Leverage       string `db:"leverage" json:"leverage"`                 // "none", "1.5x", "2x", "3x"
	Direction      string `db:"direction" json:"direction"`               // "long", "inverse"
	Category       string `db:"category" json:"category"`                 // broad_market_sp500, technology, semiconductors, etc.
	Exchange       string `db:"exchange" json:"exchange"`                 // XNAS, XNYS, ARCX, BATS, etc.
	FirstTradeDate string `db:"first_trade_date" json:"first_trade_date"` // YYYY-MM-DD
	Active         bool   `db:"active" json:"active"`
	Confirmed2021  bool   `db:"confirmed_2021" json:"confirmed_2021"`
	UpdatedAt      string `db:"updated_at" json:"updated_at"`
}

const Schema = `
CREATE TABLE IF NOT EXISTS universe (
	symbol           TEXT PRIMARY KEY,
	name             TEXT NOT NULL DEFAULT '',
	asset_type       TEXT NOT NULL DEFAULT '',
	is_etf           INTEGER NOT NULL DEFAULT 0,
	leverage         TEXT NOT NULL DEFAULT 'none',
	direction        TEXT NOT NULL DEFAULT 'long',
	category         TEXT NOT NULL DEFAULT 'other',
	exchange         TEXT NOT NULL DEFAULT '',
	first_trade_date TEXT NOT NULL DEFAULT '',
	active           INTEGER NOT NULL DEFAULT 1,
	confirmed_2021   INTEGER NOT NULL DEFAULT 0,
	updated_at       DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_universe_is_etf ON universe(is_etf);
CREATE INDEX IF NOT EXISTS idx_universe_leverage ON universe(leverage);
CREATE INDEX IF NOT EXISTS idx_universe_direction ON universe(direction);
CREATE INDEX IF NOT EXISTS idx_universe_category ON universe(category);
CREATE INDEX IF NOT EXISTS idx_universe_confirmed ON universe(confirmed_2021);
`

// Open opens (creating if needed) the universe SQLite database and initializes the schema.
func Open(path string) (*sqlx.DB, error) {
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(Schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("universe schema error: %w", err)
	}
	return db, nil
}

// RawTicker represents a ticker item fetched from discovery sources.
type RawTicker struct {
	Symbol   string
	Name     string
	Type     string
	Exchange string
	Active   bool
}

// Config controls universe discovery, verification, and persistence.
type Config struct {
	DBPath     string
	PolygonKey string
	Workers    int
	Limit      int
	MaxChecks  int
	StocksOnly bool
	ETFsOnly   bool
	// Refresh verifies every symbol again. Without it a symbol whose first trade
	// date is already stored is not looked up again, since that date never changes.
	Refresh bool
	// RetryDays is how long a symbol that could not be verified waits before the
	// next attempt, so delisted tickers are not asked about on every run.
	RetryDays int
	Out       io.Writer
}

// DefaultConfig provides sensible defaults.
func DefaultConfig() Config {
	return Config{
		DBPath:    DefaultPath,
		Workers:   16,
		Limit:     1000,
		RetryDays: 7,
	}
}

// SaveSymbols inserts or replaces a batch of symbols into the universe table.
func SaveSymbols(db *sqlx.DB, records []SymbolRecord) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	query := `
		INSERT INTO universe (
			symbol, name, asset_type, is_etf, leverage, direction,
			category, exchange, first_trade_date, active, confirmed_2021, updated_at
		) VALUES (
			:symbol, :name, :asset_type, :is_etf, :leverage, :direction,
			:category, :exchange, :first_trade_date, :active, :confirmed_2021, CURRENT_TIMESTAMP
		)
		ON CONFLICT(symbol) DO UPDATE SET
			name = excluded.name,
			asset_type = excluded.asset_type,
			is_etf = excluded.is_etf,
			leverage = excluded.leverage,
			direction = excluded.direction,
			category = excluded.category,
			exchange = excluded.exchange,
			first_trade_date = CASE WHEN excluded.first_trade_date != '' THEN excluded.first_trade_date ELSE universe.first_trade_date END,
			active = excluded.active,
			confirmed_2021 = excluded.confirmed_2021,
			updated_at = CURRENT_TIMESTAMP
	`
	stmt, err := tx.PrepareNamed(query)
	if err != nil {
		return fmt.Errorf("failed to prepare named stmt: %w", err)
	}
	defer stmt.Close()

	for _, rec := range records {
		if _, err := stmt.Exec(rec); err != nil {
			return fmt.Errorf("failed to insert symbol %s: %w", rec.Symbol, err)
		}
	}

	return tx.Commit()
}

var (
	// A factor is a number then X that does not continue a longer number or word
	// before it, so "2x Long", "-3X" and "2xMonthly Pay" match but "12X" does not.
	lev3xRegex  = regexp.MustCompile(`(^|[^0-9A-Z.])3X|\bTRIPLE (LONG|SHORT)\b|ULTRAPRO`)
	lev2xRegex  = regexp.MustCompile(`(^|[^0-9A-Z.])2X|\bDOUBLE (LONG|SHORT)\b`)
	lev15xRegex = regexp.MustCompile(`(^|[^0-9A-Z.])1\.5X`)

	// Phrases whose "short" is a maturity or a duration, not a bet against the market.
	shortMaturityRegex = regexp.MustCompile(`ULTRA[- ]?SHORT|SHORT[- ](TERM|DURATION|MATURITY|DATED|HORIZON|TREASURY|GOVERNMENT|GOVT|CORPORATE|BOND|MUNI|CREDIT|INVESTMENT GRADE)`)
	// ProShares names its leveraged funds "Ultra ..." and its inverse funds
	// "Short ..." or "UltraShort ...", sometimes after "Trust" or "Trust II".
	proSharesUltraRegex = regexp.MustCompile(`^PROSHARES( TRUST( II)?)? ULTRA`)
	proSharesShortRegex = regexp.MustCompile(`^PROSHARES( TRUST( II)?)? (ULTRASHORT|SHORT )`)
	shortTermRegex      = regexp.MustCompile(`SHORT[- ](TERM|DURATION|MATURITY|DATED|HORIZON)`)
	shortWordRegex      = regexp.MustCompile(`\bSHORTS?\b`)
)

// classifyLeverage reads the fund's leverage factor from its upper-cased name:
// "none", "1.5x", "2x" or "3x". ProShares "Ultra" is 2x and "UltraPro" 3x, while
// "Ultra Short-Term Bond" and "DoubleLine" are not leveraged.
func classifyLeverage(upperName string) string {
	switch {
	case lev3xRegex.MatchString(upperName):
		return "3x"
	case lev2xRegex.MatchString(upperName), proSharesUltraRegex.MatchString(upperName):
		return "2x"
	case lev15xRegex.MatchString(upperName):
		return "1.5x"
	}
	return "none"
}

// classifyDirection is "inverse" for a fund that bets against its index (bear,
// inverse, short, UltraShort) and "long" otherwise. A short maturity ("Short-Term
// Treasury", "VIX Short-Term Futures") does not make a fund inverse.
func classifyDirection(upperName string) string {
	// ProShares names its inverse funds "Short ..." even on bonds (Short 20+ Year
	// Treasury), where other sponsors use short for the maturity.
	if proSharesShortRegex.MatchString(upperName) {
		return "inverse"
	}
	// A leveraged fund that says short is a short bet, whatever follows ("-3x Short
	// Investment Grade Corporate Bond").
	if classifyLeverage(upperName) != "none" && shortWordRegex.MatchString(shortTermRegex.ReplaceAllString(upperName, " ")) {
		return "inverse"
	}
	n := shortMaturityRegex.ReplaceAllString(upperName, " ")
	if strings.Contains(n, "BEAR") || strings.Contains(n, "INVERSE") || shortWordRegex.MatchString(n) {
		return "inverse"
	}
	return "long"
}

// Reclassify recomputes leverage and direction of every stored symbol from its
// stored name and asset type, with the current classifier. It returns the rows
// whose labels changed and, unless dryRun, saves them.
func Reclassify(db *sqlx.DB, dryRun bool) ([]Reclassified, error) {
	var rows []struct {
		Symbol    string `db:"symbol"`
		Name      string `db:"name"`
		AssetType string `db:"asset_type"`
		Leverage  string `db:"leverage"`
		Direction string `db:"direction"`
	}
	if err := db.Select(&rows, `SELECT symbol, name, asset_type, leverage, direction FROM universe`); err != nil {
		return nil, err
	}
	var changed []Reclassified
	for _, r := range rows {
		_, lev, dir, _ := ClassifyTicker(r.Symbol, r.Name, r.AssetType)
		if lev == r.Leverage && dir == r.Direction {
			continue
		}
		changed = append(changed, Reclassified{r.Symbol, r.Name, r.Leverage, r.Direction, lev, dir})
	}
	if dryRun || len(changed) == 0 {
		return changed, nil
	}
	tx, err := db.Beginx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, c := range changed {
		if _, err := tx.Exec(`UPDATE universe SET leverage = ?, direction = ?, updated_at = CURRENT_TIMESTAMP WHERE symbol = ?`,
			c.NewLeverage, c.NewDirection, c.Symbol); err != nil {
			return nil, err
		}
	}
	return changed, tx.Commit()
}

// Reclassified is one symbol whose leverage or direction label changed.
type Reclassified struct {
	Symbol, Name              string
	OldLeverage, OldDirection string
	NewLeverage, NewDirection string
}

// ClassifyTicker analyzes ticker symbol, name, and asset type to classify leverage, direction, and grouping facts.
func ClassifyTicker(sym, name, assetType string) (isETF bool, leverage string, direction string, category string) {
	upperName := strings.ToUpper(name)
	upperType := strings.ToUpper(assetType)
	upperSym := strings.ToUpper(sym)

	// Determine if ETF
	isETF = upperType == "ETF" || upperType == "ETN" || upperType == "ETS" || upperType == "ETV" ||
		strings.Contains(upperName, " ETF") || strings.Contains(upperName, " ETN") ||
		strings.Contains(upperName, "INDEX FUND") || strings.Contains(upperName, "TRUST, SERIES")

	leverage = classifyLeverage(upperName)
	direction = classifyDirection(upperName)

	// Category / Theme Grouping
	if !isETF {
		if upperType == "ADRC" || strings.Contains(upperName, "ADR") || strings.Contains(upperName, "AMERICAN DEPOSITARY") {
			category = "adr"
		} else {
			category = "common_stock"
		}
	} else {
		category = "other_etf"
		switch {
		case strings.Contains(upperName, "SEMICONDUCTOR") || strings.Contains(upperName, "SEMIS") || upperSym == "SOXL" || upperSym == "SOXS":
			category = "semiconductors"
		case strings.Contains(upperName, "TECHNOLOGY") || strings.Contains(upperName, "TECH") ||
			strings.Contains(upperName, "NASDAQ") || strings.Contains(upperName, "QQQ") ||
			strings.Contains(upperName, "SOFTWARE") || strings.Contains(upperName, "INTERNET") ||
			strings.Contains(upperName, "CYBERSECURITY") || strings.Contains(upperName, "CLOUD") ||
			strings.Contains(upperName, "AI ") || strings.Contains(upperName, "ARTIFICIAL"):
			category = "technology"
		case strings.Contains(upperName, "S&P 500") || strings.Contains(upperName, "S&P500") ||
			strings.Contains(upperName, "SPDR S&P") || strings.Contains(upperName, "LARGE CAP") ||
			strings.Contains(upperName, "LARGE-CAP") || upperSym == "SPY" || upperSym == "VOO" || upperSym == "IVV":
			category = "broad_market_sp500"
		case strings.Contains(upperName, "DOW JONES") || strings.Contains(upperName, "DOW 30") ||
			strings.Contains(upperName, "DOW30") || strings.Contains(upperName, "DJIA") || upperSym == "DIA":
			category = "broad_market_dow"
		case strings.Contains(upperName, "RUSSELL") || strings.Contains(upperName, "SMALL CAP") ||
			strings.Contains(upperName, "SMALL-CAP") || strings.Contains(upperName, "MID CAP") ||
			strings.Contains(upperName, "MID-CAP") || upperSym == "IWM":
			category = "small_mid_cap"
		case strings.Contains(upperName, "TREASURY") || strings.Contains(upperName, "BOND") ||
			strings.Contains(upperName, "FIXED INCOME") || strings.Contains(upperName, "YIELD") ||
			strings.Contains(upperName, "AGGREGATE") || strings.Contains(upperName, "CLO ") || upperSym == "TLT":
			category = "fixed_income"
		case strings.Contains(upperName, "GOLD") || strings.Contains(upperName, "SILVER") ||
			strings.Contains(upperName, "OIL") || strings.Contains(upperName, "COMMODITY") ||
			strings.Contains(upperName, "COMMODITIES") || strings.Contains(upperName, "GAS") ||
			strings.Contains(upperName, "METALS") || upperSym == "GLD" || upperSym == "USO":
			category = "commodities"
		case strings.Contains(upperName, "VIX") || strings.Contains(upperName, "VOLATILITY") ||
			upperSym == "UVXY" || upperSym == "SVXY" || upperSym == "VXX":
			category = "volatility"
		case strings.Contains(upperName, "FINANCIAL") || strings.Contains(upperName, "BANK") ||
			strings.Contains(upperName, "INSURANCE"):
			category = "financials"
		case strings.Contains(upperName, "ENERGY") || strings.Contains(upperName, "SOLAR") ||
			strings.Contains(upperName, "CLEAN ENERGY") || upperSym == "XLE":
			category = "energy"
		case strings.Contains(upperName, "HEALTH") || strings.Contains(upperName, "BIOTECH") ||
			strings.Contains(upperName, "PHARMA") || upperSym == "XLV" || upperSym == "XBI":
			category = "healthcare_biotech"
		case strings.Contains(upperName, "REAL ESTATE") || strings.Contains(upperName, "REIT") || upperSym == "VNQ":
			category = "real_estate"
		case strings.Contains(upperName, "EMERGING") || strings.Contains(upperName, "CHINA") ||
			strings.Contains(upperName, "DEVELOPED") || strings.Contains(upperName, "INTERNATIONAL") ||
			strings.Contains(upperName, "GLOBAL") || strings.Contains(upperName, "JAPAN") || upperSym == "EEM":
			category = "international"
		case strings.Contains(upperName, "DIVIDEND") || strings.Contains(upperName, "HIGH YIELD"):
			category = "dividend"
		}
	}

	return isETF, leverage, direction, category
}

// FetchAllDiscoveredTickers fetches tickers across all available feeds (Polygon reference API with fallback/enrichment from Nasdaq screener & directory).
func FetchAllDiscoveredTickers(cfg Config) ([]RawTicker, error) {
	out := cfg.Out
	if out == nil {
		out = io.Discard
	}

	tickerMap := make(map[string]RawTicker)

	// Source 1: Polygon.io reference tickers API if key available
	apiKey := strings.TrimSpace(cfg.PolygonKey)
	if apiKey == "" {
		apiKey = datasource.ResolvePolygonAPIKey()
	}

	if apiKey != "" {
		fmt.Fprintf(out, "📡 Querying Polygon.io reference tickers API...\n")
		polyTickers, err := fetchPolygonTickers(apiKey, cfg.Limit, out)
		if err != nil {
			fmt.Fprintf(out, "⚠️ Polygon fetch error or rate-limited: %v (continuing with Nasdaq feeds)\n", err)
		} else {
			for _, t := range polyTickers {
				tickerMap[t.Symbol] = t
			}
			fmt.Fprintf(out, "✓ Retrieved %d tickers from Polygon.io\n", len(polyTickers))
		}
	}

	// Source 2: Nasdaq Official Screener API (returns ~7,000+ active US stocks, ADRs, ETFs across Nasdaq, NYSE, AMEX)
	fmt.Fprintf(out, "📡 Fetching Nasdaq Official Screener API...\n")
	nasdaqScreenerTickers, err := fetchNasdaqScreenerTickers()
	if err != nil {
		fmt.Fprintf(out, "⚠️ Nasdaq screener fetch notice: %v\n", err)
	} else {
		newCount := 0
		for _, t := range nasdaqScreenerTickers {
			if existing, ok := tickerMap[t.Symbol]; ok {
				if existing.Name == "" {
					existing.Name = t.Name
				}
				tickerMap[t.Symbol] = existing
			} else {
				tickerMap[t.Symbol] = t
				newCount++
			}
		}
		fmt.Fprintf(out, "✓ Processed %d tickers from Nasdaq Screener (+%d new)\n", len(nasdaqScreenerTickers), newCount)
	}

	// Source 3: Nasdaq Trader Symbol Directory (covers every traded equity and ETF: 13,000+ entries)
	fmt.Fprintf(out, "📡 Fetching Nasdaq Trader Symbol Directory...\n")
	traderTickers, err := fetchNasdaqTraderDirectory()
	if err != nil {
		fmt.Fprintf(out, "⚠️ Nasdaq trader directory notice: %v\n", err)
	} else {
		newCount := 0
		for _, t := range traderTickers {
			if existing, ok := tickerMap[t.Symbol]; ok {
				if existing.Name == "" {
					existing.Name = t.Name
				}
				if existing.Type == "" {
					existing.Type = t.Type
				}
				tickerMap[t.Symbol] = existing
			} else {
				tickerMap[t.Symbol] = t
				newCount++
			}
		}
		fmt.Fprintf(out, "✓ Processed %d tickers from Nasdaq Trader Directory (+%d new)\n", len(traderTickers), newCount)
	}

	results := make([]RawTicker, 0, len(tickerMap))
	for _, t := range tickerMap {
		if t.Symbol == "" {
			continue
		}
		if cfg.ETFsOnly && t.Type != "ETF" && !strings.Contains(strings.ToUpper(t.Name), "ETF") {
			continue
		}
		if cfg.StocksOnly && (t.Type == "ETF" || strings.Contains(strings.ToUpper(t.Name), "ETF")) {
			continue
		}
		results = append(results, t)
	}

	return results, nil
}

type polygonResult struct {
	Ticker          string `json:"ticker"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	PrimaryExchange string `json:"primary_exchange"`
	Active          bool   `json:"active"`
}

type polygonResponse struct {
	Results []polygonResult `json:"results"`
	NextURL string          `json:"next_url"`
	Status  string          `json:"status"`
	Error   string          `json:"error"`
}

func fetchPolygonTickers(apiKey string, limit int, out io.Writer) ([]RawTicker, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	client := &http.Client{Timeout: 30 * time.Second}
	url := fmt.Sprintf("https://api.polygon.io/v3/reference/tickers?market=stocks&active=true&sort=ticker&order=asc&limit=%d&apiKey=%s", limit, apiKey)

	var list []RawTicker
	page := 0

	for url != "" {
		page++
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return list, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return list, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return list, err
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			fmt.Fprintln(out, "⏳ Polygon 429 rate limit hit. Pausing 60s or stopping pagination...")
			break
		}
		if resp.StatusCode != http.StatusOK {
			return list, fmt.Errorf("polygon HTTP %d: %s", resp.StatusCode, string(body))
		}

		var parsed polygonResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return list, err
		}
		if parsed.Error != "" {
			return list, fmt.Errorf("polygon error: %s", parsed.Error)
		}

		for _, r := range parsed.Results {
			if r.Ticker != "" {
				list = append(list, RawTicker{
					Symbol:   strings.ToUpper(strings.TrimSpace(r.Ticker)),
					Name:     r.Name,
					Type:     r.Type,
					Exchange: r.PrimaryExchange,
					Active:   r.Active,
				})
			}
		}

		if parsed.NextURL == "" {
			break
		}
		if strings.Contains(parsed.NextURL, "apiKey=") {
			url = parsed.NextURL
		} else {
			sep := "&"
			if !strings.Contains(parsed.NextURL, "?") {
				sep = "?"
			}
			url = parsed.NextURL + sep + "apiKey=" + apiKey
		}
	}
	return list, nil
}

type nasdaqScreenerResponse struct {
	Data struct {
		Table struct {
			Rows []struct {
				Symbol string `json:"symbol"`
				Name   string `json:"name"`
			} `json:"rows"`
		} `json:"table"`
	} `json:"data"`
}

func fetchNasdaqScreenerTickers() ([]RawTicker, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	url := "https://api.nasdaq.com/api/screener/stocks?tableonly=true&limit=15000"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nasdaq HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var parsed nasdaqScreenerResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}

	var list []RawTicker
	for _, r := range parsed.Data.Table.Rows {
		sym := normalizeSymbol(r.Symbol)
		if sym == "" {
			continue
		}
		isETF := strings.Contains(strings.ToUpper(r.Name), "ETF") || strings.Contains(strings.ToUpper(r.Name), "ETN")
		tType := "CS"
		if isETF {
			tType = "ETF"
		}
		list = append(list, RawTicker{
			Symbol: sym,
			Name:   strings.TrimSpace(r.Name),
			Type:   tType,
			Active: true,
		})
	}
	return list, nil
}

func fetchNasdaqTraderDirectory() ([]RawTicker, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	url := "https://www.nasdaqtrader.com/dynamic/SymDir/nasdaqtraded.txt"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(body), "\n")
	var list []RawTicker
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "File Creation Time") || strings.HasPrefix(line, "Nasdaq Traded") {
			continue
		}
		parts := strings.Split(line, "|")
		// Format: Nasdaq Traded|Symbol|Security Name|Listing Exchange|Market Category|ETF|Round Lot Size|Test Issue|Financial Status|CQS Symbol|NASDAQ Symbol|NextShares
		if len(parts) < 8 {
			continue
		}
		if parts[7] == "Y" { // Test Issue
			continue
		}
		rawSym := parts[1]
		sym := normalizeSymbol(rawSym)
		if sym == "" {
			continue
		}
		name := strings.TrimSpace(parts[2])
		exchange := parts[3]
		isETF := parts[5] == "Y"
		tType := "CS"
		if isETF {
			tType = "ETF"
		}
		list = append(list, RawTicker{
			Symbol:   sym,
			Name:     name,
			Type:     tType,
			Exchange: exchange,
			Active:   true,
		})
	}
	return list, nil
}

func normalizeSymbol(sym string) string {
	s := strings.ToUpper(strings.TrimSpace(sym))
	s = strings.ReplaceAll(s, "^", "-P")
	s = strings.ReplaceAll(s, "/", "-")
	// Convert preferred stock format ABR$D to ABR-PD, or DOT format BRK.B to BRK-B
	if idx := strings.Index(s, "$"); idx > 0 {
		s = s[:idx] + "-P" + s[idx+1:]
	}
	s = strings.ReplaceAll(s, ".", "-")
	return s
}

// VerificationResult holds history confirmation facts.
type VerificationResult struct {
	Symbol         string
	FirstTradeDate string
	Confirmed2021  bool
	Valid          bool
}

// VerifySymbolHistory confirms if a symbol has trading history going back to Jan 2021 without downloading full OHLCV series.
func VerifySymbolHistory(client *http.Client, sym string) VerificationResult {
	res := VerificationResult{Symbol: sym}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	url := fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/%s?range=1d&interval=1mo", sym)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return res
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return res
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return res
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return res
	}

	type yfChartResponse struct {
		Chart struct {
			Result []struct {
				Meta struct {
					FirstTradeDate int64  `json:"firstTradeDate"`
					Symbol         string `json:"symbol"`
				} `json:"meta"`
			} `json:"result"`
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
		} `json:"chart"`
	}

	var yf yfChartResponse
	if err := json.Unmarshal(body, &yf); err != nil || len(yf.Chart.Result) == 0 {
		return res
	}

	meta := yf.Chart.Result[0].Meta
	if meta.FirstTradeDate > 0 {
		t := time.Unix(meta.FirstTradeDate, 0).UTC()
		res.FirstTradeDate = t.Format("2006-01-02")
		res.Valid = true
		if meta.FirstTradeDate <= CutoffUnix {
			res.Confirmed2021 = true
		}
	}
	return res
}

// known is what the universe already holds about a symbol.
type known struct {
	FirstTradeDate string `db:"first_trade_date"`
	UpdatedAt      string `db:"updated_at"`
}

// planVerification splits discovered tickers into those that need a lookup and
// those that do not. A symbol with a stored first trade date is a settled fact
// and is skipped. A symbol that failed verification before is retried only once
// retryDays have passed since its last attempt. refresh sends everything.
func planVerification(discovered []RawTicker, have map[string]known, refresh bool, retryDays int, now time.Time) (verify []RawTicker, skippedKnown, skippedRecent int) {
	for _, t := range discovered {
		k, ok := have[t.Symbol]
		switch {
		case refresh || !ok:
			verify = append(verify, t)
		case k.FirstTradeDate != "":
			skippedKnown++
		case recentlyTried(k.UpdatedAt, retryDays, now):
			skippedRecent++
		default:
			verify = append(verify, t)
		}
	}
	return verify, skippedKnown, skippedRecent
}

func recentlyTried(updatedAt string, retryDays int, now time.Time) bool {
	t, err := time.Parse("2006-01-02 15:04:05", updatedAt)
	if err != nil {
		return false
	}
	return now.Sub(t) < time.Duration(retryDays)*24*time.Hour
}

// Run executes the complete discovery, verification, classification, and database saving workflow.
func Run(cfg Config) (int, error) {
	out := cfg.Out
	if out == nil {
		out = io.Discard
	}

	fmt.Fprintf(out, "🚀 Initializing Universe Database at: %s\n", cfg.DBPath)
	db, err := Open(cfg.DBPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open universe db %s: %w", cfg.DBPath, err)
	}
	defer db.Close()

	// Step 1: Discover all available symbols
	fmt.Fprintf(out, "\n📋 Step 1: Discovering all stocks, ETFs, and instruments...\n")
	rawTickers, err := FetchAllDiscoveredTickers(cfg)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch tickers: %w", err)
	}
	fmt.Fprintf(out, "✓ Total unique candidate symbols discovered: %d\n", len(rawTickers))

	var have []struct {
		Symbol string `db:"symbol"`
		known
	}
	if err := db.Select(&have, `SELECT symbol, first_trade_date, updated_at FROM universe`); err != nil {
		return 0, fmt.Errorf("reading the symbols already stored: %w", err)
	}
	stored := make(map[string]known, len(have))
	for _, h := range have {
		stored[h.Symbol] = h.known
	}
	rawTickers, skippedKnown, skippedRecent := planVerification(rawTickers, stored, cfg.Refresh, cfg.RetryDays, time.Now().UTC())
	fmt.Fprintf(out, "✓ %d to look up (skipped %d with a stored first trade date, %d tried within %d days)\n",
		len(rawTickers), skippedKnown, skippedRecent, cfg.RetryDays)

	if cfg.MaxChecks > 0 && len(rawTickers) > cfg.MaxChecks {
		fmt.Fprintf(out, "⚠️ Limiting check to first %d symbols as requested by -max-checks\n", cfg.MaxChecks)
		rawTickers = rawTickers[:cfg.MaxChecks]
	}

	// Step 2: Confirm history back to Jan 2021 concurrently
	fmt.Fprintf(out, "\n🔍 Step 2: Verifying trading history going back to Jan 2021 (%d workers)...\n", cfg.Workers)
	httpClient := &http.Client{Timeout: 10 * time.Second}

	var confirmedCount int64
	var totalChecked int64
	var recordsToSave []SymbolRecord
	var mu sync.Mutex

	jobs := make(chan RawTicker, len(rawTickers))
	for _, t := range rawTickers {
		jobs <- t
	}
	close(jobs)

	var wg sync.WaitGroup
	workers := cfg.Workers
	if workers <= 0 {
		workers = 16
	}

	startTime := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				v := VerifySymbolHistory(httpClient, t.Symbol)
				isETF, lev, dir, cat := ClassifyTicker(t.Symbol, t.Name, t.Type)

				rec := SymbolRecord{
					Symbol:         t.Symbol,
					Name:           t.Name,
					AssetType:      t.Type,
					IsETF:          isETF,
					Leverage:       lev,
					Direction:      dir,
					Category:       cat,
					Exchange:       t.Exchange,
					FirstTradeDate: v.FirstTradeDate,
					Active:         t.Active,
					Confirmed2021:  v.Confirmed2021,
				}

				if v.Confirmed2021 {
					atomic.AddInt64(&confirmedCount, 1)
				}
				cnt := atomic.AddInt64(&totalChecked, 1)

				mu.Lock()
				recordsToSave = append(recordsToSave, rec)
				// Batch save every 250 records to SQLite
				if len(recordsToSave) >= 250 {
					_ = SaveSymbols(db, recordsToSave)
					recordsToSave = nil
				}
				mu.Unlock()

				if cnt%500 == 0 || cnt == int64(len(rawTickers)) {
					elapsed := time.Since(startTime).Seconds()
					rate := float64(cnt) / elapsed
					fmt.Fprintf(out, "   [%d/%d] Verified %d (Confirmed >= Jan 2021: %d) (%.1f/s)\n",
						cnt, len(rawTickers), cnt, atomic.LoadInt64(&confirmedCount), rate)
				}
			}
		}()
	}

	wg.Wait()

	mu.Lock()
	if len(recordsToSave) > 0 {
		_ = SaveSymbols(db, recordsToSave)
		recordsToSave = nil
	}
	mu.Unlock()

	// Print Summary Statistics
	fmt.Fprintf(out, "\n📊 Universe Summary:\n")
	var totalInDB, totalConfirmed, etfCount, stockCount int
	var lev2x, lev3x, inverseCount int

	_ = db.Get(&totalInDB, "SELECT COUNT(*) FROM universe")
	_ = db.Get(&totalConfirmed, "SELECT COUNT(*) FROM universe WHERE confirmed_2021 = 1")
	_ = db.Get(&etfCount, "SELECT COUNT(*) FROM universe WHERE confirmed_2021 = 1 AND is_etf = 1")
	_ = db.Get(&stockCount, "SELECT COUNT(*) FROM universe WHERE confirmed_2021 = 1 AND is_etf = 0")
	_ = db.Get(&lev2x, "SELECT COUNT(*) FROM universe WHERE confirmed_2021 = 1 AND leverage = '2x'")
	_ = db.Get(&lev3x, "SELECT COUNT(*) FROM universe WHERE confirmed_2021 = 1 AND leverage = '3x'")
	_ = db.Get(&inverseCount, "SELECT COUNT(*) FROM universe WHERE confirmed_2021 = 1 AND direction = 'inverse'")

	fmt.Fprintf(out, "   • Total Symbols in DB: %d\n", totalInDB)
	fmt.Fprintf(out, "   • Symbols Confirmed >= Jan 2021: %d\n", totalConfirmed)
	fmt.Fprintf(out, "   • ETFs Confirmed: %d\n", etfCount)
	fmt.Fprintf(out, "   • Stocks / Others Confirmed: %d\n", stockCount)
	fmt.Fprintf(out, "   • 2x Leveraged: %d\n", lev2x)
	fmt.Fprintf(out, "   • 3x Leveraged: %d\n", lev3x)
	fmt.Fprintf(out, "   • Inverse / Bear: %d\n", inverseCount)
	fmt.Fprintf(out, "\n✨ Universe database populated successfully at: %s\n", cfg.DBPath)

	return totalConfirmed, nil
}

// UnleveragedETFs returns the active ETFs with no leverage (inverse funds
// included, 2x and 3x excluded), most traded first by the 20-day average volume
// that `universe avgvol` stored, then by symbol. Without that table the order is
// by symbol.
func UnleveragedETFs(db *sqlx.DB) ([]string, error) {
	const where = `u.is_etf = 1 AND u.leverage = 'none' AND u.active = 1`
	var hasVol int
	if err := db.Get(&hasVol, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'avg_volume'`); err != nil {
		return nil, err
	}
	q := `SELECT u.symbol FROM universe u WHERE ` + where + ` ORDER BY u.symbol`
	if hasVol > 0 {
		q = `SELECT u.symbol FROM universe u
			LEFT JOIN avg_volume v ON v.symbol = u.symbol AND v.window_days = 20
			WHERE ` + where + ` ORDER BY coalesce(v.avg_volume, 0) DESC, u.symbol`
	}
	var syms []string
	err := db.Select(&syms, q)
	return syms, err
}
