package dashboard

import (
	"context"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_allowlist"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_state"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/schemas"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/schwaber/dbutil"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

//go:embed ui.html
var uiHTML []byte

// newTraderClient is a package var so tests can replace it so a missing/broken
// credential fails in-process instead of hanging on a live token refresh.
var newTraderClient = trader.NewTraderClient

// schwabClientInitTimeout bounds how long handleAccountState waits to build a
// client (token load/refresh) before giving up and flushing the error.
const schwabClientInitTimeout = 15 * time.Second

type Server struct {
	mu     sync.Mutex
	dbPath string
	port   int
	db     *sql.DB
}

func openDashboardDB(dbPath string) (*sql.DB, error) {
	db, err := appconfig.OpenDB(dbPath)
	if err != nil {
		return nil, err
	}
	if err := ensureAllSchemas(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to init schema: %w", err)
	}
	return db, nil
}

func ensureAllSchemas(db *sql.DB) error {
	return dbutil.EnsureOnce("dashboard.ensureAllSchemas", func() error { return ensureAllSchemasNow(db) })
}

func ensureAllSchemasNow(db *sql.DB) error {
	if err := EnsureSchema(db); err != nil {
		return err
	}
	// Every job's tables, so a job that has not run yet shows 0 rows, not
	// "no such table".
	return schemas.EnsureAll(db)
}

func NewServer(dbPath string, port int) (*Server, error) {
	if port <= 0 {
		port = 8080
	}
	db, err := openDashboardDB(dbPath)
	if err != nil {
		return nil, err
	}
	return &Server{
		dbPath: dbPath,
		port:   port,
		db:     db,
	}, nil
}

func (s *Server) reopenDB() error {
	if s.db != nil {
		_ = s.db.Close()
		s.db = nil
	}
	db, err := openDashboardDB(s.dbPath)
	if err != nil {
		return err
	}
	s.db = db
	log.Printf("[dashboard] reopened the database (%s) after an unreadable handle", dbLabel(s.dbPath))
	return nil
}

func (s *Server) ensureDB() error {
	if s.db != nil {
		if err := s.db.Ping(); err == nil {
			if _, err := s.db.Exec(orchestratorsql.Q("dashboard/ensure_db")); err == nil {
				return ensureAllSchemas(s.db)
			}
		}
	}
	return s.reopenDB()
}

// requiredDashboardCredentials reads the dashboard's Basic Auth credentials
// from the environment. There is no default username/password: an
// unconfigured dashboard refuses to start rather than serving account
// balances and job state to anyone who reaches the URL.
func requiredDashboardCredentials() (string, string, error) {
	username := os.Getenv("DASHBOARD_USERNAME")
	password := os.Getenv("DASHBOARD_PASSWORD")
	if username == "" || password == "" {
		return "", "", fmt.Errorf("DASHBOARD_USERNAME and DASHBOARD_PASSWORD must both be set in the config object (deploy_gae/config.sh); refusing to start an unauthenticated dashboard")
	}
	return username, password, nil
}

// basicAuthMiddleware gates every request behind HTTP Basic Auth, comparing
// credentials in constant time so response timing can't leak how much of a
// guess matched.
func basicAuthMiddleware(username, password string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(username)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(password)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="Trade Orchestrator Dashboard"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAuth wraps next in the dashboard's HTTP Basic Auth (DASHBOARD_USERNAME /
// DASHBOARD_PASSWORD). It fails, rather than leave next open, when either is unset.
// Use it for any operator-only route that lives outside the dashboard mux.
func RequireAuth(next http.Handler) (http.Handler, error) {
	username, password, err := requiredDashboardCredentials()
	if err != nil {
		return nil, err
	}
	return basicAuthMiddleware(username, password, next), nil
}

func (s *Server) Handler() (http.Handler, error) {
	username, password, err := requiredDashboardCredentials()
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()

	// Frontend Single-Page App
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(uiHTML)
	})

	mux.HandleFunc("/api/tables", s.handleTables)
	// /account_state is the job name: same as the CLI subcommand and /cron/account_state.
	mux.HandleFunc("/account_state", s.handleAccountState)
	// /stage_orders is a by-hand DRY-RUN preview of the job (never writes).
	mux.HandleFunc("/stage_orders", s.handleStageOrders)
	// /check_health is the job name too: which scheduled job did its job, plus the
	// stored results of the last health run. Read-only.
	mux.HandleFunc("/check_health", s.handleCheckHealth)
	// /risk: equity, drawdown and what new entries may do, per account. Read-only.
	mux.HandleFunc("/risk", s.handleRisk)
	// /manage_exit prints the manage_exit table (open, pending, closed). Read-only.
	mux.HandleFunc("/manage_exit", s.handleManageExit)
	// /status/{strategy-id}: one strategy's signal check, positions, hold windows and
	// anything that went wrong, as JSON. Read-only.
	mux.HandleFunc("/status/", s.handleStatus)
	mux.HandleFunc("/status", s.handleStatus)
	// /orders: every Schwab order with its real fill price beside the limit and stop. Read-only.
	mux.HandleFunc("/orders", s.handleOrders)
	// /strategy: the same for every greenlit strategy, one summary row each.
	mux.HandleFunc("/strategy", s.handleStrategies)
	mux.HandleFunc("/strategy/", s.handleStrategies)
	// /live_vs_backtest_winrate: backtest in-sample, out-of-sample and live win rate side by side. Read-only.
	mux.HandleFunc("/live_vs_backtest_winrate", s.handleLiveVsBacktestWinrate)

	return basicAuthMiddleware(username, password, mux), nil
}

