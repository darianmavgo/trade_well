package transaction_calc

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/jmoiron/sqlx"
)

// AccountConfig controls the account reconstruction.
type AccountConfig struct {
	CSV      string // IBKR Transaction History export
	Claim    string // tab-separated claimed performance table (optional)
	DB       string // SQLite file that receives the tables
	MarketDB string // market database holding the daily bars
	Calendar string // symbol whose daily bars define the trading days, default VOO
	Out      io.Writer
}

// Account loads the statement into SQLite and reconstructs the account day by day:
// cash, shares held, equity at each close, and the time-weighted return, so its CAR
// and max drawdown can be set beside a claimed table. The loading is Go and the
// calculation is SQL (sql/stages/ibkr_account), one slice table per stage.
func Account(cfg AccountConfig) error {
	out := cfg.Out
	if out == nil {
		out = io.Discard
	}
	if cfg.Calendar == "" {
		cfg.Calendar = "VOO"
	}
	db, err := storage.OpenSQLite(cfg.DB)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // the ATTACH below must be seen by every later statement

	n, err := loadTransactions(db, cfg.CSV)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "ibkr_transactions: %d rows from %s\n", n, cfg.CSV)
	if cfg.Claim != "" {
		c, err := loadClaim(db, cfg.Claim)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "claimed_performance: %d timeframes from %s\n", c, cfg.Claim)
	} else if _, err := db.Exec(claimSchema); err != nil {
		return err
	}
	if _, err := db.Exec(`ATTACH DATABASE ? AS mkt`, cfg.MarketDB); err != nil {
		return fmt.Errorf("attach %s: %w", cfg.MarketDB, err)
	}
	if !safeSymbol(cfg.Calendar) {
		return fmt.Errorf("calendar symbol %q", cfg.Calendar)
	}
	return storage.RunStage(db, "ibkr_account", map[string]string{"__CALENDAR_SYMBOL__": strings.ToUpper(cfg.Calendar)})
}

func safeSymbol(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range strings.ToUpper(s) {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}

const txnSchema = `
DROP TABLE IF EXISTS ibkr_transactions;
CREATE TABLE ibkr_transactions (
	date TEXT NOT NULL, account TEXT, description TEXT, type TEXT, symbol TEXT,
	quantity REAL, price REAL, price_currency TEXT, gross_amount REAL, commission REAL, net_amount REAL
)`

const claimSchema = `
CREATE TABLE IF NOT EXISTS claimed_performance (
	timeframe TEXT PRIMARY KEY, total_return REAL, car REAL, max_drawdown REAL, calmar REAL,
	avg_car REAL, avg_mdd REAL, avg_trades_per_year REAL
)`

// num reads a statement cell: "-" or empty is NULL.
func num(s string) any {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" || s == "-" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return v
}

func text(s string) any {
	if s = strings.TrimSpace(s); s == "" || s == "-" {
		return nil
	}
	return s
}

// loadTransactions replaces ibkr_transactions with the Transaction History rows of the export.
func loadTransactions(db *sqlx.DB, path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	for _, q := range strings.Split(txnSchema, ";") {
		if _, err := db.Exec(q); err != nil {
			return 0, err
		}
	}
	tx, err := db.Beginx()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var col map[string]int
	n := 0
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		if len(rec) < 3 || rec[0] != "Transaction History" {
			continue
		}
		if rec[1] == "Header" {
			col = map[string]int{}
			for i, c := range rec {
				col[strings.TrimSpace(c)] = i
			}
			continue
		}
		if rec[1] != "Data" || col == nil {
			continue
		}
		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(rec) {
				return rec[i]
			}
			return ""
		}
		if _, err := tx.Exec(`INSERT INTO ibkr_transactions (date, account, description, type, symbol, quantity, price, price_currency, gross_amount, commission, net_amount)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			strings.TrimSpace(get("Date")), text(get("Account")), text(get("Description")), text(get("Transaction Type")), text(get("Symbol")),
			num(get("Quantity")), num(get("Price")), text(get("Price Currency")), num(get("Gross Amount")), num(get("Commission")), num(get("Net Amount"))); err != nil {
			return 0, err
		}
		n++
	}
	return n, tx.Commit()
}

// loadClaim replaces claimed_performance with the rows of a tab-separated table whose columns are
// Timeframe, Total, CAR, Max Drawdown, Calmar Ratio, Avg CAR, Avg MDD, Avg Trades/Year.
// Percentages become fractions.
func loadClaim(db *sqlx.DB, path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	if _, err := db.Exec(`DROP TABLE IF EXISTS claimed_performance`); err != nil {
		return 0, err
	}
	if _, err := db.Exec(claimSchema); err != nil {
		return 0, err
	}
	pct := func(s string) any {
		s = strings.TrimSpace(s)
		if strings.HasSuffix(s, "%") {
			if v, ok := num(strings.TrimSuffix(s, "%")).(float64); ok {
				return v / 100
			}
			return nil
		}
		return num(s)
	}
	n := 0
	for i := 0; ; i++ {
		rec, err := r.Read()
		if err != nil {
			break
		}
		if i == 0 || len(rec) < 8 || strings.TrimSpace(rec[0]) == "" {
			continue // the header
		}
		if _, err := db.Exec(`INSERT INTO claimed_performance (timeframe, total_return, car, max_drawdown, calmar, avg_car, avg_mdd, avg_trades_per_year)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			strings.TrimSpace(rec[0]), pct(rec[1]), pct(rec[2]), pct(rec[3]), num(rec[4]), pct(rec[5]), pct(rec[6]), num(rec[7])); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
