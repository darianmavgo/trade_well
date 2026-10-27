package universe

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClassifyTicker(t *testing.T) {
	tests := []struct {
		sym       string
		name      string
		assetType string
		wantETF   bool
		wantLev   string
		wantDir   string
		wantCat   string
	}{
		{
			sym:       "SPY",
			name:      "SPDR S&P 500 ETF Trust",
			assetType: "ETF",
			wantETF:   true,
			wantLev:   "none",
			wantDir:   "long",
			wantCat:   "broad_market_sp500",
		},
		{
			sym:       "TQQQ",
			name:      "ProShares UltraPro QQQ",
			assetType: "ETF",
			wantETF:   true,
			wantLev:   "3x",
			wantDir:   "long",
			wantCat:   "technology",
		},
		{
			sym:       "SOXL",
			name:      "Direxion Daily Semiconductor Bull 3X Shares",
			assetType: "ETF",
			wantETF:   true,
			wantLev:   "3x",
			wantDir:   "long",
			wantCat:   "semiconductors",
		},
		{
			sym:       "SOXS",
			name:      "Direxion Daily Semiconductor Bear 3X Shares",
			assetType: "ETF",
			wantETF:   true,
			wantLev:   "3x",
			wantDir:   "inverse",
			wantCat:   "semiconductors",
		},
		{
			sym:       "UPRO",
			name:      "ProShares UltraPro S&P500",
			assetType: "ETF",
			wantETF:   true,
			wantLev:   "3x",
			wantDir:   "long",
			wantCat:   "broad_market_sp500",
		},
		{
			sym:       "SSO",
			name:      "ProShares Ultra S&P500",
			assetType: "ETF",
			wantETF:   true,
			wantLev:   "2x",
			wantDir:   "long",
			wantCat:   "broad_market_sp500",
		},
		{
			sym:       "NVDL",
			name:      "GraniteShares 2x Long NVDA Daily ETF",
			assetType: "ETF",
			wantETF:   true,
			wantLev:   "2x",
			wantDir:   "long",
			wantCat:   "other_etf",
		},
		{
			sym:       "AAPL",
			name:      "Apple Inc. Common Stock",
			assetType: "CS",
			wantETF:   false,
			wantLev:   "none",
			wantDir:   "long",
			wantCat:   "common_stock",
		},
		{
			sym:       "BABA",
			name:      "Alibaba Group Holding Ltd American Depositary Shares",
			assetType: "ADRC",
			wantETF:   false,
			wantLev:   "none",
			wantDir:   "long",
			wantCat:   "adr",
		},
		{
			sym:       "TLT",
			name:      "iShares 20+ Year Treasury Bond ETF",
			assetType: "ETF",
			wantETF:   true,
			wantLev:   "none",
			wantDir:   "long",
			wantCat:   "fixed_income",
		},
	}

	for _, tt := range tests {
		isETF, lev, dir, cat := ClassifyTicker(tt.sym, tt.name, tt.assetType)
		if isETF != tt.wantETF {
			t.Errorf("[%s] isETF = %v, want %v", tt.sym, isETF, tt.wantETF)
		}
		if lev != tt.wantLev {
			t.Errorf("[%s] leverage = %s, want %s", tt.sym, lev, tt.wantLev)
		}
		if dir != tt.wantDir {
			t.Errorf("[%s] direction = %s, want %s", tt.sym, dir, tt.wantDir)
		}
		if cat != tt.wantCat {
			t.Errorf("[%s] category = %s, want %s", tt.sym, cat, tt.wantCat)
		}
	}
}

func TestUniverseDBSaveAndQuery(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "universe_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "universe.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open universe db: %v", err)
	}
	defer db.Close()

	records := []SymbolRecord{
		{
			Symbol:         "SPY",
			Name:           "SPDR S&P 500 ETF Trust",
			AssetType:      "ETF",
			IsETF:          true,
			Leverage:       "none",
			Direction:      "long",
			Category:       "broad_market_sp500",
			Exchange:       "ARCX",
			FirstTradeDate: "1993-01-29",
			Active:         true,
			Confirmed2021:  true,
		},
		{
			Symbol:         "TQQQ",
			Name:           "ProShares UltraPro QQQ",
			AssetType:      "ETF",
			IsETF:          true,
			Leverage:       "3x",
			Direction:      "long",
			Category:       "technology",
			Exchange:       "XNAS",
			FirstTradeDate: "2010-02-11",
			Active:         true,
			Confirmed2021:  true,
		},
		{
			Symbol:         "NEW_SYM",
			Name:           "Recent IPO Inc",
			AssetType:      "CS",
			IsETF:          false,
			Leverage:       "none",
			Direction:      "long",
			Category:       "common_stock",
			Exchange:       "XNAS",
			FirstTradeDate: "2023-05-15",
			Active:         true,
			Confirmed2021:  false,
		},
	}

	if err := SaveSymbols(db, records); err != nil {
		t.Fatalf("failed to save symbols: %v", err)
	}

	var count2021 int
	err = db.Get(&count2021, "SELECT COUNT(*) FROM universe WHERE confirmed_2021 = 1")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if count2021 != 2 {
		t.Errorf("expected 2 confirmed 2021 symbols, got %d", count2021)
	}

	var lev3xCount int
	err = db.Get(&lev3xCount, "SELECT COUNT(*) FROM universe WHERE leverage = '3x'")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if lev3xCount != 1 {
		t.Errorf("expected 1 3x leveraged symbol, got %d", lev3xCount)
	}
}

