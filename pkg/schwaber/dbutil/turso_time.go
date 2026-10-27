package dbutil

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"sync"
	"time"

	"github.com/tursodatabase/libsql-client-go/libsql"
)

// The libsql driver returns every DATETIME/TIMESTAMP/DATE column as a string and ignores the
// declared type the server sends. The local modernc driver turns those columns into time.Time,
// and the code scans them into time.Time, so this adapter does the same for Turso. It is a thin
// wrapper over the real driver (no fake): it only converts values in columns whose declared
// type, read from the database schema, is a date type.

// timeColumns is the set of column names declared DATE, DATETIME or TIMESTAMP anywhere in the
// schema. It is reloaded after any statement that starts with CREATE or ALTER.
type timeColumns struct {
	db    *sql.DB // plain pool used only to read the schema
	mu    sync.Mutex
	names map[string]bool
	dirty bool
}

func (t *timeColumns) has(col string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dirty || t.names == nil {
		names := map[string]bool{}
		rows, err := t.db.Query(`SELECT DISTINCT lower(p.name) FROM sqlite_master m, pragma_table_info(m.name) p
			WHERE m.type = 'table' AND upper(p.type) IN ('DATE', 'DATETIME', 'TIMESTAMP')`)
		if err == nil {
			for rows.Next() {
				var n string
				if rows.Scan(&n) == nil {
					names[n] = true
				}
			}
			err = rows.Err()
			rows.Close()
		}
		if err == nil {
			t.names, t.dirty = names, false
		}
	}
	return t.names[strings.ToLower(col)]
}

func (t *timeColumns) noteDDL(query string) {
	q := strings.ToUpper(strings.TrimSpace(query))
	if strings.HasPrefix(q, "CREATE") || strings.HasPrefix(q, "ALTER") || strings.HasPrefix(q, "DROP") {
		t.mu.Lock()
		t.dirty = true
		t.mu.Unlock()
	}
}

var timeLayouts = []string{
	"2006-01-02 15:04:05.999999999 -0700 MST", // Go time.Time.String(), what the code stores
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02",
}

// parseTime reads the text forms SQLite date columns hold. Forms without a zone are UTC.
func parseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, " m="); i >= 0 { // monotonic clock reading
		s = s[:i]
	}
	for _, l := range timeLayouts {
		if t, err := time.ParseInLocation(l, s, time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

type timeConnector struct {
	dsn  string
	cols *timeColumns
}

func (c timeConnector) Connect(context.Context) (driver.Conn, error) {
	inner, err := libsql.Driver{}.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &timeConn{Conn: inner, cols: c.cols}, nil
}

func (c timeConnector) Driver() driver.Driver { return libsql.Driver{} }

type timeConn struct {
	driver.Conn
	cols *timeColumns
}

func (c *timeConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	c.cols.noteDDL(query)
	var st driver.Stmt
	var err error
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		st, err = p.PrepareContext(ctx, query)
	} else {
		st, err = c.Conn.Prepare(query)
	}
	if err != nil {
		return nil, err
	}
	return &timeStmt{Stmt: st, cols: c.cols}, nil
}

func (c *timeConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *timeConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.Conn.Begin()
}

func (c *timeConn) PingContext(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *timeConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	e, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	c.cols.noteDDL(query)
	return e.ExecContext(ctx, query, args)
}

func (c *timeConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := q.QueryContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return &timeRows{Rows: rows, cols: c.cols}, nil
}

type timeStmt struct {
	driver.Stmt
	cols *timeColumns
}

func (s *timeStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if e, ok := s.Stmt.(driver.StmtExecContext); ok {
		return e.ExecContext(ctx, args)
	}
	return nil, driver.ErrSkip
}

func (s *timeStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	q, ok := s.Stmt.(driver.StmtQueryContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := q.QueryContext(ctx, args)
	if err != nil {
		return nil, err
	}
	return &timeRows{Rows: rows, cols: s.cols}, nil
}

type timeRows struct {
	driver.Rows
	cols   *timeColumns
	isTime []bool
}

func (r *timeRows) Next(dest []driver.Value) error {
	if err := r.Rows.Next(dest); err != nil {
		return err
	}
	if r.isTime == nil {
		names := r.Rows.Columns()
		r.isTime = make([]bool, len(names))
		for i, n := range names {
			r.isTime[i] = r.cols.has(n)
		}
	}
	for i, d := range dest {
		if s, ok := d.(string); ok && i < len(r.isTime) && r.isTime[i] {
			if t, ok := parseTime(s); ok {
				dest[i] = t
			}
		}
	}
	return nil
}

func (r *timeRows) HasNextResultSet() bool {
	n, ok := r.Rows.(driver.RowsNextResultSet)
	return ok && n.HasNextResultSet()
}

func (r *timeRows) NextResultSet() error {
	if n, ok := r.Rows.(driver.RowsNextResultSet); ok {
		r.isTime = nil
		return n.NextResultSet()
	}
	return sql.ErrNoRows
}
