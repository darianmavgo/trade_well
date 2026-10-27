package prune_losers

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/lastbacktest"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
)

// MinWorkers is the fewest result DBs `orphans` works on at once.
const MinWorkers = 10

// OrphansConfig holds the settings of `prune_losers orphans`.
type OrphansConfig struct {
	StrategiesDB string // -db: where the row-backed strategies are read
	ReportsDir   string // -reports
	Root         string // -root: folder holding sql/strategies
	Workers      int    // -workers
	DryRun       bool   // -dry-run
	Vacuum       bool   // -vacuum: rewrite each changed DB to give the space back
	RemoveEmpty  bool   // -remove-empty: delete a result DB left with no backtest
	// Exists reports whether a strategy id (a stack counts when every member
	// does) is still known. Nil means the strategy registry.
	Exists func(id string) bool
	Out    io.Writer
}

// OrphansResult is what was (or would be) removed.
type OrphansResult struct {
	DBs        int      // result DBs read
	Changed    int      // result DBs with orphan rows
	Removed    int      // result DBs deleted for being empty
	Orphans    []string // distinct orphan ids
	RowsPerTbl map[string]int
}

func runOrphans(args []string, stdout, stderr io.Writer) int {
	cfg := OrphansConfig{Out: stdout}
	fs := flag.NewFlagSet("prune_losers orphans", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.StrategiesDB, "db", appenv.RefDB(), "strategies database that defines which strategies exist")
	fs.StringVar(&cfg.ReportsDir, "reports", appenv.Reports(), "reports folder holding the result DBs")
	fs.StringVar(&cfg.Root, "root", appenv.Folder(), "folder holding sql/strategies")
	fs.IntVar(&cfg.Workers, "workers", MaxWorkers, "concurrent result DBs (min 10, max 32)")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "report what would be deleted without deleting")
	fs.BoolVar(&cfg.Vacuum, "vacuum", false, "VACUUM each changed result DB to return the space (slow on large files)")
	fs.BoolVar(&cfg.RemoveEmpty, "remove-empty", true, "delete a result DB that has no backtest left (-remove-empty=false keeps them)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if _, err := PruneOrphans(cfg); err != nil {
		fmt.Fprintln(stderr, "prune_losers orphans:", err)
		return 1
	}
	return 0
}

// registryExists builds the real existence check from the registry.
func registryExists(root, strategiesDB string) func(string) bool {
	if refdb.DefaultPath == strategiesDB {
		stratreg.RegisterAll(root, strategiesDB)
	} else {
		strategy.AutoRegisterSQLStrategies(root, strategiesDB)
		stratreg.RegisterFamiliesAt(strategiesDB)
	}
	return func(id string) bool {
		for _, m := range strategy.ParseStack(id) {
			if _, ok := strategy.Get(m); !ok {
				return false
			}
		}
		return len(strategy.ParseStack(id)) > 0
	}
}

// PruneOrphans deletes, from every result DB, the rows of strategies that no
// longer exist. Result DBs without performance_summary are left alone.
func PruneOrphans(cfg OrphansConfig) (OrphansResult, error) {
	res := OrphansResult{RowsPerTbl: map[string]int{}}
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	if cfg.Exists == nil {
		cfg.Exists = registryExists(cfg.Root, cfg.StrategiesDB)
	}
	if cfg.Workers < MinWorkers {
		cfg.Workers = MinWorkers
	}
	if cfg.Workers > MaxWorkers {
		cfg.Workers = MaxWorkers
	}
	paths, err := lastbacktest.ResultDBs(cfg.ReportsDir)
	if err != nil {
		return res, err
	}
	res.DBs = len(paths)

	var (
		mu       sync.Mutex
		known    = map[string]bool{} // id -> exists
		orphans  = map[string]bool{}
		firstErr error
		wg       sync.WaitGroup
	)
	exists := func(id string) bool {
		mu.Lock()
		defer mu.Unlock()
		ok, seen := known[id]
		if !seen {
			ok = cfg.Exists(id)
			known[id] = ok
			if !ok {
				orphans[id] = true
			}
		}
		return ok
	}
	jobs := make(chan string)
	for i := 0; i < cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				r, err := pruneOrphansIn(p, cfg, exists)
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = fmt.Errorf("%s: %w", p, err)
				}
				if r.rows > 0 {
					res.Changed++
					fmt.Fprintf(cfg.Out, "  %s: %d rows%s\n", p, r.rows, map[bool]string{true: ", now empty", false: ""}[r.empty])
				}
				if r.removed {
					res.Removed++
				}
				for t, n := range r.perTable {
					res.RowsPerTbl[t] += n
				}
				mu.Unlock()
			}
		}()
	}
	for _, p := range paths {
		jobs <- p
	}
	close(jobs)
	wg.Wait()
	for id := range orphans {
		res.Orphans = append(res.Orphans, id)
	}
	sort.Strings(res.Orphans)

	verb := "deleted"
	if cfg.DryRun {
		verb = "would delete"
	}
	total := 0
	for _, n := range res.RowsPerTbl {
		total += n
	}
	fmt.Fprintf(cfg.Out, "read %d result DBs; %d strategies no longer exist; %d DBs hold their rows\n", res.DBs, len(res.Orphans), res.Changed)
	for _, id := range res.Orphans[:min(len(res.Orphans), 10)] {
		fmt.Fprintf(cfg.Out, "  e.g. %s\n", id)
	}
	tables := make([]string, 0, len(res.RowsPerTbl))
	for t := range res.RowsPerTbl {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, t := range tables {
		fmt.Fprintf(cfg.Out, "  %-28s %d\n", t, res.RowsPerTbl[t])
	}
	fmt.Fprintf(cfg.Out, "%s %d rows", verb, total)
	if cfg.RemoveEmpty {
		fmt.Fprintf(cfg.Out, " and %d empty result DBs", res.Removed)
	}
	fmt.Fprintln(cfg.Out)
	return res, firstErr
}

