// Package check_health runs the pluggable health-check contract (live
// broker-data inspection plus data-driven SQL checks) against an account's
// positions, staged orders, and open trades, cross-checked against what
// each strategy's own DefaultConfig() actually dictates. No package below
// cmd/orchestrator calls backtestgosqlite's global strategy registry
// directly, so Run and the contract-audit functions take a pre-resolved
// map[string]strategy.Strategy from the caller.
package check_health

import "time"

// Audit records a daily account protection inspection (the check_health_audit table).
type Audit struct {
	ID                   int64     `json:"id"`
	AccountNumber        string    `json:"account_number"`
	Timestamp            time.Time `json:"timestamp"`
	TotalPositions       int       `json:"total_positions"`
	ProtectedPositions   int       `json:"protected_positions"`
	UnprotectedPositions int       `json:"unprotected_positions"`
	OrphanOrders         int       `json:"orphan_orders"`
	Status               string    `json:"status"` // HEALTHY, WARNING, ACTION_REQUIRED
	Notes                string    `json:"notes"`
	CriticalFindings     int       `json:"critical_findings"`
	WarningFindings      int       `json:"warning_findings"`
}

// Def is a data-driven health check: a named SQL query executed against the
// orchestrator database, parameterized by account_number as its only placeholder.
// The query must return exactly two text columns (subject, detail) -- one
// row per violation found. Editing a row's `enabled` flag (via
// SetEnabled or a direct UPDATE) turns that check on/off without a code
// change or redeploy. Stored in the check_health table.
type Def struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Category    string    `json:"category"`
	Severity    string    `json:"severity"` // WARNING or CRITICAL
	Enabled     bool      `json:"enabled"`
	Query       string    `json:"query"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// Finding is one violation instance surfaced by a health-check run, whether
// from a Def's SQL query or from Run's own live-broker-data inspection.
// Every finding from a run lands in the check_health_finding table, the
// single place to look for "what did the last audit find" regardless of
// source.
type Finding struct {
	ID            int64     `json:"id"`
	RunAt         time.Time `json:"run_at"`
	AccountNumber string    `json:"account_number"`
	CheckName     string    `json:"check_name"`
	Category      string    `json:"category"`
	Severity      string    `json:"severity"`
	Subject       string    `json:"subject"`
	Detail        string    `json:"detail"`
}

// PositionHealth represents the audit status of an individual holding.
type PositionHealth struct {
	Symbol           string
	Quantity         float64
	AveragePrice     float64
	MarketValue      float64
	HasProfitTaker   bool
	ProfitTakerPrice float64
	HasStopLoss      bool
	StopLossPrice    float64
	HasScheduledExit bool
	DaysHeld         int
	MaxDays          int
	Status           string // SECURE, UNPROTECTED, ORPHAN
	Warnings         []string
}

// Result is the comprehensive health assessment of the trading account
// produced by one Run.
type Result struct {
	AccountNumber        string
	Timestamp            time.Time
	CashAvailable        float64
	LiquidationValue     float64
	TotalPositions       int
	ProtectedPositions   int
	UnprotectedPositions int
	OrphanOrders         int
	Positions            []PositionHealth
	PositionAudits       []PositionAudit
	OverallStatus        string // HEALTHY, WARNING, ACTION_REQUIRED
	Recommendations      []string
	Findings             []Finding
	CriticalFindings     int
	WarningFindings      int
}

// Row is one line of a job-run report for check_health, matching the shape
// every other job package renders its run through so an operator never has
// to relearn a different report format per job. Kept package-local (not
// pkg/send_email's type) to keep check_health decoupled from send_email,
// consistent with pkg/stage_orders and pkg/manage_exits.
type Row struct {
	Account  string
	Strategy string
	Symbol   string
	Signal   string
	Status   string
	Detail   string
}

// Rows renders r as report rows for emailing: one HEALTHY row when there
// are no findings, otherwise one row per finding.
func (r *Result) Rows() []Row {
	// Current positions come first: one row per held position, whatever its verdict.
	var rows []Row
	for _, a := range r.PositionAudits {
		rows = append(rows, Row{Account: r.AccountNumber, Strategy: orDash(a.Strategy), Symbol: a.Symbol,
			Signal: "POSITION", Status: a.Status, Detail: a.Summary()})
	}
	rows = append(rows, r.findingRows()...)
	return rows
}

func (r *Result) findingRows() []Row {
	if len(r.Findings) == 0 {
		return []Row{{
			Account: r.AccountNumber, Strategy: "-", Symbol: "-",
			Signal: "HEALTHY", Status: "HEALTHY",
			Detail: pluralPositions(r.TotalPositions),
		}}
	}
	rows := make([]Row, 0, len(r.Findings))
	for _, f := range r.Findings {
		rows = append(rows, Row{
			Account: r.AccountNumber, Strategy: f.Category, Symbol: f.Subject,
			Signal: f.CheckName, Status: f.Severity, Detail: f.Detail,
		})
	}
	return rows
}
