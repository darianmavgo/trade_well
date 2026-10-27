// Package orderintent provides durable "about to place" records so a kill
// after Schwab accepts an order but before the HTTP response returns cannot
// cause a blind retry to double-place.
//
// Schwab's Trader API does not document a client-supplied idempotency key
// (no clientOrderId filter on list/get). We still send clientOrderId on the
// place payload (harmless if ignored) and use it as our PRIMARY KEY. Reconcile
// falls back to matching recent broker orders by symbol, instruction,
// quantity, and entered-time near the intent's created_at.
package orderintent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/schwaber/dbutil"
	_ "modernc.org/sqlite"
)

// Status values for order_intents.status.
const (
	StatusPending = "pending"
	StatusAcked   = "acked"
	StatusFailed  = "failed"
	StatusUnknown = "unknown"
)

// ErrAlreadyExists is returned by BeginIntent when the client_order_id already
// has a pending, acked, or unknown intent.
var ErrAlreadyExists = errors.New("order intent already exists")

// Intent is one durable place-attempt.
type Intent struct {
	ClientOrderID string
	AccountHash   string
	Symbol        string
	Instruction   string
	Quantity      float64
	Status        string
	BrokerOrderID string
	Error         string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	RequestJSON   string
}

// Store persists intents in the shared database (Turso; a SQLite file only in tests and ibkr_api).
type Store struct {
	db *sql.DB
}

// Open opens the intents DB and ensures the schema. On Turso path is ignored. Without Turso it
// opens path as a local SQLite file, which only tests and ibkr_api (not yet moved to Turso) use.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("orderintent: empty db path")
	}
	// The intents live in the shared database, so they follow it to Turso.
	if dsn, remote, err := dbutil.Turso(); err != nil {
		return nil, err
	} else if remote {
		db, err := dbutil.OpenTurso(dsn)
		if err != nil {
			return nil, err
		}
		s := &Store{db: db}
		if err := s.ensureSchema(); err != nil {
			_ = db.Close()
			return nil, err
		}
		return s, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Dir may be "." — MkdirAll(".") is fine; ignore only weird cases.
		_ = err
	}
	// modernc.org/sqlite: single writer; busy_timeout in DSN + MaxOpenConns(1)
	// keeps concurrent BeginIntent callers retrying instead of SQLITE_BUSY.
	dsn := path
	if !strings.Contains(path, "busy_timeout") {
		dsn = path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 10000`); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// OpenDefault opens the shared Turso database (TURSO_DATABASE_URL). Outside `go test` there is no
// local file: it fails when Turso is not configured. Under `go test` it opens schwaber.db next to
// tokenPath, else ./schwaber.db, as a local SQLite file.
func OpenDefault(tokenPath string) (*Store, error) {
	if _, remote, err := dbutil.Turso(); err != nil {
		return nil, err
	} else if remote {
		return Open("turso")
	}
	if !testing.Testing() {
		return nil, fmt.Errorf("orderintent: %s is not set: the intents live in Turso only", dbutil.EnvTursoURL)
	}
	if tokenPath != "" {
		if dir := filepath.Dir(tokenPath); dir != "" && dir != "." {
			return Open(filepath.Join(dir, "schwaber.db"))
		}
	}
	return Open("schwaber.db")
}

// Close closes the underlying DB.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB exposes the underlying *sql.DB for tests.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) ensureSchema() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS order_intents (
	client_order_id TEXT PRIMARY KEY,
	account_hash TEXT NOT NULL,
	symbol TEXT NOT NULL,
	instruction TEXT NOT NULL,
	quantity REAL NOT NULL,
	status TEXT NOT NULL,
	broker_order_id TEXT,
	error TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	request_json TEXT
);
CREATE INDEX IF NOT EXISTS idx_order_intents_status ON order_intents(status);
CREATE INDEX IF NOT EXISTS idx_order_intents_account ON order_intents(account_hash);
`)
	return err
}

