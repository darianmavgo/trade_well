package strateval

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// EvalRow is one persisted IS/OOS evaluation.
type EvalRow struct {
	RunID      string
	StrategyID string
	ParamsJSON string
	Optimized  bool
	Split      Split
	IS         Metrics
	OOS        Metrics
	Tier       string
	Reasons    []string
	CreatedAt  time.Time
}

// Store is a SQLite-backed strategy_evals database (additive; never touches
// trade_orchestrator allowlists).
type Store struct {
	db *sql.DB
}

// OpenStore creates/opens path (parent dirs created).
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS strategy_evals (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  strategy_id TEXT NOT NULL,
  params_json TEXT,
  optimized INTEGER NOT NULL DEFAULT 0,
  is_start TEXT, is_end TEXT, oos_start TEXT, oos_end TEXT,
  is_cagr REAL, is_sharpe REAL, is_max_dd REAL, is_trades INTEGER, is_win_rate REAL, is_avg_trade_pct REAL, is_total_return_pct REAL,
  oos_cagr REAL, oos_sharpe REAL, oos_max_dd REAL, oos_trades INTEGER, oos_win_rate REAL, oos_avg_trade_pct REAL, oos_total_return_pct REAL,
  tier TEXT, reasons_json TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_strategy_evals_strat ON strategy_evals(strategy_id, created_at);
CREATE INDEX IF NOT EXISTS idx_strategy_evals_run ON strategy_evals(run_id);
`)
	if err != nil {
		return err
	}
	// Evaluations made before idle days were recorded get the columns, empty (0 of 0 sessions).
	for _, col := range []string{"is_idle_days", "is_days", "oos_idle_days", "oos_days"} {
		var have int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('strategy_evals') WHERE name = ?`, col).Scan(&have); err != nil {
			return err
		}
		if have == 0 {
			if _, err := s.db.Exec(`ALTER TABLE strategy_evals ADD COLUMN ` + col + ` INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
		}
	}
	return s.ensureLedger()
}

// Insert writes one evaluation row.
func (s *Store) Insert(row EvalRow) error {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	reasons, _ := json.Marshal(row.Reasons)
	_, err := s.db.Exec(`
INSERT INTO strategy_evals (
  run_id, strategy_id, params_json, optimized,
  is_start, is_end, oos_start, oos_end,
  is_cagr, is_sharpe, is_max_dd, is_trades, is_win_rate, is_avg_trade_pct, is_total_return_pct,
  oos_cagr, oos_sharpe, oos_max_dd, oos_trades, oos_win_rate, oos_avg_trade_pct, oos_total_return_pct,
  tier, reasons_json, created_at,
  is_idle_days, is_days, oos_idle_days, oos_days
) VALUES (?,?,?,?, ?,?,?,?, ?,?,?,?,?,?,?, ?,?,?,?,?,?,?, ?,?,?, ?,?,?,?)`,
		row.RunID, row.StrategyID, row.ParamsJSON, boolInt(row.Optimized),
		row.Split.ISStart, row.Split.ISEnd, row.Split.OOSStart, row.Split.OOSEnd,
		row.IS.CAGR, row.IS.Sharpe, row.IS.MaxDD, row.IS.Trades, row.IS.WinRate, row.IS.AvgTradePct, row.IS.TotalReturnPct,
		row.OOS.CAGR, row.OOS.Sharpe, row.OOS.MaxDD, row.OOS.Trades, row.OOS.WinRate, row.OOS.AvgTradePct, row.OOS.TotalReturnPct,
		row.Tier, string(reasons), row.CreatedAt.Format(time.RFC3339),
		row.IS.IdleDays, row.IS.Days, row.OOS.IdleDays, row.OOS.Days,
	)
	if err != nil {
		return err
	}
	return s.TouchStrategy(row.StrategyID, row.Tier)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// LatestByStrategy returns the newest row per strategy_id (optionally filtered by runID).
func (s *Store) LatestByStrategy(runID string) ([]EvalRow, error) {
	q := `
SELECT run_id, strategy_id, params_json, optimized,
  is_start, is_end, oos_start, oos_end,
  is_cagr, is_sharpe, is_max_dd, is_trades, is_win_rate, is_avg_trade_pct, is_total_return_pct,
  oos_cagr, oos_sharpe, oos_max_dd, oos_trades, oos_win_rate, oos_avg_trade_pct, oos_total_return_pct,
  tier, reasons_json, created_at,
  is_idle_days, is_days, oos_idle_days, oos_days
FROM strategy_evals e
WHERE id IN (
  SELECT MAX(id) FROM strategy_evals`
	if runID != "" {
		q += ` WHERE run_id = ?`
	}
	q += ` GROUP BY strategy_id) ORDER BY tier ASC, oos_sharpe DESC`
	var rows *sql.Rows
	var err error
	if runID != "" {
		rows, err = s.db.Query(q, runID)
	} else {
		rows, err = s.db.Query(q)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EvalRow
	for rows.Next() {
		var r EvalRow
		var opt int
		var reasons string
		var created string
		if err := rows.Scan(
			&r.RunID, &r.StrategyID, &r.ParamsJSON, &opt,
			&r.Split.ISStart, &r.Split.ISEnd, &r.Split.OOSStart, &r.Split.OOSEnd,
			&r.IS.CAGR, &r.IS.Sharpe, &r.IS.MaxDD, &r.IS.Trades, &r.IS.WinRate, &r.IS.AvgTradePct, &r.IS.TotalReturnPct,
			&r.OOS.CAGR, &r.OOS.Sharpe, &r.OOS.MaxDD, &r.OOS.Trades, &r.OOS.WinRate, &r.OOS.AvgTradePct, &r.OOS.TotalReturnPct,
			&r.Tier, &reasons, &created,
			&r.IS.IdleDays, &r.IS.Days, &r.OOS.IdleDays, &r.OOS.Days,
		); err != nil {
			return nil, err
		}
		r.Optimized = opt == 1
		_ = json.Unmarshal([]byte(reasons), &r.Reasons)
		r.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, r)
	}
	return out, rows.Err()
}

// AllowlistDiff compares tier-A strategies to a current allowlist.
// Returns proposed adds (A not in allowlist) and demotes (in allowlist but tier D/C).
func AllowlistDiff(rows []EvalRow, allowlist []string) (add, demote []string) {
	in := map[string]bool{}
	for _, a := range allowlist {
		in[a] = true
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.StrategyID] {
			continue
		}
		seen[r.StrategyID] = true
		switch r.Tier {
		case "A":
			if !in[r.StrategyID] {
				add = append(add, r.StrategyID)
			}
		case "C", "D":
			if in[r.StrategyID] {
				demote = append(demote, r.StrategyID)
			}
		}
	}
	return add, demote
}

// FormatReport builds a markdown scorecard.
func FormatReport(rows []EvalRow, allowlist []string) string {
	add, demote := AllowlistDiff(rows, allowlist)
	var b []byte
	w := func(format string, args ...any) {
		b = append(b, fmt.Sprintf(format, args...)...)
	}
	w("# Strategy eval scorecard\n\n")
	w("| Tier | Strategy | OOS Sharpe | OOS CAGR | OOS DD | OOS Trades | OOS Idle Days | Notes |\n")
	w("|------|----------|------------|----------|--------|------------|---------------|-------|\n")
	for _, r := range rows {
		note := ""
		if len(r.Reasons) > 0 {
			note = r.Reasons[0]
		}
		idle := "-"
		if r.OOS.Days > 0 {
			idle = fmt.Sprintf("%d/%d (%.0f%%)", r.OOS.IdleDays, r.OOS.Days, 100*float64(r.OOS.IdleDays)/float64(r.OOS.Days))
		}
		w("| %s | `%s` | %.2f | %.1f%% | %.1f%% | %d | %s | %s |\n",
			r.Tier, r.StrategyID, r.OOS.Sharpe, r.OOS.CAGR*100, r.OOS.MaxDD*100, r.OOS.Trades, idle, note)
	}
	w("\n## Allowlist proposal (manual apply — not written)\n\n")
	if len(add) == 0 && len(demote) == 0 {
		w("_No changes suggested._\n")
	} else {
		if len(add) > 0 {
			w("**Add to STRATEGY_ALLOWLIST:** `%v`\n\n", add)
		}
		if len(demote) > 0 {
			w("**Consider removing:** `%v`\n\n", demote)
		}
	}
	return string(b)
}
