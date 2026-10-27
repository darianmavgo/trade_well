package check_health

import (
	"database/sql"
	"fmt"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// RecordAudit logs the daily account inspection result to check_health_audit.
func RecordAudit(db *sql.DB, audit *Audit) error {
	query := orchestratorsql.Q("check_health/record_audit")
	_, err := db.Exec(query,
		audit.AccountNumber, audit.Timestamp, audit.TotalPositions,
		audit.ProtectedPositions, audit.UnprotectedPositions,
		audit.OrphanOrders, audit.Status, audit.Notes,
		audit.CriticalFindings, audit.WarningFindings,
	)
	return err
}

// ListChecks returns every defined health check (built-in or custom),
// grouped by category then name.
func ListChecks(db *sql.DB) ([]Def, error) {
	rows, err := db.Query(orchestratorsql.Q("check_health/list_checks"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Def
	for rows.Next() {
		var c Def
		var enabled int
		if err := rows.Scan(&c.ID, &c.Name, &c.Category, &c.Severity, &enabled, &c.Query, &c.Description, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Enabled = enabled != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetEnabled toggles a named health check on or off. This is how an
// operator manages the check_health table without touching code: disable a
// noisy check, or (for a custom row inserted directly into check_health)
// turn a new one on.
func SetEnabled(db *sql.DB, name string, enabled bool) error {
	res, err := db.Exec(orchestratorsql.Q("check_health/set_enabled"), enabled, name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no health check named %q", name)
	}
	return nil
}

// RecordFinding persists one check_health violation, whether it came from a
// Def's SQL query or from Run's own live-broker-data inspection.
func RecordFinding(db *sql.DB, f *Finding) error {
	res, err := db.Exec(orchestratorsql.Q("check_health/record_finding"),
		f.RunAt, f.AccountNumber, f.CheckName, f.Category, f.Severity, f.Subject, f.Detail)
	if err != nil {
		return err
	}
	f.ID, err = res.LastInsertId()
	return err
}

// GetRecentFindings returns the most recent findings for accountNumber
// ("" matches every account), newest first, capped at limit.
func GetRecentFindings(db *sql.DB, accountNumber string, limit int) ([]Finding, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Query(orchestratorsql.Q("check_health/get_recent_findings"), accountNumber, accountNumber, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Finding
	for rows.Next() {
		var f Finding
		var subject sql.NullString
		if err := rows.Scan(&f.ID, &f.RunAt, &f.AccountNumber, &f.CheckName, &f.Category, &f.Severity, &subject, &f.Detail); err != nil {
			return nil, err
		}
		f.Subject = subject.String
		out = append(out, f)
	}
	return out, rows.Err()
}

// RunSQLHealthChecks executes every enabled, data-driven health check
// (check_health) against accountNumber, persists each violation as a
// check_health_finding row, and returns the findings it recorded. A check
// whose query fails to execute or scan (e.g. a malformed custom check) is
// itself recorded as a WARNING finding under category "check_error" instead
// of aborting the rest of the run.
func RunSQLHealthChecks(db *sql.DB, accountNumber string, runAt time.Time) ([]Finding, error) {
	checks, err := ListChecks(db)
	if err != nil {
		return nil, err
	}

	var findings []Finding
	for _, c := range checks {
		if !c.Enabled {
			continue
		}
		rows, err := db.Query(c.Query, accountNumber)
		if err != nil {
			findings = append(findings, Finding{
				RunAt: runAt, AccountNumber: accountNumber, CheckName: c.Name,
				Category: "check_error", Severity: "WARNING",
				Detail: fmt.Sprintf("check %q failed to run: %v", c.Name, err),
			})
			continue
		}
		for rows.Next() {
			var subject, detail sql.NullString
			if err := rows.Scan(&subject, &detail); err != nil {
				findings = append(findings, Finding{
					RunAt: runAt, AccountNumber: accountNumber, CheckName: c.Name,
					Category: "check_error", Severity: "WARNING",
					Detail: fmt.Sprintf("check %q returned unexpected columns: %v", c.Name, err),
				})
				break
			}
			findings = append(findings, Finding{
				RunAt: runAt, AccountNumber: accountNumber, CheckName: c.Name,
				Category: c.Category, Severity: c.Severity,
				Subject: subject.String, Detail: detail.String,
			})
		}
		if err := rows.Err(); err != nil {
			findings = append(findings, Finding{
				RunAt: runAt, AccountNumber: accountNumber, CheckName: c.Name,
				Category: "check_error", Severity: "WARNING",
				Detail: fmt.Sprintf("check %q errored while reading rows: %v", c.Name, err),
			})
		}
		rows.Close()
	}

	for i := range findings {
		if err := RecordFinding(db, &findings[i]); err != nil {
			return findings, err
		}
	}
	return findings, nil
}
