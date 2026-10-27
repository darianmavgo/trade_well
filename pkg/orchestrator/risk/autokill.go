package risk

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
)

// autoKillJobs are the jobs whose repeated failure engages the killswitch: the two that place
// orders. A flaky reporting job must not switch trading off.
var autoKillJobs = []string{"stage_orders", "submit_orders"}

// AutoKillReason says why the killswitch should engage on its own, or "" when it should not.
// Two triggers, both set loose so that only something badly wrong fires them:
//
//   - an account's drawdown from its high-water mark reaches Limits.AutoKillDrawdown (the
//     ladder has by then long since halted new entries, so this is the "stop everything and
//     look" level; it does not sell anything), or
//   - stage_orders or submit_orders has failed Limits.AutoKillFailures runs in a row.
//
// The killswitch refuses every cron job, exits included, so it is a human-in-the-loop stop:
// the email says which trigger fired and how to turn it off again.
func AutoKillReason(db *sql.DB, l Limits, states []State) (string, error) {
	if l.AutoKillDrawdown > 0 {
		for _, s := range states {
			if s.Drawdown >= l.AutoKillDrawdown-eps {
				return fmt.Sprintf("account %s is %.1f%% below its high-water mark (auto-kill level %.0f%%)", s.Account, s.Drawdown*100, l.AutoKillDrawdown*100), nil
			}
		}
	}
	if n := l.AutoKillFailures; n > 0 {
		if err := job_runs.EnsureSchema(db); err != nil {
			return "", err
		}
		for _, job := range autoKillJobs {
			rows, err := db.Query(`SELECT status FROM job_run WHERE job = ? ORDER BY id DESC LIMIT ?`, job, n)
			if err != nil {
				return "", fmt.Errorf("risk: read %s runs: %w", job, err)
			}
			var statuses []string
			for rows.Next() {
				var st string
				if err := rows.Scan(&st); err != nil {
					rows.Close()
					return "", err
				}
				statuses = append(statuses, st)
			}
			rows.Close()
			if len(statuses) == n && strings.Count(strings.Join(statuses, ","), "FAILED") == n {
				return fmt.Sprintf("%s failed its last %d runs in a row", job, n), nil
			}
		}
	}
	return "", nil
}
