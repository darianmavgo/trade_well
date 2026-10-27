package strategies

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	_ "modernc.org/sqlite"
)

// BundledAssetInfo says what the strategy bundle compiled into this binary holds: a short hash of the
// three databases (so two deploys can be told apart) and the id of every strategy row in it.
type BundledAssetInfo struct {
	Hash string   `json:"hash"`
	IDs  []string `json:"ids"`
	// Alloc is the fraction of equity per position the bundle was exported at (strategy export
	// -alloc), 0 for a bundle made without one.
	Alloc float64 `json:"alloc_pct,omitempty"`
}

// ReadBundledStrategyAssetInfo reads the embedded strategy ASSETS (strategies.db and the model databases, built in backtestgosqlite) without placing it in the app folder.
func ReadBundledStrategyAssetInfo() (BundledAssetInfo, error) {
	h := sha256.New()
	h.Write(bundledStrategies)
	h.Write(bundledMarkovModels)
	h.Write(bundledTreeModels)
	h.Write(bundledHMMModels)
	info := BundledAssetInfo{Hash: hex.EncodeToString(h.Sum(nil))[:10]}

	dir, err := os.MkdirTemp("", "bundle-info-")
	if err != nil {
		return info, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "strategies.db")
	if err := os.WriteFile(path, bundledStrategies, 0o600); err != nil {
		return info, err
	}
	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return info, err
	}
	defer db.Close()
	for _, table := range []string{"streak_strategy", "tree_strategy", "hold_strategy", "markov_strategy", "hmm_strategy", "rotation_strategy"} {
		// A bundle exported before a family existed has no table for it, and so no rows.
		var have int
		if err := db.QueryRow(orchestratorsql.Q("strategies/read_bundled_strategy_asset_info"), table).Scan(&have); err != nil {
			return info, fmt.Errorf("bundle %s: %w", table, err)
		}
		if have == 0 {
			continue
		}
		rows, err := db.Query("SELECT id FROM " + table)
		if err != nil {
			return info, fmt.Errorf("bundle %s: %w", table, err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return info, err
			}
			info.IDs = append(info.IDs, id)
		}
		rows.Close()
	}
	sort.Strings(info.IDs)
	var v string
	switch err := db.QueryRow(orchestratorsql.Q("strategies/read_bundled_strategy_asset_info_2")).Scan(&v); {
	case err == nil:
		if _, err := fmt.Sscan(v, &info.Alloc); err != nil {
			return info, fmt.Errorf("bundle_meta alloc_pct %q: %w", v, err)
		}
	case strings.Contains(err.Error(), "no such table"), err == sql.ErrNoRows:
	default:
		return info, err
	}
	return info, nil
}
