// Package strategies parses and validates the set of trading strategy IDs
// that are greenlit for active use (scanning and live order submission), and
// resolves greenlit IDs to backtestgosqlite Strategy implementations.
//
// backtestgosqlite's strategy registry holds every strategy ever built,
// including experimental and research-only ones; trade_orchestrator must
// never treat "registered somewhere" as "safe to trade". Greenlighting is a
// separate, explicit allowlist controlled via STRATEGY_ALLOWLIST.
//
// An allowlist entry may be a single registry ID or a stack ID joined with
// '+' (primary first, then overlays). Stacking lives here and in the scan
// job — nothing new is added in backtestgosqlite's pkg/strategy. Overlay
// members of a stack are not independently tradable unless listed alone.
package strategies

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/csvlist"
)

// EnvAllowlist is the environment variable holding the comma-separated list
// of strategy IDs (or stack IDs) greenlit for active scanning/trading.
const EnvAllowlist = "STRATEGY_ALLOWLIST"

// Parse splits a comma-separated list of strategy IDs, trims whitespace,
// drops empty entries, and de-duplicates while preserving order. It fails
// explicitly rather than silently defaulting when the result is empty.
func Parse(raw string) ([]string, error) {
	out := csvlist.Unique(raw)

	if len(out) == 0 {
		return nil, fmt.Errorf("no strategies greenlit: set %s in the config object (deploy_gae/config.sh) to a comma-separated list of strategy IDs", EnvAllowlist)
	}
	return out, nil
}

// ResolveRequest picks which greenlit strategies a job should run.
// Empty flags → every allowlist entry. Explicit IDs must already be greenlit.
func ResolveRequest(strategiesFlag, strategyFlag string) ([]string, error) {
	allowlist, err := Parse(os.Getenv(EnvAllowlist))
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(strategiesFlag)
	if raw == "" {
		raw = strings.TrimSpace(strategyFlag)
	}
	if raw == "" {
		return allowlist, nil
	}
	requested, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	var notGreenlit []string
	for _, id := range requested {
		if !slices.Contains(allowlist, id) {
			notGreenlit = append(notGreenlit, id)
		}
	}
	if len(notGreenlit) > 0 {
		return nil, fmt.Errorf("strategy(ies) not greenlit for trading: %s (greenlit: %s; add to %s in the config object to enable)",
			strings.Join(notGreenlit, ", "), strings.Join(allowlist, ", "), EnvAllowlist)
	}
	return requested, nil
}

// EnsureSchema creates the strategies log table if it does not already exist.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS strategies (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	strategy_id TEXT NOT NULL,
	config_json TEXT NOT NULL,
	resolved_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_strategies_strategy_id ON strategies(strategy_id, resolved_at DESC);
`)
	return err
}

// ResolveGreenlit resolves every entry of allowlist (expanding stacks into
// their members) to its backtestgosqlite Strategy via the global registry,
// and logs one row per resolved strategy to the strategies table when db is
// non-nil. It returns an error if any entry (or stack member) is not
// registered in backtestgosqlite.
func ResolveGreenlit(db *sql.DB, allowlist []string) ([]strategy.Strategy, error) {
	if err := ensureRegistered(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(allowlist))
	seen := map[string]bool{}
	for _, entry := range allowlist {
		members := []string{entry}
		if strategy.IsStack(entry) {
			members = strategy.ParseStack(entry)
		}
		for _, id := range members {
			if seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}

	out := make([]strategy.Strategy, 0, len(ids))
	for _, id := range ids {
		s, ok := strategy.Get(id)
		if !ok {
			return nil, fmt.Errorf("strategy %q is greenlit but not registered in backtestgosqlite (a strategy that is a database row also needs its row in pkg/strategies/deploy/strategies.db: regenerate it with `strategy export`)", id)
		}
		out = append(out, s)
		if db != nil {
			if err := logResolved(db, s); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func logResolved(db *sql.DB, s strategy.Strategy) error {
	if err := EnsureSchema(db); err != nil {
		return err
	}
	cfgJSON, err := json.Marshal(s.DefaultConfig())
	if err != nil {
		return err
	}
	_, err = db.Exec(
		`INSERT INTO strategies (strategy_id, config_json, resolved_at) VALUES (?, ?, ?)`,
		s.ID(), string(cfgJSON), time.Now().UTC().Format(time.RFC3339),
	)
	return err
}