type orphanDBResult struct {
	rows     int
	empty    bool
	removed  bool
	perTable map[string]int
}

// keyColumn is the column naming the strategy in a table: combined_id wins
// (shared_account_priorities lists live members of a dead stack and the reverse),
// then strategy_id. "" means the table is not keyed by strategy.
func keyColumn(cols map[string]bool) string {
	switch {
	case cols["combined_id"]:
		return "combined_id"
	case cols["strategy_id"]:
		return "strategy_id"
	}
	return ""
}

func pruneOrphansIn(path string, cfg OrphansConfig, exists func(string) bool) (orphanDBResult, error) {
	out := orphanDBResult{perTable: map[string]int{}}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return out, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // the temp table lives on one connection

	var tables []string
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return out, err
		}
		tables = append(tables, n)
	}
	rows.Close()
	keys := map[string]string{} // table -> key column
	for _, t := range tables {
		cols := map[string]bool{}
		cr, err := db.Query(`SELECT name FROM pragma_table_info(?)`, t)
		if err != nil {
			return out, err
		}
		for cr.Next() {
			var c string
			cr.Scan(&c)
			cols[c] = true
		}
		cr.Close()
		if k := keyColumn(cols); k != "" {
			keys[t] = k
		}
	}
	if _, ok := keys["performance_summary"]; !ok {
		return out, nil // not a result DB
	}

	// Ids come from the small tables; the big ones (trades, equity_curve) are only deleted from.
	ids := map[string]bool{}
	for _, t := range []string{"performance_summary", "runs", "shared_account_audit", "shared_account_priorities"} {
		k, ok := keys[t]
		if !ok {
			continue
		}
		ir, err := db.Query(`SELECT DISTINCT ` + k + ` FROM ` + t + ` WHERE ` + k + ` IS NOT NULL`)
		if err != nil {
			return out, err
		}
		for ir.Next() {
			var id string
			ir.Scan(&id)
			ids[id] = true
		}
		ir.Close()
	}
	var dead []string
	for id := range ids {
		if !exists(id) {
			dead = append(dead, id)
		}
	}
	if len(dead) == 0 {
		return out, vacuumIfWasteful(db, cfg) // a file pruned by an earlier run still holds its free pages
	}
	sort.Strings(dead)

	tx, err := db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TEMP TABLE orphan(id TEXT PRIMARY KEY)`); err != nil {
		return out, err
	}
	for _, id := range dead {
		if _, err := tx.Exec(`INSERT INTO orphan(id) VALUES (?)`, id); err != nil {
			return out, err
		}
	}
	sort.Strings(tables)
	for _, t := range tables {
		k, ok := keys[t]
		if !ok {
			continue
		}
		var n int
		if cfg.DryRun {
			err = tx.QueryRow(`SELECT count(*) FROM "` + t + `" WHERE ` + k + ` IN (SELECT id FROM orphan)`).Scan(&n)
		} else {
			var r sql.Result
			if r, err = tx.Exec(`DELETE FROM "` + t + `" WHERE ` + k + ` IN (SELECT id FROM orphan)`); err == nil {
				var c int64
				c, err = r.RowsAffected()
				n = int(c)
			}
		}
		if err != nil {
			return out, fmt.Errorf("%s: %w", t, err)
		}
		if n > 0 {
			out.perTable[t] = n
			out.rows += n
		}
	}
	var left int
	if err := tx.QueryRow(`SELECT count(*) FROM performance_summary` + map[bool]string{true: ` WHERE ` + keys["performance_summary"] + ` NOT IN (SELECT id FROM orphan)`, false: ``}[cfg.DryRun]).Scan(&left); err != nil {
		return out, err
	}
	out.empty = left == 0
	if cfg.DryRun {
		return out, nil
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	if out.empty && cfg.RemoveEmpty {
		db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
				return out, err
			}
		}
		out.removed = true
		return out, nil
	}
	return out, vacuumIfWasteful(db, cfg)
}

// vacuumSlots bounds the VACUUMs running at once: each writes a full copy of
// its file next to it, so 32 of them on multi-GB files would fill the disk.
var vacuumSlots = make(chan struct{}, 3)

// vacuumIfWasteful rewrites db when -vacuum is set and over a quarter of its
// pages are free. DELETE never shrinks a SQLite file; only VACUUM does.
func vacuumIfWasteful(db *sql.DB, cfg OrphansConfig) error {
	if !cfg.Vacuum || cfg.DryRun {
		return nil
	}
	var free, total int64
	if err := db.QueryRow(`SELECT freelist_count, page_count FROM pragma_freelist_count, pragma_page_count`).Scan(&free, &total); err != nil {
		return err
	}
	if total == 0 || free*4 < total {
		return nil
	}
	vacuumSlots <- struct{}{}
	defer func() { <-vacuumSlots }()
	if _, err := db.Exec(`VACUUM`); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	_, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}
