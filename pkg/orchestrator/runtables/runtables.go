// Package runtables gives a calculation its own persisted tables in Turso.
//
// A calculation here is a series of SQL stages, each writing a slice table that the next one
// reads. They used to run in a throwaway in-memory SQLite database. They now run in Turso itself,
// in real tables, so a run can be audited afterwards and nothing is local. To keep one run from
// reading another's rows (a second viewer, a later run), every slice table of a run is named
// <table>__<run id>: the stage SQL keeps its plain names and Run rewrites them. Old runs are
// dropped by Prune, keeping the newest few.
package runtables

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

var seq atomic.Int64

// Run is one calculation's set of tables.
type Run struct {
	db     *sql.DB
	ID     string
	tables []string
	re     *regexp.Regexp
}

// Begin starts a run whose slice tables are the logical names in tables. The id sorts by time, so
// Prune can tell the newest runs from the oldest.
func Begin(db *sql.DB, tables []string) *Run { return begin(db, tables, "") }

// BeginKeyed is Begin for a run whose inputs are summed up in key (a short hex string). Its tables
// can later be found again with FindRecent, so a caller asked the same question within a short
// time reads them instead of building new ones.
func BeginKeyed(db *sql.DB, tables []string, key string) *Run { return begin(db, tables, key) }

func begin(db *sql.DB, tables []string, key string) *Run {
	id := fmt.Sprintf("r%s%03d", time.Now().UTC().Format("20060102t150405"), seq.Add(1)%1000)
	if key != "" {
		id += "_k" + key
	}
	names := append([]string(nil), tables...)
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = regexp.QuoteMeta(n)
	}
	var re *regexp.Regexp
	if len(quoted) > 0 {
		re = regexp.MustCompile(`\b(?:main\.|temp\.)?(` + strings.Join(quoted, "|") + `)\b`)
	}
	return &Run{db: db, ID: id, tables: names, re: re}
}

// Table is the real name of logical table name in this run.
func (r *Run) Table(name string) string { return name + "__" + r.ID }

// Rewrite replaces every logical table name in sqlText with this run's name. A "main." or "temp."
// in front of a slice table is dropped: the table is an ordinary one now.
func (r *Run) Rewrite(sqlText string) string {
	if r.re == nil {
		return sqlText
	}
	return r.re.ReplaceAllStringFunc(sqlText, func(m string) string {
		name := m
		if i := strings.LastIndexByte(m, '.'); i >= 0 {
			name = m[i+1:]
		}
		return r.Table(name)
	})
}

// Exec runs sqlText, which may hold several statements, one at a time.
func (r *Run) Exec(ctx context.Context, sqlText string, args ...any) error {
	stmts := Split(r.Rewrite(sqlText))
	for i, s := range stmts {
		var a []any
		if i == len(stmts)-1 {
			a = args
		}
		if _, err := r.db.ExecContext(ctx, s, a...); err != nil {
			return fmt.Errorf("runtables: %w\n  statement: %s", err, firstLine(s))
		}
	}
	return nil
}

// Query runs one query against this run's tables.
func (r *Run) Query(ctx context.Context, sqlText string, args ...any) (*sql.Rows, error) {
	return r.db.QueryContext(ctx, r.Rewrite(sqlText), args...)
}

// QueryRow runs one query against this run's tables.
func (r *Run) QueryRow(ctx context.Context, sqlText string, args ...any) *sql.Row {
	return r.db.QueryRowContext(ctx, r.Rewrite(sqlText), args...)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	return s
}

// Split cuts SQL text into statements at semicolons outside quotes and comments. A "-- comment"
// runs to the end of its line and is dropped.
func Split(text string) []string {
	var out []string
	var b strings.Builder
	var quote byte
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			out = append(out, s)
		}
		b.Reset()
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			b.WriteByte(c)
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
			b.WriteByte(c)
		case c == '-' && i+1 < len(text) && text[i+1] == '-':
			for i < len(text) && text[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		case c == ';':
			flush()
		default:
			b.WriteByte(c)
		}
	}
	flush()
	return out
}

// Prune drops the tables of all but the newest keep runs among those named <logical>__r…, for
// each logical name in tables. A table that does not exist is not an error.
func Prune(ctx context.Context, db *sql.DB, tables []string, keep int) error {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE '%\_\_r%' ESCAPE '\'`)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		names = append(names, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	want := map[string]bool{}
	for _, t := range tables {
		want[t] = true
	}
	byRun := map[string][]string{}
	for _, n := range names {
		i := strings.LastIndex(n, "__r")
		if i < 0 || !want[n[:i]] {
			continue
		}
		byRun[n[i+2:]] = append(byRun[n[i+2:]], n)
	}
	runs := make([]string, 0, len(byRun))
	for id := range byRun {
		runs = append(runs, id)
	}
	sort.Strings(runs)
	if len(runs) <= keep {
		return nil
	}
	for _, id := range runs[:len(runs)-keep] {
		for _, n := range byRun[id] {
			if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS "`+n+`"`); err != nil {
				return err
			}
		}
	}
	return nil
}

// Execer is a *sql.DB or a *sql.Tx.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// maxVars keeps one INSERT under SQLite's limit on bound values.
const maxVars = 900

// InsertRows inserts rows into table (columns cols) in as few statements as the bound-value limit
// allows. Against Turso every statement is a network round trip, so rows go in batches.
func InsertRows(ctx context.Context, e Execer, table string, cols []string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	per := max(1, maxVars/len(cols))
	one := "(" + strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",") + ")"
	for i := 0; i < len(rows); i += per {
		end := min(i+per, len(rows))
		var q strings.Builder
		q.WriteString("INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES ")
		args := make([]any, 0, (end-i)*len(cols))
		for j, row := range rows[i:end] {
			if len(row) != len(cols) {
				return fmt.Errorf("runtables: row has %d values for %d columns of %s", len(row), len(cols), table)
			}
			if j > 0 {
				q.WriteByte(',')
			}
			q.WriteString(one)
			args = append(args, row...)
		}
		if _, err := e.ExecContext(ctx, q.String(), args...); err != nil {
			return fmt.Errorf("runtables: insert into %s: %w", table, err)
		}
	}
	return nil
}

// InsertRows inserts into this run's table for logical name table.
func (r *Run) InsertRows(ctx context.Context, table string, cols []string, rows [][]any) error {
	return InsertRows(ctx, r.db, r.Table(table), cols, rows)
}

// idTime is the time encoded in the front of a run id (rYYYYMMDDtHHMMSS…).
func idTime(id string) (time.Time, bool) {
	if len(id) < 16 || id[0] != 'r' {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102t150405", id[1:16])
	return t, err == nil
}

// FindRecent looks for a finished run of the same key built within maxAge. A run is finished when
// its marker table exists: the caller creates that table last, so a run still being built, or one
// that failed half way, is never returned. It reports false when there is none.
func FindRecent(ctx context.Context, db *sql.DB, tables []string, marker, key string, maxAge time.Duration) (*Run, bool, error) {
	if maxAge <= 0 || key == "" {
		return nil, false, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE ? ESCAPE '\'`,
		strings.NewReplacer(`_`, `\_`, `%`, `\%`).Replace(marker)+`\_\_r%\_k`+key)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	best := ""
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, false, err
		}
		if n > best {
			best = n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if best == "" {
		return nil, false, nil
	}
	id := best[strings.LastIndex(best, "__")+2:]
	if t, ok := idTime(id); !ok || time.Since(t) > maxAge {
		return nil, false, nil
	}
	run := begin(db, tables, "")
	run.ID = id
	return run, true, nil
}