// BeginIntent inserts a pending intent BEFORE any HTTP PlaceOrder call.
// If the PK already exists with status pending/acked/unknown, returns
// ErrAlreadyExists (call GetIntent for details). A prior failed intent with
// the same id may be restarted by overwriting to pending.
func (s *Store) BeginIntent(ctx context.Context, intent Intent) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("orderintent: nil store")
	}
	id := strings.TrimSpace(intent.ClientOrderID)
	if id == "" {
		return fmt.Errorf("orderintent: client_order_id required")
	}
	if strings.TrimSpace(intent.AccountHash) == "" {
		return fmt.Errorf("orderintent: account_hash required")
	}
	now := time.Now().UTC()
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = now
	}
	intent.UpdatedAt = now
	intent.Status = StatusPending
	intent.ClientOrderID = id

	existing, err := s.GetIntent(ctx, id)
	if err == nil && existing != nil {
		switch existing.Status {
		case StatusPending, StatusAcked, StatusUnknown:
			return fmt.Errorf("%w: status=%s broker_order_id=%s", ErrAlreadyExists, existing.Status, existing.BrokerOrderID)
		case StatusFailed:
			// Allow retry of a clearly failed attempt with the same key.
			_, err = s.db.ExecContext(ctx, `
UPDATE order_intents SET
	account_hash=?, symbol=?, instruction=?, quantity=?, status=?,
	broker_order_id='', error='', updated_at=?, request_json=?
WHERE client_order_id=?`,
				intent.AccountHash, intent.Symbol, intent.Instruction, intent.Quantity,
				StatusPending, intent.UpdatedAt.Format(time.RFC3339Nano), intent.RequestJSON, id)
			return err
		}
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO order_intents (
	client_order_id, account_hash, symbol, instruction, quantity,
	status, broker_order_id, error, created_at, updated_at, request_json
) VALUES (?, ?, ?, ?, ?, ?, '', '', ?, ?, ?)`,
		id, intent.AccountHash, intent.Symbol, intent.Instruction, intent.Quantity,
		StatusPending,
		intent.CreatedAt.Format(time.RFC3339Nano),
		intent.UpdatedAt.Format(time.RFC3339Nano),
		intent.RequestJSON,
	)
	if err != nil {
		// Race: another process inserted first.
		if existing2, gerr := s.GetIntent(ctx, id); gerr == nil && existing2 != nil {
			return fmt.Errorf("%w: status=%s broker_order_id=%s", ErrAlreadyExists, existing2.Status, existing2.BrokerOrderID)
		}
		return err
	}
	return nil
}

// AckIntent records broker acceptance with the Schwab order id.
func (s *Store) AckIntent(ctx context.Context, clientOrderID, brokerOrderID string) error {
	return s.setStatus(ctx, clientOrderID, StatusAcked, brokerOrderID, "")
}

// FailIntent records a clear broker reject / validation failure.
func (s *Store) FailIntent(ctx context.Context, clientOrderID, errMsg string) error {
	return s.setStatus(ctx, clientOrderID, StatusFailed, "", errMsg)
}

// MarkUnknown leaves the intent unresolved after an ambiguous network error.
func (s *Store) MarkUnknown(ctx context.Context, clientOrderID, errMsg string) error {
	return s.setStatus(ctx, clientOrderID, StatusUnknown, "", errMsg)
}

func (s *Store) setStatus(ctx context.Context, clientOrderID, status, brokerID, errMsg string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("orderintent: nil store")
	}
	id := strings.TrimSpace(clientOrderID)
	if id == "" {
		return fmt.Errorf("orderintent: client_order_id required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `
UPDATE order_intents SET status=?, broker_order_id=?, error=?, updated_at=?
WHERE client_order_id=?`, status, brokerID, errMsg, now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("orderintent: no intent %q", id)
	}
	return nil
}

// GetIntent returns the intent or nil if missing.
func (s *Store) GetIntent(ctx context.Context, clientOrderID string) (*Intent, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("orderintent: nil store")
	}
	row := s.db.QueryRowContext(ctx, `
SELECT client_order_id, account_hash, symbol, instruction, quantity, status,
	IFNULL(broker_order_id,''), IFNULL(error,''), created_at, updated_at, IFNULL(request_json,'')
FROM order_intents WHERE client_order_id=?`, strings.TrimSpace(clientOrderID))
	var in Intent
	var created, updated string
	err := row.Scan(&in.ClientOrderID, &in.AccountHash, &in.Symbol, &in.Instruction, &in.Quantity,
		&in.Status, &in.BrokerOrderID, &in.Error, &created, &updated, &in.RequestJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	in.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if in.CreatedAt.IsZero() {
		in.CreatedAt, _ = time.Parse(time.RFC3339, created)
	}
	in.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if in.UpdatedAt.IsZero() {
		in.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	}
	return &in, nil
}

// ListUnresolved returns pending and unknown intents, oldest first.
func (s *Store) ListUnresolved(ctx context.Context) ([]Intent, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("orderintent: nil store")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT client_order_id, account_hash, symbol, instruction, quantity, status,
	IFNULL(broker_order_id,''), IFNULL(error,''), created_at, updated_at, IFNULL(request_json,'')
FROM order_intents
WHERE status IN ('pending','unknown')
ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Intent
	for rows.Next() {
		var in Intent
		var created, updated string
		if err := rows.Scan(&in.ClientOrderID, &in.AccountHash, &in.Symbol, &in.Instruction, &in.Quantity,
			&in.Status, &in.BrokerOrderID, &in.Error, &created, &updated, &in.RequestJSON); err != nil {
			return nil, err
		}
		in.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if in.CreatedAt.IsZero() {
			in.CreatedAt, _ = time.Parse(time.RFC3339, created)
		}
		in.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		if in.UpdatedAt.IsZero() {
			in.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		}
		out = append(out, in)
	}
	return out, rows.Err()
}
