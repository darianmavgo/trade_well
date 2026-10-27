package dbutil

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	_ "github.com/tursodatabase/libsql-client-go/libsql"
)

// Environment variables that point the shared database at Turso instead of a file.
const (
	EnvTursoURL   = "TURSO_DATABASE_URL"
	EnvTursoToken = "TURSO_AUTH_TOKEN"
)

// Turso reports the Turso database URL and auth token from the environment. ok is false when
// TURSO_DATABASE_URL is unset, and always false inside `go test`, so a test can never write a
// production database because the developer's shell has the variables exported.
func Turso() (dsn string, ok bool, err error) {
	raw := strings.TrimSpace(os.Getenv(EnvTursoURL))
	if raw == "" || testing.Testing() {
		return "", false, nil
	}
	dsn, err = tursoDSN(raw, strings.TrimSpace(os.Getenv(EnvTursoToken)))
	return dsn, err == nil, err
}

// tursoDSN builds the driver DSN from the database URL and auth token.
func tursoDSN(raw, token string) (string, error) {
	if token == "" {
		return "", fmt.Errorf("dbutil: %s is set but %s is empty", EnvTursoURL, EnvTursoToken)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("dbutil: bad %s %q", EnvTursoURL, raw)
	}
	// libsql:// would use a WebSocket; plain HTTPS is one request per statement batch and
	// suits App Engine, where instances come and go between requests.
	if u.Scheme == "libsql" || u.Scheme == "wss" {
		u.Scheme = "https"
	}
	q := u.Query()
	q.Set("authToken", token)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// OpenTurso opens the Turso database named by the environment. No pragmas are applied: the
// server owns journaling, so WAL and busy_timeout do not apply.
func OpenTurso(dsn string) (*sql.DB, error) {
	plain, err := sql.Open("libsql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open turso database: %w", err)
	}
	if err := plain.Ping(); err != nil {
		plain.Close()
		return nil, fmt.Errorf("reach turso database: %w", err)
	}
	// The plain pool stays open: the date-column adapter reads the schema through it.
	return sql.OpenDB(timeConnector{dsn: dsn, cols: &timeColumns{db: plain}}), nil
}

var (
	ensureMu   sync.Mutex
	ensureDone = map[string]bool{}
)

// EnsureOnce runs fn, the schema setup for key. Against a local file it runs every time (it is
// cheap and tests give each call a fresh database). Against Turso every statement is a network
// round trip, so a successful fn runs once per process and later calls return at once.
func EnsureOnce(key string, fn func() error) error {
	if _, remote, _ := Turso(); !remote {
		return fn()
	}
	ensureMu.Lock()
	done := ensureDone[key]
	ensureMu.Unlock()
	if done {
		return nil
	}
	// The lock is not held while fn runs: setups nest (the dashboard's calls schemas.EnsureAll),
	// and every setup is idempotent, so two racing first calls are harmless.
	if err := fn(); err != nil {
		return err
	}
	ensureMu.Lock()
	ensureDone[key] = true
	ensureMu.Unlock()
	return nil
}