func (s *Server) Start() error {
	h, err := s.Handler()
	if err != nil {
		return err
	}
	addr := fmt.Sprintf(":%d", s.port)
	fmt.Printf("\nSchwab Trade Orchestrator Dashboard running on: http://localhost:%d\n", s.port)
	fmt.Printf("   Connected Database: %s\n\n", dbLabel(s.dbPath))
	if err := logServerStart(s.db, s.port); err != nil {
		log.Printf("[dashboard] failed to log server start: %v", err)
	}
	return http.ListenAndServe(addr, h)
}

func (s *Server) handleAccountState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDB(); err != nil {
		log.Printf("[dashboard] handleAccountState: %v", err)
		http.Error(w, "ERROR: database unavailable: "+err.Error(), http.StatusInternalServerError)
		return
	}

	cfg, err := appconfig.Load()
	if err != nil {
		log.Printf("[dashboard] handleAccountState: %v", err)
		http.Error(w, "ERROR: "+err.Error(), http.StatusInternalServerError)
		return
	}
	targets, err := account_allowlist.FromEnv()
	if err != nil {
		log.Printf("[dashboard] handleAccountState: %v", err)
		http.Error(w, "ERROR: "+err.Error(), http.StatusInternalServerError)
		return
	}

	initCtx, cancel := context.WithTimeout(r.Context(), schwabClientInitTimeout)
	defer cancel()
	tc, err := newTraderClient(initCtx, cfg.Schwab)
	if err != nil {
		if initCtx.Err() != nil {
			err = fmt.Errorf("timed out after %s initializing Schwab client: %w", schwabClientInitTimeout, err)
		}
		log.Printf("[dashboard] handleAccountState: %v", err)
		http.Error(w, "ERROR: failed to initialize Schwab client: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	writeAccountStateOpen(w, targets)
	flush()

	emit := func(ev account_state.Event) {
		if ev.Error != "" {
			writeAccountStateError(w, ev.Error)
			log.Printf("[dashboard] account_state: %s", ev.Error)
		}
		if ev.Status != "" {
			writeAccountStateStatus(w, ev.Status)
			log.Printf("[dashboard] account_state: %s", ev.Status)
		}
		if ev.Table != nil {
			writeAccountStateTable(w, ev.Table)
		}
		flush()
	}

	result, err := account_state.SyncWithProgress(r.Context(), s.db, tc, targets, emit)
	if err != nil {
		writeAccountStateError(w, "ERROR: "+err.Error())
		writeAccountStateClose(w)
		flush()
		return
	}
	writeAccountStateStatus(w, fmt.Sprintf("OK accounts=%d orders=%d transactions=%d",
		result.AccountsFound, result.OrdersFound, result.TransactionsFound))
	writeAccountStateClose(w)
	flush()
}

// dashboardTable describes one SQLite table the dashboard prints, and which
// scheduled job(s) populate it. The homepage only dumps orchestrator
// lifecycle tables — account_state_* mirror tables live in the same DB but
// are not printed here.
type dashboardTable struct {
	Name   string
	Label  string
	Job    string
	JobKey string // canonical job_runs.job name; empty = no run tracking
	// Where limits the dump to current rows (no leading WHERE). Empty means every row.
	// ?all=1 on /api/tables ignores it, and so does an unset Where.
	Where string
	Args  []any
}

var dashboardTables = []dashboardTable{
	{Name: "scan_market_buy_signal", Label: "Buy Signals (review)", Job: "scan_market → review before stage", JobKey: "scan_market"},
	{Name: "scan_market_signal", Label: "Strategy Signals", Job: "scan_market NO_BUY notes, stage_orders → submit_orders status", JobKey: "scan_market"},
	{Name: "stage_order", Label: "Staged Orders", Job: "scan_market → submit_orders", JobKey: "stage_orders"},
	{Name: "manage_exit", Label: "Active Trades", Job: "submit_orders → manage_exits"},
	{Name: "check_health_audit", Label: "Health Audits", Job: "check_health", JobKey: "check_health"},
	{Name: "check_health_finding", Label: "Health Check Findings", Job: "check_health", JobKey: "check_health"},
	{Name: "check_health", Label: "Health Check Definitions", Job: "seeded by init; managed via `health_checks`"},
}

// currentWhere is what the default view of a table shows: what is still live, not what was
// retired. It hides orders and trades clear_stale retired, the rows of strategies that are not
// greenlit (STRATEGY_ALLOWLIST), and all but the latest check_health run. ?all=1 shows
// everything. The list of greenlit ids is the allowlist entries plus the members of its stacks.
func currentWhere(table string, greenlit []string) (string, []any) {
	in := func(col string) (string, []any) {
		if len(greenlit) == 0 {
			return "", nil // no allowlist known: hide nothing rather than everything
		}
		args := make([]any, len(greenlit))
		for i, g := range greenlit {
			args[i] = g
		}
		return col + " IN (?" + strings.Repeat(",?", len(greenlit)-1) + ")", args
	}
	switch table {
	case "stage_order":
		w, a := in("strategy_name")
		if w != "" {
			w = " AND " + w
		}
		return "status <> 'CLEARED'" + w, a
	case "manage_exit":
		w, a := in("strategy_name")
		if w != "" {
			w = " AND " + w
		}
		return "IFNULL(exit_reason_code, '') <> 'STALE_CLEARED'" + w, a
	case "scan_market_buy_signal":
		return in("strategy_id")
	case "scan_market_signal":
		return in("strategy_name")
	case "check_health_finding":
		return "substr(run_at, 1, 13) = (SELECT substr(MAX(run_at), 1, 13) FROM check_health_finding)", nil
	case "check_health_audit":
		return "substr(timestamp, 1, 13) = (SELECT substr(MAX(timestamp), 1, 13) FROM check_health_audit)", nil
	}
	return "", nil
}

// greenlitIDs is every id the allowlist runs: its entries and the members of its stacks.
func greenlitIDs() []string {
	allow, err := strategies.Parse(os.Getenv(strategies.EnvAllowlist))
	if err != nil {
		return nil
	}
	return append(append([]string{}, allow...), strategies.Members(allow)...)
}

// TableDump is one table's most recent rows, ready to render as-is.
type TableDump struct {
	Name    string     `json:"name"`
	Label   string     `json:"label"`
	Job     string     `json:"job"`
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
	Error   string     `json:"error,omitempty"`
	// EmptyState is set when Rows is empty:
	//   "never_run"  — owning job has no job_runs row yet
	//   "ran_empty"  — job ran but produced no rows for this table
	//   "empty"      — no job tracking / unknown
	EmptyState     string `json:"empty_state,omitempty"`
	EmptyMessage   string `json:"empty_message,omitempty"`
	LastRunAt      string `json:"last_run_at,omitempty"`
	LastRunStatus  string `json:"last_run_status,omitempty"`
	LastRunSummary string `json:"last_run_summary,omitempty"`
}

const (
	tableRowLimit   = 200
	cellTruncateLen = 300
)

// handleTables dumps the job-lifecycle tables, most-recent-row first,
// clearly labeled with the table name and the job that owns it.
func (s *Server) handleTables(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Under `go test` the database is a real SQLite file (dbPath): take a fresh handle on it.
	// On Turso (dbPath empty) the handle is a stateless HTTPS client and needs no reopen.
	if s.dbPath != "" && s.dbPath != ":memory:" {
		if err := s.reopenDB(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	dumps := make([]TableDump, 0, len(dashboardTables))
	greenlit := greenlitIDs()
	showAll := r.URL.Query().Get("all") == "1"
	for _, t := range dashboardTables {
		if !showAll {
			t.Where, t.Args = currentWhere(t.Name, greenlit)
		}
		dumps = append(dumps, s.dumpTable(t))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"tables": dumps})
}

func (s *Server) dumpTable(t dashboardTable) TableDump {
	dump := TableDump{Name: t.Name, Label: t.Label, Job: t.Job}

	where := ""
	if t.Where != "" {
		where = " WHERE " + t.Where
	}
	q := fmt.Sprintf("SELECT * FROM %s%s ORDER BY rowid DESC LIMIT %d", t.Name, where, tableRowLimit)
	rows, err := s.db.Query(q, t.Args...)
	if err != nil {
		dump.Error = err.Error()
		log.Printf("[dashboard] failed to query %s: %v", t.Name, err)
		return dump
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		dump.Error = err.Error()
		return dump
	}
	dump.Columns = cols

	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			dump.Error = err.Error()
			break
		}

		rowOut := make([]string, len(cols))
		for i, v := range raw {
			rowOut[i] = truncateCell(stringifyCell(v))
		}
		dump.Rows = append(dump.Rows, rowOut)
	}
	if err := rows.Err(); err != nil && dump.Error == "" {
		dump.Error = err.Error()
	}
	attachEmptyState(&dump, s.db, t.JobKey)
	return dump
}