func TestVerifySymbolHistoryLive(t *testing.T) {
	// Verify that checking Yahoo doesn't fetch full history and correctly identifies cutoff
	res := VerifySymbolHistory(nil, "AAPL")
	if !res.Valid {
		t.Fatalf("expected AAPL to be valid")
	}
	if !res.Confirmed2021 {
		t.Errorf("expected AAPL to be confirmed for Jan 2021, got %v (first trade %s)", res.Confirmed2021, res.FirstTradeDate)
	}

	resDate, err := time.Parse("2006-01-02", res.FirstTradeDate)
	if err != nil {
		t.Fatalf("invalid date format: %s", res.FirstTradeDate)
	}
	if resDate.After(CutoffDate) {
		t.Errorf("AAPL first trade date %s should be before cutoff %s", res.FirstTradeDate, CutoffDate)
	}
}

// Names the classifier used to get wrong: leverage written as "Double" or glued to a
// word ("2xLeveraged"), "Short-Term" read as a short bet, "Ultra Short" bond funds read
// as 2x leveraged, and "DoubleLine" read as leverage.
func TestClassifyLeverageAndDirectionFromNames(t *testing.T) {
	for _, tc := range []struct {
		name, lev, dir string
	}{
		{"DB Gold Double Short ETN due February 15, 2038", "2x", "inverse"},
		{"DB Gold Double Long ETN due February 15, 2038", "2x", "long"},
		{"ETRACS Monthly Pay 2xLeveraged US High Dividend Low Volatility ETN Series B due September 30, 2044", "2x", "long"},
		{"ETRACS 2xMonthly Pay Leveraged Preferred Stock Index ETN due September 25, 2048", "2x", "long"},
		{"VIX Short-Term Futures ETF", "none", "long"},
		{"ProShares VIX Short-Term Futures ETF", "none", "long"},
		{"ProShares Short VIX Short-Term Futures ETF", "none", "inverse"},
		{"iShares Short Treasury Bond ETF", "none", "long"},
		{"iShares Ultra Short-Term Bond ETF", "none", "long"},
		{"PIMCO Enhanced Short Maturity Active ETF", "none", "long"},
		{"State Street DoubleLine Total Return Tactical ETF", "none", "long"},
		{"ProShares Short S&P500", "none", "inverse"},
		{"ProShares UltraShort QQQ", "2x", "inverse"},
		{"DoubleLine Ultrashort Income ETF", "none", "long"},
		{"ProShares Trust UltraShort MSCI Emerging Markets", "2x", "inverse"},
		{"ProShares Trust II Ultra VIX Short-Term Futures ETF", "2x", "long"},
		{"Franklin Ultra Short Bond ETF", "none", "long"},
		{"ProShares Ultra VIX Short Term Futures ETF", "2x", "long"},
		{"ProShares UltraShort Bloomberg Crude Oil", "2x", "inverse"},
		{"MicroSectors -3x Short Investment Grade Corporate Bond (LQD) ETNs", "3x", "inverse"},
		{"Triple Flag Precious Metals Corp. Common Shares", "none", "long"},
		{"YieldMax U.S. Stocks Target Double Distribution ETF", "none", "long"},
		{"Ultralife Corporation Common Stock", "none", "long"},
		{"Innovator U.S. Equity Ultra Buffer ETF - May", "none", "long"},
		{"ProShares UltraPro Short QQQ", "3x", "inverse"},
		{"ProShares Ultra QQQ", "2x", "long"},
		{"Direxion Daily Gold Miners Index Bear 2X Shares", "2x", "inverse"},
		{"Direxion Daily Small Cap Bull 3X Shares", "3x", "long"},
		{"VelocityShares Triple Long Crude Oil ETN", "3x", "long"},
		{"iShares 1-3 Year Treasury Bond ETF", "none", "long"},
		{"Invesco S&P 500 Equal Weight ETF", "none", "long"},
	} {
		_, lev, dir, _ := ClassifyTicker("X", tc.name, "ETF")
		if lev != tc.lev || dir != tc.dir {
			t.Errorf("%q: got %s/%s, want %s/%s", tc.name, lev, dir, tc.lev, tc.dir)
		}
	}
}

func TestProSharesShortBondFundsAreInverse(t *testing.T) {
	_, lev, dir, _ := ClassifyTicker("TBF", "ProShares Short 20+ Year Treasury", "ETF")
	if lev != "none" || dir != "inverse" {
		t.Errorf("got %s/%s, want none/inverse", lev, dir)
	}
}

func TestUnleveragedETFsOrderAndFilter(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "universe.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	etf := func(sym, lev string, active bool) SymbolRecord {
		return SymbolRecord{Symbol: sym, AssetType: "ETF", IsETF: true, Leverage: lev, Direction: "long", Category: "other", Active: active}
	}
	if err := SaveSymbols(db, []SymbolRecord{
		etf("BBB", "none", true), etf("AAA", "none", true), etf("TQQQ", "3x", true), etf("OLD", "none", false),
		{Symbol: "AAPL", AssetType: "CS", Leverage: "none", Direction: "long", Category: "common_stock", Active: true},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := UnleveragedETFs(db)
	if err != nil || len(got) != 2 || got[0] != "AAA" || got[1] != "BBB" {
		t.Fatalf("without avg_volume got %v, %v; want [AAA BBB]", got, err)
	}

	if _, err := db.Exec(`CREATE TABLE avg_volume (symbol TEXT, window_days INT, avg_volume REAL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO avg_volume VALUES ('BBB', 20, 900), ('AAA', 20, 5), ('AAA', 60, 99999)`); err != nil {
		t.Fatal(err)
	}
	got, err = UnleveragedETFs(db)
	if err != nil || len(got) != 2 || got[0] != "BBB" || got[1] != "AAA" {
		t.Fatalf("with avg_volume got %v, %v; want most traded first [BBB AAA]", got, err)
	}
}
