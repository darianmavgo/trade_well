// Package refdb is the reference database (refdata/strategies.db): strategy
// configs live in SQLite tables (streak, tree, markov, hold, rotation, price action reclaim).
package refdb

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"os"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/jmoiron/sqlx"
)

// DefaultPath is the reference DB location (APP_FOLDER/refdata/strategies.db).
var DefaultPath = appenv.RefDB()

const schema = `
-- One runnable streak strategy per row. pkg/streak_strategy registers each
-- row. take_profit_pct and stop_loss_pct are fractional offsets (0.08 = 8%),
-- matching gridsearch_results, not StrategyConfig.StopLossPct's multiplier.
CREATE TABLE IF NOT EXISTS streak_strategy (
	id               TEXT PRIMARY KEY,
	name             TEXT NOT NULL,
	signal_symbol    TEXT NOT NULL,
	trade_symbol     TEXT NOT NULL,
	direction        TEXT NOT NULL,
	signal_days      INTEGER NOT NULL,
	hold_days        INTEGER NOT NULL,
	take_profit_pct  REAL NOT NULL,
	stop_loss_pct    REAL NOT NULL,
	regime           TEXT NOT NULL,
	allocation_pct   REAL NOT NULL,
	cash_yield       REAL NOT NULL,
	slippage_pct     REAL NOT NULL,
	next_day_limit   INTEGER NOT NULL,
	source_strategy  TEXT,
	source_label     TEXT,
	win_rate         REAL,
	total_trades     INTEGER
);
-- One runnable markov strategy per row, similar to streak_strategy.
CREATE TABLE IF NOT EXISTS tree_strategy (
	id               TEXT PRIMARY KEY,
	name             TEXT NOT NULL,
	signal_symbol    TEXT NOT NULL,
	trade_symbol     TEXT NOT NULL,
	direction        TEXT NOT NULL,
	hold_days        INTEGER NOT NULL,
	take_profit_pct  REAL NOT NULL,
	stop_loss_pct    REAL NOT NULL,
	allocation_pct   REAL NOT NULL,
	cash_yield       REAL NOT NULL,
	slippage_pct     REAL NOT NULL,
	next_day_limit   INTEGER NOT NULL,
	coil_range_max   REAL NOT NULL,
	sma_bounce_min   REAL NOT NULL,
	sma_bounce_max   REAL NOT NULL,
	source_strategy  TEXT,
	source_label     TEXT,
	win_rate         REAL,
	total_trades     INTEGER
);

CREATE TABLE IF NOT EXISTS markov_strategy (
	id               TEXT PRIMARY KEY,
	name             TEXT NOT NULL,
	signal_symbol    TEXT NOT NULL,
	trade_symbol     TEXT NOT NULL,
	direction        TEXT NOT NULL,
	target_state     TEXT NOT NULL,
	hold_days        INTEGER NOT NULL,
	take_profit_pct  REAL NOT NULL,
	stop_loss_pct    REAL NOT NULL,
	allocation_pct   REAL NOT NULL,
	cash_yield       REAL NOT NULL,
	slippage_pct     REAL NOT NULL,
	next_day_limit   INTEGER NOT NULL,
	source_strategy  TEXT,
	source_label     TEXT,
	win_rate         REAL,
	total_trades     INTEGER
);

-- One runnable hmm strategy per row (pkg/hmm_strategy): trade trade_symbol from the regime the
-- hidden Markov model of hmm_symbol is in. rule is calm_filter (long while the probability of the
-- stressed regime is under stress_prob), calm_down (long when it falls back under stress_prob) or
-- normal_filter (long while the middle volatility regime is the most likely one, which ignores
-- stress_prob) or stress_filter (long while the probability of the stressed regime is at or over stress_prob).
CREATE TABLE IF NOT EXISTS hmm_strategy (
	id               TEXT PRIMARY KEY,
	name             TEXT NOT NULL,
	hmm_symbol       TEXT NOT NULL DEFAULT 'QQQ',
	trade_symbol     TEXT NOT NULL DEFAULT 'QQQ',
	rule             TEXT NOT NULL,
	stress_prob      REAL NOT NULL,
	hold_days        INTEGER NOT NULL,
	take_profit_pct  REAL NOT NULL,
	stop_loss_pct    REAL NOT NULL,
	allocation_pct   REAL NOT NULL,
	cash_yield       REAL NOT NULL,
	slippage_pct     REAL NOT NULL,
	source_strategy  TEXT,
	source_label     TEXT,
	win_rate         REAL,
	total_trades     INTEGER
);

-- One rotation strategy per row (pkg/rotation_strategy): rank the liquid
-- universe on momentum each session and hold the top few. symbols is a comma
-- separated candidate list, or a symbol_lists.symbol_list_id to use that list,
-- or empty for every symbol in the market database.
-- regime_sma = 0 turns the market gate off. period is empty for that daily
-- momentum ranking, or 1d, 1w, 1m, 1q, 1y for the period winner rule: at the
-- start of each calendar period hold the top_k names by the previous period's
-- return until that period's last session. side is long, short or inverse (the
-- matched inverse ETF of each pick); it only applies to a period row. pick is winner
-- (best previous period return) or loser (worst). entry_limit_pct above 0 buys on
-- the entry session with a limit at that fraction of the previous close (0.9 is 10%
-- under) and skips the period if the low never reaches it. take_profit_pct above 0
-- sells at that gain over the entry, also on the entry session if the high gets there.
-- hold_days above 0 sells at the close that many sessions after the entry session, if the
-- period has not ended first (on a 1d period there is no earlier period-end sale: the hold
-- window, take profit and stop are the exits). pick = all enters every name of the list each
-- period, sized at max_weight_pct of equity, instead of ranking. stop_loss_pct is a
-- fractional offset under the entry. cooldown = month takes a name out of the rotation for
-- the rest of the calendar month once an entry in it fills.
CREATE TABLE IF NOT EXISTS rotation_strategy (
	id               TEXT PRIMARY KEY,
	name             TEXT NOT NULL,
	symbols          TEXT NOT NULL DEFAULT '',
	universe_size    INTEGER NOT NULL,
	top_k            INTEGER NOT NULL,
	exit_buffer      INTEGER NOT NULL,
	max_weight_pct   REAL NOT NULL,
	regime_symbol    TEXT NOT NULL DEFAULT 'QQQ',
	regime_sma       INTEGER NOT NULL DEFAULT 0,
	allocation_pct   REAL NOT NULL,
	cash_yield       REAL NOT NULL,
	slippage_pct     REAL NOT NULL,
	period           TEXT NOT NULL DEFAULT '',
	side             TEXT NOT NULL DEFAULT 'long',
	pick             TEXT NOT NULL DEFAULT 'winner',
	entry_limit_pct  REAL NOT NULL DEFAULT 0,
	take_profit_pct  REAL NOT NULL DEFAULT 0,
	hold_days        INTEGER NOT NULL DEFAULT 0,
	stop_loss_pct    REAL NOT NULL DEFAULT 0,
	cooldown         TEXT NOT NULL DEFAULT ''
);

-- One price action reclaim strategy per row (pkg/price_action_reclaim_strategy): trade one
-- symbol when a close falls under its support and a later close reclaims it, in an uptrend.
-- Support is the lowest low from support_bars bars back to reclaim_gap bars back, so the
-- breakdown bar does not lower the level. The uptrend is a close at or over its sma_period
-- bar average. The stop sits stop_cushion_pct under support, and a trade whose stop is more
-- than max_stop_pct under the close is skipped. take_profit_pct is a fractional offset over
-- the entry; hold_days are sessions.
CREATE TABLE IF NOT EXISTS price_action_reclaim_strategy (
	id               TEXT PRIMARY KEY,
	name             TEXT NOT NULL,
	symbol           TEXT NOT NULL,
	sma_period       INTEGER NOT NULL DEFAULT 200,
	support_bars     INTEGER NOT NULL DEFAULT 25,
	reclaim_gap      INTEGER NOT NULL DEFAULT 5,
	stop_cushion_pct REAL NOT NULL DEFAULT 0.005,
	max_stop_pct     REAL NOT NULL DEFAULT 0.15,
	take_profit_pct  REAL NOT NULL DEFAULT 0.10,
	hold_days        INTEGER NOT NULL DEFAULT 20,
	allocation_pct   REAL NOT NULL DEFAULT 0.10,
	cash_yield       REAL NOT NULL DEFAULT 0,
	slippage_pct     REAL NOT NULL DEFAULT 0.0005
);

-- Named symbol lists a strategy or a command can take as its basket. list is the
-- tickers, upper case, comma separated, sorted. sql/seed/symbol_lists.sql fills it.
CREATE TABLE IF NOT EXISTS symbol_lists (
	symbol_list_id TEXT PRIMARY KEY,
	list         TEXT NOT NULL
);

-- Named stacks. name is the friendly label people type; id is the stack's
-- members joined with "+" (strategy.StackID), the key every result DB uses.
-- sql/seed/stack.sql fills it.
CREATE TABLE IF NOT EXISTS stack (
	name         TEXT PRIMARY KEY,
	id           TEXT NOT NULL UNIQUE,
	note         TEXT NOT NULL DEFAULT ''
);

-- Where each strategy's most recent backtest lives. result_db is relative to the
-- app folder (data/reports/3/streak.db). Written by "backtest lastrun"; rows for
-- strategies that were never backtested do not exist. strategy_id is any id a
-- result DB uses, so stacks ("a+b") and built-ins are here too.
CREATE TABLE IF NOT EXISTS strategy_last_backtest (
	strategy_id  TEXT PRIMARY KEY,
	result_db    TEXT NOT NULL,
	created_at   TEXT NOT NULL DEFAULT '',
	win_rate     REAL,
	total_trades INTEGER NOT NULL DEFAULT 0
);
`

