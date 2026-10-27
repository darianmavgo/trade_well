package storage

import (
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/jmoiron/sqlx"
)

// EnsureOptionTables creates the option history tables in the market DB.
//
//	option_contracts   one row per listed contract; bars_fetched_at marks that
//	                   its bars were already pulled so reruns skip it
//	option_bars        daily aggregates, one row per (ticker, Date)
//	option_expiry_scan which (underlying, expiry) chains were already listed,
//	                   including chains that came back empty (holiday expiries)
func EnsureOptionTables(db *sqlx.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS option_contracts (
			ticker TEXT PRIMARY KEY,
			underlying TEXT NOT NULL,
			expiry TEXT NOT NULL,
			strike REAL NOT NULL,
			contract_type TEXT NOT NULL,
			shares_per_contract INTEGER DEFAULT 100,
			bars_fetched_at TEXT,
			bar_count INTEGER DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_option_contracts_chain ON option_contracts(underlying, expiry, contract_type);
		CREATE TABLE IF NOT EXISTS option_bars (
			ticker TEXT NOT NULL,
			Date TEXT NOT NULL,
			open REAL, high REAL, low REAL, close REAL,
			volume INTEGER, trades INTEGER,
			PRIMARY KEY (ticker, Date)
		);
		CREATE TABLE IF NOT EXISTS option_expiry_scan (
			underlying TEXT NOT NULL,
			expiry TEXT NOT NULL,
			contracts INTEGER,
			scanned_at TEXT,
			PRIMARY KEY (underlying, expiry)
		);
	`)
	return err
}

// ExpiryScanned reports whether the chain for (underlying, expiry) was already listed.
func ExpiryScanned(db *sqlx.DB, underlying, expiry string) (bool, error) {
	var n int
	err := db.Get(&n, `SELECT COUNT(*) FROM option_expiry_scan WHERE underlying = ? AND expiry = ?`, underlying, expiry)
	return n > 0, err
}

func SaveExpiryScan(db *sqlx.DB, underlying, expiry string, contracts int) error {
	_, err := db.Exec(`INSERT OR REPLACE INTO option_expiry_scan (underlying, expiry, contracts, scanned_at) VALUES (?, ?, ?, ?)`,
		underlying, expiry, contracts, time.Now().UTC().Format(time.RFC3339))
	return err
}

func UpsertOptionContracts(db *sqlx.DB, cs []models.OptionContract) error {
	if len(cs) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// ON CONFLICT keeps bars_fetched_at/bar_count from an earlier run.
	stmt, err := tx.Prepare(`INSERT INTO option_contracts (ticker, underlying, expiry, strike, contract_type, shares_per_contract)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(ticker) DO UPDATE SET underlying=excluded.underlying, expiry=excluded.expiry,
			strike=excluded.strike, contract_type=excluded.contract_type, shares_per_contract=excluded.shares_per_contract`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, c := range cs {
		if _, err := stmt.Exec(c.Ticker, c.Underlying, c.Expiry, c.Strike, c.Right, c.SharesPerContract); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ContractFetchState returns whether a contract's bars were pulled before and
// the newest bar date stored for it ("" if none).
func ContractFetchState(db *sqlx.DB, ticker string) (fetched bool, lastBar string, err error) {
	var at *string
	if err = db.Get(&at, `SELECT bars_fetched_at FROM option_contracts WHERE ticker = ?`, ticker); err != nil {
		return false, "", err
	}
	var last *string
	if err = db.Get(&last, `SELECT MAX(Date) FROM option_bars WHERE ticker = ?`, ticker); err != nil {
		return false, "", err
	}
	if last != nil {
		lastBar = *last
	}
	return at != nil, lastBar, nil
}

func UpsertOptionBars(db *sqlx.DB, ticker string, bars []models.OptionBar) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO option_bars (ticker, Date, open, high, low, close, volume, trades) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, b := range bars {
		if _, err := stmt.Exec(b.Ticker, b.Date, b.Open, b.High, b.Low, b.Close, b.Volume, b.Trades); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE option_contracts SET bars_fetched_at = ?, bar_count = (SELECT COUNT(*) FROM option_bars WHERE ticker = ?) WHERE ticker = ?`,
		time.Now().UTC().Format(time.RFC3339), ticker, ticker); err != nil {
		return err
	}
	return tx.Commit()
}

// OptionChainSeries is one contract with all of its stored bars, oldest first.
type OptionChainSeries struct {
	Contract models.OptionContract
	Bars     []models.OptionBar
}

// FetchCallChains loads every stored call (with at least one bar) for the
// underlying, grouped by expiry.
func FetchCallChains(db *sqlx.DB, underlying string) (map[string][]OptionChainSeries, error) {
	var cs []models.OptionContract
	if err := db.Select(&cs, `SELECT ticker, underlying, expiry, strike, contract_type, shares_per_contract
		FROM option_contracts WHERE underlying = ? AND contract_type = 'call' AND bar_count > 0
		ORDER BY expiry, strike`, underlying); err != nil {
		return nil, err
	}
	out := make(map[string][]OptionChainSeries)
	for _, c := range cs {
		var bars []models.OptionBar
		if err := db.Select(&bars, `SELECT ticker, Date, open, high, low, close, volume, trades
			FROM option_bars WHERE ticker = ? ORDER BY Date`, c.Ticker); err != nil {
			return nil, err
		}
		out[c.Expiry] = append(out[c.Expiry], OptionChainSeries{Contract: c, Bars: bars})
	}
	return out, nil
}
