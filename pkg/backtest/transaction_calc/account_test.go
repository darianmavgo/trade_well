package transaction_calc

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

const stmtHeader = `Statement,Header,Field Name,Field Value
Statement,Data,Title,Transaction History
Transaction History,Header,Date,Account,Description,Transaction Type,Symbol,Quantity,Price,Price Currency,Gross Amount ,Commission,Net Amount
`

// A real market database with literal bars: VOO defines the calendar, AAA trades,
// and SPL is on a 10-for-1 split basis (the statement records its old, ten times larger price).
func accountFixture(t *testing.T) (csvPath, claimPath, market string) {
	t.Helper()
	dir := t.TempDir()
	market = filepath.Join(dir, "market.db")
	db, err := storage.OpenSQLite(market)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE backtest_start (idx INTEGER, symbol TEXT, Date TEXT, timeframe TEXT,
		open REAL, high REAL, low REAL, close REAL, volume INTEGER, "Adj Close" REAL)`); err != nil {
		t.Fatal(err)
	}
	bars := map[string][]float64{
		"VOO": {1, 1, 1, 1},
		"AAA": {100, 110, 121, 121},
		"SPL": {10, 11, 11, 12.1}, // adjusted: it traded at 100 and 110 on the old basis
	}
	days := []string{"2026-01-05", "2026-01-06", "2026-01-07", "2026-01-08"}
	for sym, closes := range bars {
		for i, c := range closes {
			if _, err := db.Exec(`INSERT INTO backtest_start (symbol, Date, timeframe, open, high, low, close, volume, "Adj Close")
				VALUES (?, ?, '1d', ?, ?, ?, ?, 1000, ?)`, sym, days[i], c, c, c, c, c); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Day 1: deposit 2000, buy 10 AAA at 100 and 10 SPL at 100 (SPL is 10 on the adjusted basis and cost 1000 for 10 old shares).
	// Day 3: deposit 5000 (a flow, not a return) and nothing else happens.
	csvPath = filepath.Join(dir, "t.csv")
	rows := stmtHeader +
		"Transaction History,Data,2026-01-05,U1,Cash,Deposit,-,-,-,-,2000.0,-,2000.0\n" +
		"Transaction History,Data,2026-01-05,U1,AAA INC,Buy,AAA,10.0,100.0,USD,-1000.0,0.0,-1000.0\n" +
		"Transaction History,Data,2026-01-05,U1,SPL INC,Buy,SPL,10.0,100.0,USD,-1000.0,0.0,-1000.0\n" +
		"Transaction History,Data,2026-01-07,U1,Cash,Deposit,-,-,-,-,5000.0,-,5000.0\n"
	if err := os.WriteFile(csvPath, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	claimPath = filepath.Join(dir, "claim.txt")
	claim := "Timeframe\tTotal \tCAR \tMax Drawdown \tCalmar Ratio\tAvg CAR \tAvg MDD \tAvg Trades/Year\n" +
		"1 year\t40.0%\t40.0%\t10.0%\t4.00\t40.0%\t10.0%\t100.0\n"
	if err := os.WriteFile(claimPath, []byte(claim), 0o644); err != nil {
		t.Fatal(err)
	}
	return csvPath, claimPath, market
}

func TestAccountTimeWeightsAndSplitAdjusts(t *testing.T) {
	csvPath, claimPath, market := accountFixture(t)
	out := filepath.Join(t.TempDir(), "acct.db")
	if err := Account(AccountConfig{CSV: csvPath, Claim: claimPath, DB: out, MarketDB: market}); err != nil {
		t.Fatal(err)
	}
	db, err := storage.OpenSQLite(out)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	type day struct {
		Date   string   `db:"date"`
		Equity float64  `db:"equity"`
		Idx    *float64 `db:"idx"`
	}
	var days []day
	if err := db.Select(&days, `SELECT date, equity, idx FROM ibkr_return_daily ORDER BY date`); err != nil {
		t.Fatal(err)
	}
	// The calendar runs from the first to the last transaction, here three trading days.
	if len(days) != 3 {
		t.Fatalf("%d days, want 3", len(days))
	}
	// Day 1: 10 AAA at 100 is 1000. SPL traded at 100 on the old basis, so its 10 shares are 100 shares on the
	// bars' basis, worth 100 x 10 = 1000. Cash is 0 after the 2000 deposit and the two buys.
	if math.Abs(days[0].Equity-2000) > 1e-6 {
		t.Fatalf("day 1 equity %.4f, want 2000 (split-adjusted shares)", days[0].Equity)
	}
	// Day 2: AAA 10 x 110 = 1100 and SPL 100 x 11 = 1100.
	if math.Abs(days[1].Equity-2200) > 1e-6 {
		t.Fatalf("day 2 equity %.4f, want 2200", days[1].Equity)
	}
	// Day 3: AAA 1210 + SPL 1100 + 5000 deposited cash = 7310. The deposit is not a return.
	if math.Abs(days[2].Equity-7310) > 1e-6 {
		t.Fatalf("day 3 equity %.4f, want 7310", days[2].Equity)
	}
	if days[1].Idx == nil || days[2].Idx == nil {
		t.Fatal("index missing")
	}
	// Day 2 is +10%. Day 3 earns AAA +10% and SPL +0%: (7310 - 5000) / 2200 - 1 = +5%.
	if math.Abs(*days[1].Idx-1.10) > 1e-9 {
		t.Fatalf("day 2 index %.6f, want 1.10", *days[1].Idx)
	}
	if got := *days[2].Idx / *days[1].Idx; math.Abs(got-1.05) > 1e-9 {
		t.Fatalf("day 3 return %.6f, want 1.05: the 5000 deposit must not count as return", got)
	}

	var claimed int
	if err := db.Get(&claimed, `SELECT COUNT(*) FROM ibkr_claim_vs_actual WHERE timeframe = '1 year' AND claimed_car = 0.4`); err != nil || claimed != 1 {
		t.Fatalf("claimed_performance row not loaded as fractions: %d %v", claimed, err)
	}
}