// Open opens (creating if needed) the reference DB and ensures its schema.
func Open(path string) (*sqlx.DB, error) {
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("refdb schema: %w", err)
	}
	if err := upgradeRotation(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("refdb rotation upgrade: %w", err)
	}
	if err := upgradeHold(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("refdb hold upgrade: %w", err)
	}
	// strategy_family_param: what gridsearch can vary per family (sql/stages/family_params).
	if err := storage.RunStage(db, "family_params", nil); err != nil {
		db.Close()
		return nil, fmt.Errorf("refdb family params: %w", err)
	}
	return db, nil
}

// upgradeRotation adds period (” = daily momentum ranking) and side ('long')
// and pick ('winner') to a rotation_strategy table made before they existed.
func upgradeRotation(db *sqlx.DB) error {
	var cols []string
	if err := db.Select(&cols, `SELECT name FROM pragma_table_info('rotation_strategy')`); err != nil {
		return err
	}
	have := map[string]bool{}
	for _, c := range cols {
		have[c] = true
	}
	if len(cols) == 0 {
		return nil
	}
	if !have["period"] {
		if _, err := db.Exec(`ALTER TABLE rotation_strategy ADD COLUMN period TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !have["side"] {
		if _, err := db.Exec(`ALTER TABLE rotation_strategy ADD COLUMN side TEXT NOT NULL DEFAULT 'long'`); err != nil {
			return err
		}
	}
	if !have["pick"] {
		if _, err := db.Exec(`ALTER TABLE rotation_strategy ADD COLUMN pick TEXT NOT NULL DEFAULT 'winner'`); err != nil {
			return err
		}
	}
	if !have["hold_days"] {
		if _, err := db.Exec(`ALTER TABLE rotation_strategy ADD COLUMN hold_days INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	for _, c := range []string{"entry_limit_pct", "take_profit_pct", "stop_loss_pct"} {
		if !have[c] {
			if _, err := db.Exec(`ALTER TABLE rotation_strategy ADD COLUMN ` + c + ` REAL NOT NULL DEFAULT 0`); err != nil {
				return err
			}
		}
	}
	if !have["cooldown"] {
		if _, err := db.Exec(`ALTER TABLE rotation_strategy ADD COLUMN cooldown TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	return nil
}

// upgradeHold brings an older hold_strategy table up to date on open. It adds
// trailing_stop_pct and sma_reentry_period (0 = never) when they are missing and
// copies in any hold_bail_strategy rows not yet there, which is how the hold_bail
// family was merged into hold. It never drops or deletes, and does nothing on a
// database without a hold_strategy table.
func upgradeHold(db *sqlx.DB) error {
	var cols []string
	if err := db.Select(&cols, `SELECT name FROM pragma_table_info('hold_strategy')`); err != nil || len(cols) == 0 {
		return err
	}
	have := map[string]bool{}
	for _, c := range cols {
		have[c] = true
	}
	if !have["trailing_stop_pct"] {
		if _, err := db.Exec(`ALTER TABLE hold_strategy ADD COLUMN trailing_stop_pct REAL NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	// The three exits every family has. 0 is no take-profit or stop, and a hold of
	// 0 or 99999 is no time limit. 99999 is the default so existing rows do not move.
	for col, def := range map[string]string{"take_profit_pct": "REAL NOT NULL DEFAULT 0", "stop_loss_pct": "REAL NOT NULL DEFAULT 0", "hold_days": "INTEGER NOT NULL DEFAULT 99999"} {
		if !have[col] {
			if _, err := db.Exec(`ALTER TABLE hold_strategy ADD COLUMN ` + col + ` ` + def); err != nil {
				return err
			}
		}
	}
	if !have["regime_symbol"] {
		if _, err := db.Exec(`ALTER TABLE hold_strategy ADD COLUMN regime_symbol TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !have["sma_reentry_period"] {
		if _, err := db.Exec(`ALTER TABLE hold_strategy ADD COLUMN sma_reentry_period INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if !have["covered_call_otm_pct"] {
		if _, err := db.Exec(`ALTER TABLE hold_strategy ADD COLUMN covered_call_otm_pct REAL NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'hold_bail_strategy'`); err != nil || n == 0 {
		return err
	}
	_, err := db.Exec(`
		INSERT OR IGNORE INTO hold_strategy
			(id, name, symbol, total_return, allocation_pct, cash_yield, slippage_pct, trailing_stop_pct, sma_reentry_period)
		SELECT id, name, symbol, 0, allocation_pct, cash_yield, slippage_pct, trailing_stop_pct, sma_reentry_period
		FROM hold_bail_strategy`)
	return err
}

// OpenExisting opens the reference DB only if the file already exists and is
// non-empty, without creating anything. Returns nil, nil otherwise.
func OpenExisting(path string) (*sqlx.DB, error) {
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	return storage.OpenSQLite(path)
}

// Universe returns the symbols in a list, sorted.
func Universe(db *sqlx.DB, list string) ([]string, error) {
	var syms []string
	err := db.Select(&syms, `SELECT symbol FROM etf_universe WHERE list = ? ORDER BY symbol`, list)
	return syms, err
}

// SaveUniverse replaces a list's contents.
func SaveUniverse(db *sqlx.DB, list string, symbols []string) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM etf_universe WHERE list = ?`, list); err != nil {
		tx.Rollback()
		return err
	}
	for _, s := range symbols {
		s = strings.ToUpper(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO etf_universe (list, symbol) VALUES (?, ?)`, list, s); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// StreakStrategy is one row of streak_strategy: the symbol watched for the
// streak, the symbol bought, and the grid-searched exit parameters.
// TakeProfitPct and StopLossPct are fractional offsets (0.08 = 8%).
type StreakStrategy struct {
	ID             string   `db:"id"`
	Name           string   `db:"name"`
	SignalSymbol   string   `db:"signal_symbol"`
	TradeSymbol    string   `db:"trade_symbol"`
	Direction      string   `db:"direction"`
	SignalDays     int      `db:"signal_days"`
	HoldDays       int      `db:"hold_days"`
	TakeProfitPct  float64  `db:"take_profit_pct"`
	StopLossPct    float64  `db:"stop_loss_pct"`
	Regime         string   `db:"regime"`
	AllocationPct  float64  `db:"allocation_pct"`
	CashYield      float64  `db:"cash_yield"`
	SlippagePct    float64  `db:"slippage_pct"`
	NextDayLimit   int      `db:"next_day_limit"`
	SourceStrategy string   `db:"source_strategy"`
	SourceLabel    string   `db:"source_label"`
	WinRate        *float64 `db:"win_rate"`
	TotalTrades    *int     `db:"total_trades"`
}

// StreakStrategies returns every streak_strategy row, ordered by id.
// A missing table returns an error; callers that register strategies treat
// that as an empty list.
func StreakStrategies(db *sqlx.DB) ([]StreakStrategy, error) { return streakStrategiesWhere(db, "") }

// StreakStrategyByID returns the row whose id is exactly id.
func StreakStrategyByID(db *sqlx.DB, id string) (StreakStrategy, bool, error) {
	rows, err := streakStrategiesWhere(db, " WHERE id = ?", id)
	if err != nil || len(rows) == 0 {
		return StreakStrategy{}, false, err
	}
	return rows[0], true, nil
}

func streakStrategiesWhere(db *sqlx.DB, where string, args ...any) ([]StreakStrategy, error) {
	var out []StreakStrategy
	err := db.Select(&out, `
		SELECT id, name, signal_symbol, trade_symbol, direction, signal_days, hold_days,
		       take_profit_pct, stop_loss_pct, regime, allocation_pct, cash_yield,
		       slippage_pct, next_day_limit,
		       COALESCE(source_strategy, '') AS source_strategy,
		       COALESCE(source_label, '') AS source_label,
		       win_rate, total_trades
		FROM streak_strategy`+where+` ORDER BY id`, args...)
	return out, err
}

// UpsertStreakStrategies inserts or replaces each row by id.
func UpsertStreakStrategies(db *sqlx.DB, rows []StreakStrategy) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	const q = `
		INSERT INTO streak_strategy (
			id, name, signal_symbol, trade_symbol, direction, signal_days, hold_days,
			take_profit_pct, stop_loss_pct, regime, allocation_pct, cash_yield,
			slippage_pct, next_day_limit, source_strategy, source_label, win_rate, total_trades
		) VALUES (
			:id, :name, :signal_symbol, :trade_symbol, :direction, :signal_days, :hold_days,
			:take_profit_pct, :stop_loss_pct, :regime, :allocation_pct, :cash_yield,
			:slippage_pct, :next_day_limit, :source_strategy, :source_label, :win_rate, :total_trades
		)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			signal_symbol = excluded.signal_symbol,
			trade_symbol = excluded.trade_symbol,
			direction = excluded.direction,
			signal_days = excluded.signal_days,
			hold_days = excluded.hold_days,
			take_profit_pct = excluded.take_profit_pct,
			stop_loss_pct = excluded.stop_loss_pct,
			regime = excluded.regime,
			allocation_pct = excluded.allocation_pct,
			cash_yield = excluded.cash_yield,
			slippage_pct = excluded.slippage_pct,
			next_day_limit = excluded.next_day_limit,
			source_strategy = excluded.source_strategy,
			source_label = excluded.source_label,
			win_rate = excluded.win_rate,
			total_trades = excluded.total_trades`
	for _, r := range rows {
		if _, err := tx.NamedExec(q, r); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// HMMStrategy is one row of hmm_strategy.
type HMMStrategy struct {
	ID             string   `db:"id"`
	Name           string   `db:"name"`
	HMMSymbol      string   `db:"hmm_symbol"`
	TradeSymbol    string   `db:"trade_symbol"`
	Rule           string   `db:"rule"`
	StressProb     float64  `db:"stress_prob"`
	HoldDays       int      `db:"hold_days"`
	TakeProfitPct  float64  `db:"take_profit_pct"`
	StopLossPct    float64  `db:"stop_loss_pct"`
	AllocationPct  float64  `db:"allocation_pct"`
	CashYield      float64  `db:"cash_yield"`
	SlippagePct    float64  `db:"slippage_pct"`
	SourceStrategy string   `db:"source_strategy"`
	SourceLabel    string   `db:"source_label"`
	WinRate        *float64 `db:"win_rate"`
	TotalTrades    *int     `db:"total_trades"`
}

// HMMStrategies returns every hmm_strategy row, ordered by id.
func HMMStrategies(db *sqlx.DB) ([]HMMStrategy, error) { return hmmStrategiesWhere(db, "") }

// HMMStrategyByID returns the row whose id is exactly id.
func HMMStrategyByID(db *sqlx.DB, id string) (HMMStrategy, bool, error) {
	rows, err := hmmStrategiesWhere(db, " WHERE id = ?", id)
	if err != nil || len(rows) == 0 {
		return HMMStrategy{}, false, err
	}
	return rows[0], true, nil
}

func hmmStrategiesWhere(db *sqlx.DB, where string, args ...any) ([]HMMStrategy, error) {
	var out []HMMStrategy
	err := db.Select(&out, `
		SELECT id, name, hmm_symbol, trade_symbol, rule, stress_prob, hold_days,
		       take_profit_pct, stop_loss_pct, allocation_pct, cash_yield,
		       slippage_pct,
		       COALESCE(source_strategy, '') AS source_strategy,
		       COALESCE(source_label, '') AS source_label,
		       win_rate, total_trades
		FROM hmm_strategy`+where+` ORDER BY id`, args...)
	return out, err
}

// UpsertHMMStrategies inserts or replaces each row by id.
func UpsertHMMStrategies(db *sqlx.DB, rows []HMMStrategy) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	const q = `
		INSERT INTO hmm_strategy (
			id, name, hmm_symbol, trade_symbol, rule, stress_prob, hold_days,
			take_profit_pct, stop_loss_pct, allocation_pct, cash_yield,
			slippage_pct, source_strategy, source_label, win_rate, total_trades
		) VALUES (
			:id, :name, :hmm_symbol, :trade_symbol, :rule, :stress_prob, :hold_days,
			:take_profit_pct, :stop_loss_pct, :allocation_pct, :cash_yield,
			:slippage_pct, :source_strategy, :source_label, :win_rate, :total_trades
		)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			hmm_symbol = excluded.hmm_symbol,
			trade_symbol = excluded.trade_symbol,
			rule = excluded.rule,
			stress_prob = excluded.stress_prob,
			hold_days = excluded.hold_days,
			take_profit_pct = excluded.take_profit_pct,
			stop_loss_pct = excluded.stop_loss_pct,
			allocation_pct = excluded.allocation_pct,
			cash_yield = excluded.cash_yield,
			slippage_pct = excluded.slippage_pct,
			source_strategy = excluded.source_strategy,
			source_label = excluded.source_label,
			win_rate = excluded.win_rate,
			total_trades = excluded.total_trades`
	for _, r := range rows {
		if _, err := tx.NamedExec(q, r); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// MarkovStrategy is one row of markov_strategy.
type MarkovStrategy struct {
	ID             string   `db:"id"`
	Name           string   `db:"name"`
	SignalSymbol   string   `db:"signal_symbol"`
	TradeSymbol    string   `db:"trade_symbol"`
	Direction      string   `db:"direction"`
	TargetState    string   `db:"target_state"`
	HoldDays       int      `db:"hold_days"`
	TakeProfitPct  float64  `db:"take_profit_pct"`
	StopLossPct    float64  `db:"stop_loss_pct"`
	AllocationPct  float64  `db:"allocation_pct"`
	CashYield      float64  `db:"cash_yield"`
	SlippagePct    float64  `db:"slippage_pct"`
	NextDayLimit   int      `db:"next_day_limit"`
	SourceStrategy string   `db:"source_strategy"`
	SourceLabel    string   `db:"source_label"`
	WinRate        *float64 `db:"win_rate"`
	TotalTrades    *int     `db:"total_trades"`
}

// MarkovStrategies returns every markov_strategy row, ordered by id.
func MarkovStrategies(db *sqlx.DB) ([]MarkovStrategy, error) { return markovStrategiesWhere(db, "") }

// MarkovStrategyByID returns the row whose id is exactly id.
func MarkovStrategyByID(db *sqlx.DB, id string) (MarkovStrategy, bool, error) {
	rows, err := markovStrategiesWhere(db, " WHERE id = ?", id)
	if err != nil || len(rows) == 0 {
		return MarkovStrategy{}, false, err
	}
	return rows[0], true, nil
}

func markovStrategiesWhere(db *sqlx.DB, where string, args ...any) ([]MarkovStrategy, error) {
	var out []MarkovStrategy
	err := db.Select(&out, `
		SELECT id, name, signal_symbol, trade_symbol, direction, target_state, hold_days,
		       take_profit_pct, stop_loss_pct, allocation_pct, cash_yield,
		       slippage_pct, next_day_limit,
		       COALESCE(source_strategy, '') AS source_strategy,
		       COALESCE(source_label, '') AS source_label,
		       win_rate, total_trades
		FROM markov_strategy`+where+` ORDER BY id`, args...)
	return out, err
}

// UpsertMarkovStrategies inserts or replaces each row by id.
func UpsertMarkovStrategies(db *sqlx.DB, rows []MarkovStrategy) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	const q = `
CREATE TABLE IF NOT EXISTS tree_strategy (
	id               TEXT PRIMARY KEY,
	name             TEXT NOT NULL,
	signal_symbol    TEXT NOT NULL,
	trade_symbol     TEXT NOT NULL,
	direction        TEXT NOT NULL,
	hold_days        INTEGER NOT NULL,
	take_profit_pct  REAL NOT NULL,
	stop_loss_pct    REAL NOT NULL,
	allocation_pct   REAL NOT NULL,
	cash_yield       REAL NOT NULL,
	slippage_pct     REAL NOT NULL,
	next_day_limit   INTEGER NOT NULL,
	coil_range_max   REAL NOT NULL,
	sma_bounce_min   REAL NOT NULL,
	sma_bounce_max   REAL NOT NULL,
	source_strategy  TEXT,
	source_label     TEXT,
	win_rate         REAL,
	total_trades     INTEGER
);

		INSERT INTO markov_strategy (
			id, name, signal_symbol, trade_symbol, direction, target_state, hold_days,
			take_profit_pct, stop_loss_pct, allocation_pct, cash_yield,
			slippage_pct, next_day_limit, source_strategy, source_label, win_rate, total_trades
		) VALUES (
			:id, :name, :signal_symbol, :trade_symbol, :direction, :target_state, :hold_days,
			:take_profit_pct, :stop_loss_pct, :allocation_pct, :cash_yield,
			:slippage_pct, :next_day_limit, :source_strategy, :source_label, :win_rate, :total_trades
		)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			signal_symbol = excluded.signal_symbol,
			trade_symbol = excluded.trade_symbol,
			direction = excluded.direction,
			target_state = excluded.target_state,
			hold_days = excluded.hold_days,
			take_profit_pct = excluded.take_profit_pct,
			stop_loss_pct = excluded.stop_loss_pct,
			allocation_pct = excluded.allocation_pct,
			cash_yield = excluded.cash_yield,
			slippage_pct = excluded.slippage_pct,
			next_day_limit = excluded.next_day_limit,
			source_strategy = excluded.source_strategy,
			source_label = excluded.source_label,
			win_rate = excluded.win_rate,
			total_trades = excluded.total_trades`
	for _, r := range rows {
		if _, err := tx.NamedExec(q, r); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// TreeStrategy is one row of tree_strategy.
type TreeStrategy struct {
	ID             string   `db:"id"`
	Name           string   `db:"name"`
	SignalSymbol   string   `db:"signal_symbol"`
	TradeSymbol    string   `db:"trade_symbol"`
	Direction      string   `db:"direction"`
	HoldDays       int      `db:"hold_days"`
	TakeProfitPct  float64  `db:"take_profit_pct"`
	StopLossPct    float64  `db:"stop_loss_pct"`
	AllocationPct  float64  `db:"allocation_pct"`
	CashYield      float64  `db:"cash_yield"`
	SlippagePct    float64  `db:"slippage_pct"`
	NextDayLimit   int      `db:"next_day_limit"`
	CoilRangeMax   float64  `db:"coil_range_max"`
	SMABounceMin   float64  `db:"sma_bounce_min"`
	SMABounceMax   float64  `db:"sma_bounce_max"`
	SourceStrategy string   `db:"source_strategy"`
	SourceLabel    string   `db:"source_label"`
	WinRate        *float64 `db:"win_rate"`
	TotalTrades    *int     `db:"total_trades"`
}

// TreeStrategies returns every tree_strategy row, ordered by id.
func TreeStrategies(db *sqlx.DB) ([]TreeStrategy, error) { return treeStrategiesWhere(db, "") }

// TreeStrategyByID returns the row whose id is exactly id.
func TreeStrategyByID(db *sqlx.DB, id string) (TreeStrategy, bool, error) {
	rows, err := treeStrategiesWhere(db, " WHERE id = ?", id)
	if err != nil || len(rows) == 0 {
		return TreeStrategy{}, false, err
	}
	return rows[0], true, nil
}

func treeStrategiesWhere(db *sqlx.DB, where string, args ...any) ([]TreeStrategy, error) {
	var out []TreeStrategy
	err := db.Select(&out, `
		SELECT id, name, signal_symbol, trade_symbol, direction, hold_days,
		       take_profit_pct, stop_loss_pct, allocation_pct, cash_yield,
		       slippage_pct, next_day_limit, coil_range_max, sma_bounce_min, sma_bounce_max,
		       COALESCE(source_strategy, '') AS source_strategy,
		       COALESCE(source_label, '') AS source_label,
		       win_rate, total_trades
		FROM tree_strategy`+where+` ORDER BY id`, args...)
	return out, err
}

// UpsertTreeStrategies inserts or replaces each row by id.
func UpsertTreeStrategies(db *sqlx.DB, rows []TreeStrategy) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	const q = `
		INSERT INTO tree_strategy (
			id, name, signal_symbol, trade_symbol, direction, hold_days,
			take_profit_pct, stop_loss_pct, allocation_pct, cash_yield,
			slippage_pct, next_day_limit, coil_range_max, sma_bounce_min, sma_bounce_max,
			source_strategy, source_label, win_rate, total_trades
		) VALUES (
			:id, :name, :signal_symbol, :trade_symbol, :direction, :hold_days,
			:take_profit_pct, :stop_loss_pct, :allocation_pct, :cash_yield,
			:slippage_pct, :next_day_limit, :coil_range_max, :sma_bounce_min, :sma_bounce_max,
			:source_strategy, :source_label, :win_rate, :total_trades
		)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			signal_symbol = excluded.signal_symbol,
			trade_symbol = excluded.trade_symbol,
			direction = excluded.direction,
			hold_days = excluded.hold_days,
			take_profit_pct = excluded.take_profit_pct,
			stop_loss_pct = excluded.stop_loss_pct,
			allocation_pct = excluded.allocation_pct,
			cash_yield = excluded.cash_yield,
			slippage_pct = excluded.slippage_pct,
			next_day_limit = excluded.next_day_limit,
			coil_range_max = excluded.coil_range_max,
			sma_bounce_min = excluded.sma_bounce_min,
			sma_bounce_max = excluded.sma_bounce_max,
			source_strategy = excluded.source_strategy,
			source_label = excluded.source_label,
			win_rate = excluded.win_rate,
			total_trades = excluded.total_trades`
	for _, r := range rows {
		if _, err := tx.NamedExec(q, r); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// HoldStrategy is one row of hold_strategy.
type HoldStrategy struct {
	ID            string  `db:"id"`
	Name          string  `db:"name"`
	Symbol        string  `db:"symbol"`
	TotalReturn   int     `db:"total_return"`
	AllocationPct float64 `db:"allocation_pct"`
	CashYield     float64 `db:"cash_yield"`
	SlippagePct   float64 `db:"slippage_pct"`
	// TrailingStopPct is the trailing stop as a fraction of the high since entry;
	// 0 never bails. SMAReentryPeriod is the length of the average whose close
	// above it re-enters after a bail; 0 never re-enters.
	TrailingStopPct  float64 `db:"trailing_stop_pct"`
	SMAReentryPeriod int     `db:"sma_reentry_period"`
	// RegimeSymbol, when set, makes the row long unless the trained Markov state
	// of that symbol is bear: it sells on a bear bar and buys back on the first
	// sideways or bull bar. "" holds without a regime.
	RegimeSymbol string `db:"regime_symbol"`
	// The family's exits, as every other family has them. TakeProfitPct and
	// StopLossPct are fractional offsets (0.08 = 8%), 0 is not set. HoldDays of 0
	// or 99999 is no time limit.
	TakeProfitPct float64 `db:"take_profit_pct"`
	StopLossPct   float64 `db:"stop_loss_pct"`
	HoldDays      int     `db:"hold_days"`
	// CoveredCallOTMPct, when above 0, makes the row a covered call: hold the symbol and sell a
	// monthly call that many percent out of the money (10 = 10%). The row is simulated on option
	// history, not on entry signals, and its take-profit, stop-loss and hold are not used.
	CoveredCallOTMPct float64 `db:"covered_call_otm_pct"`
}

// HoldStrategies returns every hold_strategy row, ordered by id.
func HoldStrategies(db *sqlx.DB) ([]HoldStrategy, error) { return holdStrategiesWhere(db, "") }

// HoldStrategyByID returns the row whose id is exactly id.
func HoldStrategyByID(db *sqlx.DB, id string) (HoldStrategy, bool, error) {
	rows, err := holdStrategiesWhere(db, " WHERE id = ?", id)
	if err != nil || len(rows) == 0 {
		return HoldStrategy{}, false, err
	}
	return rows[0], true, nil
}

func holdStrategiesWhere(db *sqlx.DB, where string, args ...any) ([]HoldStrategy, error) {
	var out []HoldStrategy
	err := db.Select(&out, `
		SELECT id, name, symbol, total_return, allocation_pct, cash_yield, slippage_pct,
		       trailing_stop_pct, sma_reentry_period, regime_symbol,
		       take_profit_pct, stop_loss_pct, hold_days, covered_call_otm_pct
		FROM hold_strategy`+where+` ORDER BY id`, args...)
	return out, err
}

// PriceActionReclaimStrategy is one row of price_action_reclaim_strategy.
type PriceActionReclaimStrategy struct {
	ID             string  `db:"id"`
	Name           string  `db:"name"`
	Symbol         string  `db:"symbol"`
	SMAPeriod      int     `db:"sma_period"`
	SupportBars    int     `db:"support_bars"`
	ReclaimGap     int     `db:"reclaim_gap"`
	StopCushionPct float64 `db:"stop_cushion_pct"`
	MaxStopPct     float64 `db:"max_stop_pct"`
	TakeProfitPct  float64 `db:"take_profit_pct"`
	HoldDays       int     `db:"hold_days"`
	AllocationPct  float64 `db:"allocation_pct"`
	CashYield      float64 `db:"cash_yield"`
	SlippagePct    float64 `db:"slippage_pct"`
}

// PriceActionReclaimStrategies returns every price_action_reclaim_strategy row, ordered by id.
func PriceActionReclaimStrategies(db *sqlx.DB) ([]PriceActionReclaimStrategy, error) {
	return priceActionReclaimWhere(db, "")
}

// PriceActionReclaimStrategyByID returns the row whose id is exactly id.
func PriceActionReclaimStrategyByID(db *sqlx.DB, id string) (PriceActionReclaimStrategy, bool, error) {
	rows, err := priceActionReclaimWhere(db, " WHERE id = ?", id)
	if err != nil || len(rows) == 0 {
		return PriceActionReclaimStrategy{}, false, err
	}
	return rows[0], true, nil
}

func priceActionReclaimWhere(db *sqlx.DB, where string, args ...any) ([]PriceActionReclaimStrategy, error) {
	var out []PriceActionReclaimStrategy
	err := db.Select(&out, `
		SELECT id, name, symbol, sma_period, support_bars, reclaim_gap, stop_cushion_pct, max_stop_pct,
		       take_profit_pct, hold_days, allocation_pct, cash_yield, slippage_pct
		FROM price_action_reclaim_strategy`+where+` ORDER BY id`, args...)
	return out, err
}

// RotationStrategy is one row of rotation_strategy.
type RotationStrategy struct {
	ID            string  `db:"id"`
	Name          string  `db:"name"`
	Symbols       string  `db:"symbols"`
	UniverseSize  int     `db:"universe_size"`
	TopK          int     `db:"top_k"`
	ExitBuffer    int     `db:"exit_buffer"`
	MaxWeightPct  float64 `db:"max_weight_pct"`
	RegimeSymbol  string  `db:"regime_symbol"`
	RegimeSMA     int     `db:"regime_sma"`
	AllocationPct float64 `db:"allocation_pct"`
	CashYield     float64 `db:"cash_yield"`
	SlippagePct   float64 `db:"slippage_pct"`
	Period        string  `db:"period"`
	Side          string  `db:"side"`
	Pick          string  `db:"pick"`
	EntryLimitPct float64 `db:"entry_limit_pct"`
	TakeProfitPct float64 `db:"take_profit_pct"`
	HoldDays      int     `db:"hold_days"`
	StopLossPct   float64 `db:"stop_loss_pct"`
	Cooldown      string  `db:"cooldown"`
}

// Selector reads rows: *sqlx.DB and *sqlx.Tx both are one.
type Selector interface {
	Select(dest any, query string, args ...any) error
}

// RotationStrategies returns every rotation_strategy row, ordered by id.
func RotationStrategies(db *sqlx.DB) ([]RotationStrategy, error) {
	return rotationStrategiesWhere(db, "")
}

// RotationStrategyByID returns the row whose id is exactly id.
func RotationStrategyByID(db Selector, id string) (RotationStrategy, bool, error) {
	rows, err := rotationStrategiesWhere(db, " WHERE id = ?", id)
	if err != nil || len(rows) == 0 {
		return RotationStrategy{}, false, err
	}
	return rows[0], true, nil
}

func rotationStrategiesWhere(db Selector, where string, args ...any) ([]RotationStrategy, error) {
	var out []RotationStrategy
	err := db.Select(&out, `
		SELECT id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct,
		       regime_symbol, regime_sma, allocation_pct, cash_yield, slippage_pct,
		       period, side, pick, entry_limit_pct, take_profit_pct, hold_days, stop_loss_pct, cooldown
		FROM rotation_strategy`+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	resolveSymbolLists(db, out)
	return out, nil
}

// resolveSymbolLists replaces a row's symbols with the list of the symbol_lists
// row whose symbol_list_id it equals (ignoring case and surrounding spaces), so
// `symbols = 'etf-pre-2021'` means that list's tickers. Any other value is a
// literal ticker list and is left alone. A database without symbol_lists, or
// with none, changes nothing.
func resolveSymbolLists(db Selector, rows []RotationStrategy) {
	var lists []struct {
		ID   string `db:"symbol_list_id"`
		List string `db:"list"`
	}
	if err := db.Select(&lists, `SELECT symbol_list_id, list FROM symbol_lists`); err != nil || len(lists) == 0 {
		return
	}
	byID := make(map[string]string, len(lists))
	for _, l := range lists {
		byID[strings.ToLower(strings.TrimSpace(l.ID))] = l.List
	}
	for i := range rows {
		if list, ok := byID[strings.ToLower(strings.TrimSpace(rows[i].Symbols))]; ok {
			rows[i].Symbols = list
		}
	}
}

// strategyTables are the tables CanonicalID and IDs accept.
var strategyTables = map[string]bool{
	"streak_strategy": true, "hold_strategy": true,
	"tree_strategy": true, "markov_strategy": true,
	"hmm_strategy":                  true,
	"rotation_strategy":             true,
	"price_action_reclaim_strategy": true,
}

// ExactID returns the stored id equal to id in table. It is an indexed lookup.
func ExactID(db *sqlx.DB, table, id string) (string, bool) {
	if !strategyTables[table] {
		return "", false
	}
	var got string
	if err := db.Get(&got, "SELECT id FROM "+table+" WHERE id = ?", id); err != nil {
		return "", false
	}
	return got, true
}

// CanonicalID returns the stored id that matches id in table: exact first,
// then ignoring case, "-", "_" and spaces (the same looseness strategy.Get
// has always had). The loose match scans the table, so exact ids are cheap.
func CanonicalID(db *sqlx.DB, table, id string) (string, bool) {
	if got, ok := ExactID(db, table, id); ok {
		return got, true
	}
	if !strategyTables[table] {
		return "", false
	}
	norm := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(id))
	if norm == "" {
		return "", false
	}
	var got string
	err := db.Get(&got, "SELECT id FROM "+table+
		" WHERE replace(replace(replace(lower(id),'-',''),'_',''),' ','') = ? ORDER BY id LIMIT 1", norm)
	return got, err == nil
}

// IDs returns every id in a strategy table, ordered. A missing table returns an error.
func IDs(db *sqlx.DB, table string) ([]string, error) {
	if !strategyTables[table] {
		return nil, fmt.Errorf("refdb: %q is not a strategy table", table)
	}
	var out []string
	err := db.Select(&out, "SELECT id FROM "+table+" ORDER BY id")
	return out, err
}

// FamilyParam is one row of strategy_family_param: what a grid search may do
// with one column of a family's rows.
type FamilyParam struct {
	Family         string  `db:"family"`
	Param          string  `db:"param"`
	Kind           string  `db:"kind"`
	Role           string  `db:"role"`
	Gridsearchable bool    `db:"gridsearchable"`
	GridValues     *string `db:"grid_values"`
	WhyNot         *string `db:"why_not"`
}

// FamilyParams returns every strategy_family_param row of one family, by param.
func FamilyParams(db *sqlx.DB, family string) ([]FamilyParam, error) {
	var out []FamilyParam
	err := db.Select(&out, `SELECT family, param, kind, role, gridsearchable, grid_values, why_not
		FROM strategy_family_param WHERE family = ? ORDER BY param`, family)
	return out, err
}

// SymbolList returns the tickers of the symbol_lists row symbolListID, in the
// order stored, and whether the row exists.
func SymbolList(db *sqlx.DB, symbolListID string) ([]string, bool, error) {
	var list string
	if err := db.Get(&list, `SELECT list FROM symbol_lists WHERE symbol_list_id = ?`, symbolListID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var out []string
	for sym := range strings.SplitSeq(list, ",") {
		if sym = strings.ToUpper(strings.TrimSpace(sym)); sym != "" {
			out = append(out, sym)
		}
	}
	return out, true, nil
}

// Stack is a named strategy stack. Name is the friendly label, ID the members
// joined with "+".
type Stack struct {
	Name string `db:"name"`
	ID   string `db:"id"`
	Note string `db:"note"`
}

// StackByName finds a stack by its name or its id, ignoring case, "-", "_" and
// spaces in the name.
func StackByName(db *sqlx.DB, key string) (Stack, bool, error) {
	var st Stack
	norm := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(key))
	err := db.Get(&st, "SELECT name, id, note FROM stack WHERE id = ? OR "+
		"replace(replace(replace(lower(name),'-',''),'_',''),' ','') = ? ORDER BY name LIMIT 1", key, norm)
	if errors.Is(err, sql.ErrNoRows) {
		return Stack{}, false, nil
	}
	return st, err == nil, err
}

// Stacks returns every named stack, ordered by name.
func Stacks(db *sqlx.DB) ([]Stack, error) {
	var out []Stack
	err := db.Select(&out, "SELECT name, id, note FROM stack ORDER BY name")
	return out, err
}
