package runview

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// definitionTables are the refdata tables whose rows define a strategy by id.
var definitionTables = []string{"streak_strategy", "tree_strategy", "markov_strategy", "hmm_strategy", "hold_strategy", "rotation_strategy"}

// heavyTables hold one row per bar or trade, so the strategy view links to
// them filtered instead of loading them.
var heavyTables = map[string]bool{
	"trades": true, "equity_curve": true, "signals": true, "return_breakdown": true,
	"shared_account_audit": true, "shared_account_priorities": true, "run_metrics": true, "runs": true,
}

const strategyQueryTimeout = 5 * time.Second

// strategyRows is one table's rows for one strategy.
type strategyRows struct {
	File    string    `json:"file"`
	Table   string    `json:"table"`
	Columns []colInfo `json:"columns"`
	Rows    [][]any   `json:"rows"`
	Total   int       `json:"total"`
	Heavy   bool      `json:"heavy,omitempty"`
}

var strategyIDRe = regexp.MustCompile(`^[A-Za-z0-9._+\-]+$`)

// strategy gathers everything the run and the reference database hold under
// one strategy id: its definition rows, then every table with a strategy_id
// column in each result database of the run.
func (s *server) strategy(q url.Values) (any, error) {
	id := strings.TrimSpace(q.Get("id"))
	if !strategyIDRe.MatchString(id) {
		return nil, fmt.Errorf("bad strategy id %q", id)
	}
	dir, err := s.resolve(q.Get("run"), "")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), strategyQueryTimeout*4)
	defer cancel()

	var definition []strategyRows
	if s.refDB != "" {
		if db, err := openRO(s.refDB); err == nil {
			for _, t := range definitionTables {
				if r, ok := rowsFor(ctx, db, "refdata", t, "id", id, 5); ok {
					definition = append(definition, r)
				}
			}
			db.Close()
		}
	}

	var results []strategyRows
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".db") || strings.Contains(p, "scratch") {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == "pipeline.db" {
			return nil
		}
		db, err := openRO(p)
		if err != nil {
			return nil
		}
		defer db.Close()
		var tables []string
		if db.Select(&tables, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`) != nil {
			return nil
		}
		for _, t := range tables {
			cols, err := tableColumns(db, t)
			if err != nil || !hasColumn(cols, "strategy_id") {
				continue
			}
			if heavyTables[t] {
				if hasRows(ctx, db, t, id) {
					results = append(results, strategyRows{File: rel, Table: t, Heavy: true, Columns: cols})
				}
				continue
			}
			if r, ok := rowsFor(ctx, db, rel, t, "strategy_id", id, 300); ok {
				results = append(results, r)
			}
		}
		return nil
	})
	sort.SliceStable(results, func(i, j int) bool { return resultRank(results[i]) < resultRank(results[j]) })
	return map[string]any{"id": id, "definition": definition, "results": results}, nil
}

func hasColumn(cols []colInfo, name string) bool {
	for _, c := range cols {
		if c.Name == name {
			return true
		}
	}
	return false
}

// resultRank puts the tables that explain a result first.
func resultRank(r strategyRows) int {
	if r.Heavy {
		return 9
	}
	switch r.Table {
	case "gridsearch_results":
		return 0
	case "strategy_evals":
		return 1
	case "performance_summary":
		return 2
	case "scoreboard":
		return 3
	}
	return 5
}

// hasRows reports whether the table has a row for the strategy. A query that
// runs out of time counts as yes, so a big table is still offered.
func hasRows(ctx context.Context, db *sqlx.DB, table, id string) bool {
	qctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var one int
	err := db.GetContext(qctx, &one, fmt.Sprintf("SELECT 1 FROM %s WHERE strategy_id = ? LIMIT 1", quoteIdent(table)), id)
	return err == nil || qctx.Err() != nil
}

// rowsFor selects up to limit rows of table where col = id. A table with no
// match, or a query that times out, is left out.
func rowsFor(ctx context.Context, db *sqlx.DB, file, table, col, id string, limit int) (strategyRows, bool) {
	cols, err := tableColumns(db, table)
	if err != nil || !hasColumn(cols, col) {
		return strategyRows{}, false
	}
	qctx, cancel := context.WithTimeout(ctx, strategyQueryTimeout)
	defer cancel()
	order := ""
	if table == "gridsearch_results" && hasColumn(cols, "is_baseline") && hasColumn(cols, "calmar_ratio") {
		order = " ORDER BY is_baseline DESC, calmar_ratio DESC" // the baseline and the best configs always make the cut
	}
	rows, err := db.QueryxContext(qctx, fmt.Sprintf("SELECT * FROM %s WHERE %s = ?%s LIMIT %d", quoteIdent(table), quoteIdent(col), order, limit), id)
	if err != nil {
		return strategyRows{}, false
	}
	defer rows.Close()
	data, _, err := scanRows(rows, limit)
	if err != nil || len(data) == 0 {
		return strategyRows{}, false
	}
	total := len(data)
	if len(data) >= limit {
		var n int
		if db.GetContext(qctx, &n, fmt.Sprintf("SELECT count(*) FROM %s WHERE %s = ?", quoteIdent(table), quoteIdent(col)), id) == nil {
			total = n
		}
	}
	return strategyRows{File: file, Table: table, Columns: cols, Rows: data, Total: total}, true
}

// find suggests strategy ids from the reference database by prefix, then by
// containing text.
func (s *server) find(q url.Values) (any, error) {
	text := strings.TrimSpace(q.Get("q"))
	out := []string{}
	if text == "" || s.refDB == "" || !strategyIDRe.MatchString(text) {
		return out, nil
	}
	db, err := openRO(s.refDB)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var union []string
	for _, t := range definitionTables {
		union = append(union, fmt.Sprintf("SELECT id FROM %s WHERE id LIKE ?1 || '%%'", t))
	}
	var ids []string
	if err := db.Select(&ids, strings.Join(union, " UNION ")+" ORDER BY 1 LIMIT 25", text); err != nil {
		return nil, err
	}
	return append(out, ids...), nil
}
