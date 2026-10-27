package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_state"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/check_health"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/config"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
	_ "modernc.org/sqlite"
)

func TestBasicAuthMiddleware(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := basicAuthMiddleware("admin", "s3cr3t", inner)

	// No credentials at all.
	called = false
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized || called {
		t.Errorf("expected 401 and no inner call for missing credentials, got %d (called=%v)", w.Code, called)
	}
	if w.Header().Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate header on 401 response")
	}

	// Wrong credentials.
	called = false
	req = httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "wrong")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized || called {
		t.Errorf("expected 401 and no inner call for wrong password, got %d (called=%v)", w.Code, called)
	}

	// Correct credentials.
	called = false
	req = httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "s3cr3t")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !called {
		t.Errorf("expected 200 and inner call for correct credentials, got %d (called=%v)", w.Code, called)
	}
}

func TestRequiredDashboardCredentialsFailsExplicitlyWhenUnset(t *testing.T) {
	t.Setenv("DASHBOARD_USERNAME", "")
	t.Setenv("DASHBOARD_PASSWORD", "")
	if _, _, err := requiredDashboardCredentials(); err == nil {
		t.Fatal("expected an explicit error when DASHBOARD_USERNAME/DASHBOARD_PASSWORD are unset, got nil")
	}
}

// setupTestDB creates the full schema the real binary assembles across every
// owning package's EnsureSchema, so dashboard tests exercise every table the
// dashboard is expected to print.
func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open memory db: %v", err)
	}
	for _, ensure := range []func(*sql.DB) error{
		account_state.EnsureSchema, scan_market.EnsureSchema, stage_orders.EnsureSchema,
		manage_exits.EnsureSchema, check_health.EnsureSchema,
	} {
		if err := ensure(db); err != nil {
			t.Fatalf("failed to init schema: %v", err)
		}
	}

	_, err = db.Exec(`
		INSERT INTO account_state_account (account_number, account_type, cash_balance, available_funds, liquidation_value, fetched_at)
		VALUES ('74234603', 'MARGIN', 2300.0, 2300.0, 2300.0, datetime('now'));
		INSERT INTO scan_market_signal (symbol, strategy_name, signal_type, price, generated_at, status)
		VALUES ('TECL', 'sig-voo-buy-tecl', 'BUY', 90.5, datetime('now'), 'PENDING');
	`)
	if err != nil {
		t.Fatalf("failed to seed test rows: %v", err)
	}

	return db
}

