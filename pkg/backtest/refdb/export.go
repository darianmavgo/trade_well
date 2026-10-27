package refdb

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

var safeStrategyID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Export writes a small reference database at dstPath holding only the strategy
// rows named in ids, copied from the full database at srcPath. It is how a program
// that cannot carry the whole file (trade_orchestrator on App Engine) ships the
// handful of rows it trades. An id found in none of the five row tables is an error,
// so a typo cannot ship as an empty bundle. It returns the ids found, by table.
func Export(srcPath, dstPath string, ids []string) (map[string][]string, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("export: no strategy ids")
	}
	quoted := make([]string, len(ids))
	for i, id := range ids {
		if !safeStrategyID.MatchString(id) {
			return nil, fmt.Errorf("export: %q is not a strategy id", id)
		}
		quoted[i] = "'" + id + "'"
	}
	// Opening the source gives an older database any table added since (rotation_strategy),
	// so the bundle query below finds every row table.
	if src, err := Open(srcPath); err != nil {
		return nil, fmt.Errorf("export: open %s: %w", srcPath, err)
	} else {
		src.Close()
	}
	if err := os.Remove(dstPath); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	db, err := storage.OpenSQLite(dstPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // the ATTACH must be seen by the statements that follow
	if _, err := db.Exec(`ATTACH DATABASE ? AS src`, srcPath); err != nil {
		return nil, fmt.Errorf("export: attach %s: %w", srcPath, err)
	}
	if err := storage.RunStage(db, "refdb_export", map[string]string{"__ID_LIST__": strings.Join(quoted, ",")}); err != nil {
		return nil, err
	}
	found := map[string][]string{}
	have := map[string]bool{}
	for _, table := range []string{"streak_strategy", "tree_strategy", "hold_strategy", "markov_strategy", "hmm_strategy", "rotation_strategy"} {
		var got []string
		if err := db.Select(&got, `SELECT id FROM `+table+` ORDER BY id`); err != nil {
			return nil, err
		}
		found[table] = got
		for _, id := range got {
			have[id] = true
		}
	}
	var missing []string
	for _, id := range ids {
		if !have[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return found, fmt.Errorf("export: not a row in any strategy table of %s: %s", srcPath, strings.Join(missing, ", "))
	}
	return found, nil
}

// bundleMeta records how a bundle was made, so the program that trades it can check it.
const bundleMeta = `CREATE TABLE IF NOT EXISTS bundle_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`

// rowTables are the six strategy row tables, each with an allocation_pct column.
var rowTables = []string{"streak_strategy", "tree_strategy", "hold_strategy", "markov_strategy", "hmm_strategy", "rotation_strategy"}

// SetAllocation sizes every row of the bundle at dstPath at alloc (a fraction of equity per
// position, 0.20 = 20%) and records it in bundle_meta as alloc_pct. It is how the allocation a
// backtest ran with (backtest stack -alloc) reaches live: the bundle is a derived copy, so the
// full database is left alone.
func SetAllocation(dstPath string, alloc float64) error {
	if alloc <= 0 || alloc > 1 {
		return fmt.Errorf("allocation %v: want a fraction in (0, 1], such as 0.20", alloc)
	}
	db, err := storage.OpenSQLite(dstPath)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, table := range rowTables {
		var n int
		if err := db.Get(&n, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table); err != nil {
			return err
		}
		if n == 0 {
			continue // a bundle holds only the families it exports
		}
		if _, err := db.Exec(`UPDATE `+table+` SET allocation_pct = ?`, alloc); err != nil {
			return fmt.Errorf("set allocation on %s: %w", table, err)
		}
	}
	if _, err := db.Exec(bundleMeta); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO bundle_meta (key, value) VALUES ('alloc_pct', ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, fmt.Sprint(alloc))
	return err
}

// BundleAllocation is the allocation SetAllocation recorded in the bundle at path. ok is false
// for a bundle made without one.
func BundleAllocation(path string) (alloc float64, ok bool, err error) {
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return 0, false, err
	}
	defer db.Close()
	var v string
	if err := db.Get(&v, `SELECT value FROM bundle_meta WHERE key = 'alloc_pct'`); err != nil {
		if strings.Contains(err.Error(), "no such table") || strings.Contains(err.Error(), "no rows") {
			return 0, false, nil
		}
		return 0, false, err
	}
	if _, err := fmt.Sscan(v, &alloc); err != nil {
		return 0, false, fmt.Errorf("bundle_meta alloc_pct %q: %w", v, err)
	}
	return alloc, true, nil
}
