package cleanup

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var createRe = regexp.MustCompile(`(?im)^\s*CREATE\s+(?:TEMP\s+|TEMPORARY\s+)?(?:TABLE|VIEW)\s+(?:IF\s+NOT\s+EXISTS\s+)?[\x60"\[]?([A-Za-z_][A-Za-z0-9_]*)`)

// SchemaTables lists the tables and views the orchestrator's SQL creates: every
// CREATE TABLE or CREATE VIEW name in the .sql files under <root>/sql/orchestrator.
// A table a run made under a run-suffixed name (winrate_live__r2026...) is not in
// the list, so a leftover one shows up as unknown.
func SchemaTables(root string) ([]string, error) {
	seen := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "sql", "orchestrator"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".sql" {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range createRe.FindAllStringSubmatch(string(b), -1) {
			seen[m[1]] = true
		}
		return nil
	})
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, err
}

// ExtraTable is a table or view in a database that no schema creates.
type ExtraTable struct {
	Name string
	Kind string // "table" or "view"
	Rows int64  // -1 for a view or when the count failed
}

// engineOwned are names the database engine or Litestream keep for themselves.
func engineOwned(name string) bool {
	for _, p := range []string{"sqlite_", "_litestream", "libsql_", "_cf_"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func quoteIdent(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// ScanTables lists the tables and views in db whose name is not in known.
// known is what the schemas create (see SchemaTables). Shadow tables
// of a virtual table (fts and the like) show up as extras: judge the list.
func ScanTables(ctx context.Context, db *sql.DB, known []string) ([]ExtraTable, error) {
	keep := map[string]bool{}
	for _, k := range known {
		keep[k] = true
	}
	rows, err := db.QueryContext(ctx, `SELECT name, type FROM sqlite_master WHERE type IN ('table','view') ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	var extra []ExtraTable
	for rows.Next() {
		var t ExtraTable
		if err := rows.Scan(&t.Name, &t.Kind); err != nil {
			rows.Close()
			return nil, err
		}
		if !keep[t.Name] && !engineOwned(t.Name) {
			extra = append(extra, t)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range extra {
		extra[i].Rows = -1
		if extra[i].Kind == "table" {
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdent(extra[i].Name)).Scan(&extra[i].Rows); err != nil {
				extra[i].Rows = -1
			}
		}
	}
	return extra, nil
}

// Tables prints the tables nothing creates and, when cfg.Remove is set and
// cfg.DryRun is not, drops them. It returns how many it found.
func Tables(ctx context.Context, cfg Config, db *sql.DB, known []string) (int, error) {
	extra, err := ScanTables(ctx, db, known)
	if err != nil {
		return 0, err
	}
	for _, t := range extra {
		fmt.Fprintf(cfg.Out, "unknown %-5s %-45s %10d rows\n", t.Kind, t.Name, t.Rows)
	}
	verb := "found"
	if cfg.Remove && !cfg.DryRun {
		for _, t := range extra {
			if _, err := db.ExecContext(ctx, "DROP "+strings.ToUpper(t.Kind)+" "+quoteIdent(t.Name)); err != nil {
				return len(extra), fmt.Errorf("drop %s %s: %w", t.Kind, t.Name, err)
			}
		}
		verb = "dropped"
	} else if cfg.Remove {
		verb = "would drop"
	}
	fmt.Fprintf(cfg.Out, "%s: %d tables or views no schema creates\n", verb, len(extra))
	return len(extra), nil
}

// StaleJob is a job name in job_run that no pkg/orchestrator package has.
type StaleJob struct {
	Job  string
	Rows int64
	Last string
}

// JobNames lists the job names of the tree: one per directory under
// <root>/pkg/orchestrator (the package name is the job name).
func JobNames(root string) ([]string, error) { return subdirs(root, "pkg", "orchestrator") }

// Jobs prints the job_run rows whose job is not in known and, when cfg.Remove
// is set and cfg.DryRun is not, deletes them. It returns how many jobs it found.
func Jobs(ctx context.Context, cfg Config, db *sql.DB, known []string) (int, error) {
	keep := map[string]bool{}
	for _, k := range known {
		keep[k] = true
	}
	rows, err := db.QueryContext(ctx, `SELECT job, COUNT(*), COALESCE(MAX(finished_at), '') FROM job_run GROUP BY job ORDER BY job`)
	if err != nil {
		return 0, fmt.Errorf("list jobs: %w", err)
	}
	var stale []StaleJob
	for rows.Next() {
		var j StaleJob
		if err := rows.Scan(&j.Job, &j.Rows, &j.Last); err != nil {
			rows.Close()
			return 0, err
		}
		if !keep[j.Job] {
			stale = append(stale, j)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	sort.Slice(stale, func(i, j int) bool { return stale[i].Job < stale[j].Job })
	var total int64
	for _, j := range stale {
		fmt.Fprintf(cfg.Out, "stale job %-35s %8d rows, last %s\n", j.Job, j.Rows, j.Last)
		total += j.Rows
	}
	verb := "found"
	if cfg.Remove && !cfg.DryRun {
		for _, j := range stale {
			if _, err := db.ExecContext(ctx, `DELETE FROM job_run WHERE job = ?`, j.Job); err != nil {
				return len(stale), fmt.Errorf("delete job %s: %w", j.Job, err)
			}
		}
		verb = "deleted"
	} else if cfg.Remove {
		verb = "would delete"
	}
	fmt.Fprintf(cfg.Out, "%s: %d stale jobs, %d job_run rows\n", verb, len(stale), total)
	return len(stale), nil
}