func TestHandleTables(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	srv := &Server{dbPath: ":memory:", port: 8080, db: db}

	req := httptest.NewRequest("GET", "/api/tables", nil)
	w := httptest.NewRecorder()
	srv.handleTables(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var resp struct {
		Tables []TableDump `json:"tables"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse tables JSON: %v", err)
	}

	if len(resp.Tables) != len(dashboardTables) {
		t.Fatalf("expected %d tables, got %d", len(dashboardTables), len(resp.Tables))
	}

	want := map[string]bool{
		"scan_market_buy_signal": false,
		"scan_market_signal":     false,
		"stage_order":            false,
		"manage_exit":            false,
		"check_health_audit":     false,
		"check_health_finding":   false,
		"check_health":           false,
	}
	var signals *TableDump
	for i := range resp.Tables {
		td := &resp.Tables[i]
		if td.Label == "" || td.Job == "" {
			t.Errorf("table %s is missing a label or job", td.Name)
		}
		if td.Error != "" {
			t.Errorf("table %s unexpectedly errored: %s", td.Name, td.Error)
		}
		if _, ok := want[td.Name]; !ok {
			t.Errorf("unexpected Schwab-mirror (or other) table in job-state dump: %s", td.Name)
			continue
		}
		want[td.Name] = true
		if td.Name == "scan_market_signal" {
			signals = td
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("expected job-state table %s in the response", name)
		}
	}
	if signals == nil {
		t.Fatal("expected a 'scan_market_signal' table in the response")
	}
	if len(signals.Rows) != 1 {
		t.Fatalf("expected scan_market_signal to contain the seeded row, got %+v", signals.Rows)
	}
	symbolIdx := -1
	for i, col := range signals.Columns {
		if col == "symbol" {
			symbolIdx = i
			break
		}
	}
	if symbolIdx < 0 || signals.Rows[0][symbolIdx] != "TECL" {
		t.Errorf("expected scan_market_signal to contain TECL, got %+v (columns=%v)", signals.Rows, signals.Columns)
	}
}

func TestDumpTableMostRecentFirst(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, err := db.Exec(`INSERT INTO account_state_account (account_number, account_type, cash_balance, available_funds, liquidation_value, fetched_at)
		VALUES ('99999999', 'MARGIN', 1.0, 1.0, 1.0, datetime('now'))`)
	if err != nil {
		t.Fatalf("failed to insert second account: %v", err)
	}

	srv := &Server{db: db}
	dump := srv.dumpTable(dashboardTable{Name: "account_state_account", Label: "Accounts", Job: "account_state"})
	if dump.Error != "" {
		t.Fatalf("unexpected dump error: %v", dump.Error)
	}
	if len(dump.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(dump.Rows))
	}
	if dump.Rows[0][0] != "99999999" {
		t.Errorf("expected the most recently inserted row first, got %+v", dump.Rows[0])
	}
}

func TestDumpTableMissingTableReportsErrorInstead(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open memory db: %v", err)
	}
	defer db.Close()

	srv := &Server{db: db}
	dump := srv.dumpTable(dashboardTable{Name: "does_not_exist", Label: "Nope", Job: "nobody"})
	if dump.Error == "" {
		t.Fatal("expected an Error for a missing table instead of a panic/crash")
	}
}

func TestTruncateCell(t *testing.T) {
	short := "hello"
	if got := truncateCell(short); got != short {
		t.Errorf("expected short string unchanged, got %q", got)
	}
	long := strings.Repeat("a", cellTruncateLen+50)
	got := truncateCell(long)
	if len(got) != cellTruncateLen+len("…") {
		t.Errorf("expected truncated length %d, got %d", cellTruncateLen+len("…"), len(got))
	}
}

func TestHandleSchwabStatePrintsClientErrorImmediately(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	t.Setenv("SCHWAB_ACCOUNT_NUMBERS", "25474194,74234603,75124906")
	orig := newTraderClient
	t.Cleanup(func() { newTraderClient = orig })
	newTraderClient = func(ctx context.Context, cfg *config.Config) (*trader.TraderClient, error) {
		return nil, fmt.Errorf("token refresh failed: simulated")
	}

	db := setupTestDB(t)
	defer db.Close()
	srv := &Server{db: db}

	req := httptest.NewRequest("GET", "/account_state", nil)
	w := httptest.NewRecorder()
	srv.handleAccountState(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%q", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "ERROR:") || !strings.Contains(body, "token refresh failed") {
		t.Errorf("expected immediate ERROR body, got %q", body)
	}
}

func TestWriteSchwabStateTableEscapesHTML(t *testing.T) {
	var buf strings.Builder
	writeAccountStateTable(&buf, &account_state.Table{
		Name:    "account_state_order",
		Label:   `Orders <script>alert(1)</script>`,
		Columns: []string{"symbol"},
		Rows:    [][]string{{"<TECL>"}},
	})
	got := buf.String()
	if strings.Contains(got, "<script>") || strings.Contains(got, "<TECL>") {
		t.Fatalf("expected HTML-escaped table, got %s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") || !strings.Contains(got, "&lt;TECL&gt;") {
		t.Fatalf("expected escaped entities, got %s", got)
	}
}

// /stage_orders is a preview only: with a broken Schwab client it must fail
// fast with an ERROR body and, above all, write nothing.
func TestStageOrdersPreviewFailsFastAndWritesNothing(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	t.Setenv("SCHWAB_ACCOUNT_NUMBERS", "111")
	orig := newTraderClient
	defer func() { newTraderClient = orig }()
	newTraderClient = func(ctx context.Context, cfg *config.Config) (*trader.TraderClient, error) {
		return nil, fmt.Errorf("token refresh failed: simulated")
	}
	db := setupTestDB(t)
	defer db.Close()
	srv := &Server{db: db}
	w := httptest.NewRecorder()
	srv.handleStageOrders(w, httptest.NewRequest("GET", "/stage_orders", nil))
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "token refresh failed") {
		t.Fatalf("code=%d body=%q", w.Code, w.Body.String())
	}
}

// A database on which no job has run yet must still serve every panel the
// dashboard lists, with 0 rows, not "no such table". This is the live
// dashboard regression: a freshly restored orchestrator.db had only the
// account_state tables.
func TestDashboardSchemaHasEveryJobTable(t *testing.T) {
	db, err := openDashboardDB(filepath.Join(t.TempDir(), "orchestrator.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{
		"scan_market_buy_signal", "scan_market_signal", "stage_order", "submit_order",
		"manage_exit", "check_health_audit", "check_health_finding", "check_health", "job_run",
	} {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Errorf("table %s: %v", table, err)
		}
	}
}
