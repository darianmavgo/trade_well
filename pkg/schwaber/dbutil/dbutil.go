// Package dbutil opens the shared operational database, which is Turso (libSQL), and
// carries the SQLite-dialect helpers both repos use. A local SQLite file exists only
// under `go test` (modernc.org/sqlite driver "sqlite").
package dbutil

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	_ "modernc.org/sqlite"
)

// Open opens the shared operational database: the Turso database named by TURSO_DATABASE_URL
// and TURSO_AUTH_TOKEN (see Turso). Outside `go test` those are required; there is no local
// file fallback, because an instance's disk is lost when it stops and a laptop file drifts
// from the deployment. Under `go test` Turso is never used and dbPath is opened as a local
// SQLite file with the standard pragmas (WAL, synchronous NORMAL, in-memory temp store and a
// 5s busy timeout), so tests run against real SQLite in a t.TempDir() or ":memory:". No schema
// is created; owners run their own EnsureSchema.
func Open(dbPath string) (*sql.DB, error) {
	dsn, remote, err := Turso()
	if err != nil {
		return nil, err
	}
	if remote {
		return OpenTurso(dsn)
	}
	if !testing.Testing() {
		return nil, fmt.Errorf("dbutil: %s is not set: the database is Turso only (a local SQLite file is not supported outside tests)", EnvTursoURL)
	}
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir %s: %w", dir, err)
		}
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database %s: %w", dbPath, err)
	}
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA temp_store=MEMORY",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	return db, nil
}

// Conn is what AddColumn needs: both *sql.DB and *sql.Tx satisfy it.
type Conn interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// AddColumn adds column to table with the given SQLite declaration (type and
// default, e.g. "REAL") when the table lacks it. Every package's EnsureSchema is
// CREATE TABLE IF NOT EXISTS, which leaves an existing table as it is, so a column
// added to a definition later never reaches a database that already has the table
// (the local one and the replicated GAE one) and the first statement naming it
// fails. AddColumn closes that gap. It only ever adds; it is a no-op when the
// column exists or the table does not exist yet. table and column must be plain
// identifiers; decl comes from source code, never from input.
func AddColumn(db Conn, table, column, decl string) error {
	if !identRe.MatchString(table) || !identRe.MatchString(column) {
		return fmt.Errorf("dbutil: bad identifier %q.%q", table, column)
	}
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return fmt.Errorf("dbutil: read %s columns: %w", table, err)
	}
	seen := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		seen = true
		if name == column {
			rows.Close()
			return nil
		}
	}
	err = rows.Err()
	rows.Close() // release the connection before the ALTER (a transaction has only one)
	if err != nil {
		return err
	}
	if !seen {
		return nil // no such table yet; CREATE TABLE will include the column
	}
	if _, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl)); err != nil {
		return fmt.Errorf("dbutil: add %s.%s: %w", table, column, err)
	}
	return nil
}