// attachEmptyState fills EmptyState/EmptyMessage so the UI can tell "job never
// ran" apart from "job ran, zero signals/rows".
func attachEmptyState(dump *TableDump, db *sql.DB, jobKey string) {
	if dump == nil || dump.Error != "" || len(dump.Rows) > 0 {
		return
	}
	if jobKey == "" {
		dump.EmptyState = "empty"
		dump.EmptyMessage = "No rows yet."
		return
	}
	run, err := job_runs.LastJobRun(db, jobKey)
	if err != nil {
		dump.EmptyState = "empty"
		dump.EmptyMessage = "No rows yet."
		return
	}
	if run == nil {
		dump.EmptyState = "never_run"
		dump.EmptyMessage = fmt.Sprintf("Job %q has not run yet — nothing to show (not the same as zero signals).", jobKey)
		return
	}
	dump.EmptyState = "ran_empty"
	dump.LastRunAt = run.FinishedAt.Format(time.RFC3339)
	dump.LastRunStatus = run.Status
	dump.LastRunSummary = run.Summary
	dump.EmptyMessage = fmt.Sprintf("Last %s finished %s (%s) with 0 rows. %s",
		jobKey, run.FinishedAt.Local().Format("Jan 2 3:04 PM"), run.Status, run.Summary)
}

func stringifyCell(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(val)
	case string:
		return val
	case time.Time:
		return val.Format(time.RFC3339)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func truncateCell(s string) string {
	if len(s) <= cellTruncateLen {
		return s
	}
	return s[:cellTruncateLen] + "…"
}

// dbLabel names the database in logs: the file under `go test`, "turso" otherwise.
func dbLabel(path string) string {
	if path == "" {
		return "turso"
	}
	return path
}
