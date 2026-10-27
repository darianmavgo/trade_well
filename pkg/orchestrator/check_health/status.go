package check_health

import (
	"database/sql"
	"fmt"
	"time"
)

// Status is everything the /check_health page shows: each scheduled job against
// its latest slot, and the stored results of the last health run per account.
// It is assembled from stored rows only, so viewing it never calls the broker
// and never writes.
type Status struct {
	At        time.Time
	Jobs      []JobStatus
	Root      *Finding        // pipeline_root_cause when a job is broken, else nil
	Audits    []Audit         // latest audit per account
	Positions []PositionAudit // each account's latest position audit, worst first
	Findings  []Finding       // findings of the latest run per account, worst first
}

// Healthy reports whether every scheduled job that is due did its job.
func (s *Status) Healthy() bool { return s.Root == nil }

// LoadStatus reads the pipeline verdicts, the latest audit per account and the
// findings of each account's latest run.
func LoadStatus(db *sql.DB, now time.Time) (*Status, error) {
	if err := EnsureSchema(db); err != nil {
		return nil, fmt.Errorf("check_health: ensure schema: %w", err)
	}
	jobs, err := PipelineStatus(db, now)
	if err != nil {
		return nil, err
	}
	st := &Status{At: now, Jobs: jobs}

	diag, err := DiagnosePipeline(db, now)
	if err != nil {
		return nil, err
	}
	for i := range diag {
		if diag[i].CheckName == "pipeline_root_cause" || diag[i].CheckName == "killswitch_engaged" {
			st.Root = &diag[i]
		}
	}

	if st.Positions, err = LatestPositionAudits(db); err != nil {
		return nil, fmt.Errorf("read position audits: %w", err)
	}

	arows, err := db.Query(`
		SELECT id, account_number, timestamp, total_positions, protected_positions, unprotected_positions,
		       orphan_orders, status, IFNULL(notes, ''), critical_findings, warning_findings
		FROM check_health_audit
		WHERE id IN (SELECT MAX(id) FROM check_health_audit GROUP BY account_number)
		ORDER BY account_number`)
	if err != nil {
		return nil, fmt.Errorf("read audits: %w", err)
	}
	defer arows.Close()
	for arows.Next() {
		var a Audit
		if err := arows.Scan(&a.ID, &a.AccountNumber, &a.Timestamp, &a.TotalPositions, &a.ProtectedPositions,
			&a.UnprotectedPositions, &a.OrphanOrders, &a.Status, &a.Notes, &a.CriticalFindings, &a.WarningFindings); err != nil {
			return nil, fmt.Errorf("scan audit: %w", err)
		}
		st.Audits = append(st.Audits, a)
	}
	if err := arows.Err(); err != nil {
		return nil, err
	}

	frows, err := db.Query(`
		SELECT f.id, f.run_at, f.account_number, f.check_name, f.category, f.severity, IFNULL(f.subject, ''), f.detail
		FROM check_health_finding f
		WHERE f.run_at = (SELECT MAX(x.run_at) FROM check_health_finding x WHERE x.account_number = f.account_number)
		ORDER BY CASE f.severity WHEN 'CRITICAL' THEN 0 ELSE 1 END, f.account_number, f.check_name, f.subject
		LIMIT 200`)
	if err != nil {
		return nil, fmt.Errorf("read findings: %w", err)
	}
	defer frows.Close()
	for frows.Next() {
		var f Finding
		if err := frows.Scan(&f.ID, &f.RunAt, &f.AccountNumber, &f.CheckName, &f.Category, &f.Severity, &f.Subject, &f.Detail); err != nil {
			return nil, fmt.Errorf("scan finding: %w", err)
		}
		st.Findings = append(st.Findings, f)
	}
	return st, frows.Err()
}
