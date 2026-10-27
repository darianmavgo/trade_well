package risk

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/killswitch"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/send_email"
)

// staleAfter is how old the account mirror may be before the risk job refuses to treat it as
// today's equity: a sync that has been failing must not pass for a flat day.
const staleAfter = 4 * 24 * time.Hour

// JobOptions are the inputs to RunJob.
type JobOptions struct {
	Limits Limits
	// DryRun computes and prints the assessment and writes nothing.
	DryRun bool
	// Email sends a message when an account's entry scale changed (tightened or relaxed).
	Email bool
	// ResetPeak forgets the equity history before today, so the high-water mark restarts at
	// the current equity. Use it after a withdrawal, which otherwise looks like a drawdown.
	ResetPeak bool
	// Now overrides the clock in tests.
	Now time.Time
}

// Result is what RunJob found.
type Result struct {
	States  []State
	Changed []State // accounts whose entry scale differs from the previous assessment
	// AutoKill is why the killswitch was engaged (or, in a dry run, would be); "" when not.
	AutoKill string
}

// RunJob assesses every account in the account_state mirror: it records the session's equity,
// measures drawdown and the change since the last session, saves the state the other jobs read,
// and prints one line per account (the first line is the outcome job_run keeps).
func RunJob(ctx context.Context, db *sql.DB, out io.Writer, o JobOptions) (*Result, error) {
	if db == nil {
		return nil, fmt.Errorf("risk: nil db")
	}
	if err := EnsureSchema(db); err != nil {
		return nil, err
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	session := now.In(job_runs.EasternTime()).Format("2006-01-02")

	rows, err := db.QueryContext(ctx, orchestratorsql.Q("risk/run_job"))
	if err != nil {
		return nil, fmt.Errorf("risk: read accounts (has account_state run?): %w", err)
	}
	type acct struct {
		num     string
		equity  float64
		fetched time.Time
	}
	var accts []acct
	for rows.Next() {
		var a acct
		if err := rows.Scan(&a.num, &a.equity, &a.fetched); err != nil {
			rows.Close()
			return nil, err
		}
		accts = append(accts, a)
	}
	rows.Close()

	res := &Result{}
	var lines []string
	var problems []string
	for _, a := range accts {
		if now.Sub(a.fetched) > staleAfter {
			problems = append(problems, fmt.Sprintf("%s: account mirror is from %s, too old to assess", a.num, a.fetched.Format("2006-01-02")))
			continue
		}
		if a.equity <= 0 {
			// An empty account (the cash account) has nothing to protect and no drawdown to measure.
			lines = append(lines, fmt.Sprintf("  %s: no equity, skipped", a.num))
			continue
		}
		before, err := Load(db, a.num)
		if err != nil {
			return res, err
		}
		if o.ResetPeak && !o.DryRun {
			if _, err := db.Exec(orchestratorsql.Q("risk/run_job_2"), a.num, session); err != nil {
				return res, err
			}
		}
		var prev, peak sql.NullFloat64
		_ = db.QueryRow(orchestratorsql.Q("risk/run_job_3"), a.num, session).Scan(&prev)
		_ = db.QueryRow(orchestratorsql.Q("risk/run_job_4"), a.num, session).Scan(&peak)
		var long float64
		_ = db.QueryRow(orchestratorsql.Q("risk/run_job_5"), a.num).Scan(&long)

		s := Assess(o.Limits, a.equity, peak.Float64, prev.Float64, long)
		s.Account, s.AssessedAt, s.Session = a.num, now.UTC(), session
		if !o.DryRun {
			if _, err := db.Exec(orchestratorsql.Q("risk/run_job_6"),
				a.num, session, a.equity, now.UTC()); err != nil {
				return res, err
			}
			if err := save(db, s); err != nil {
				return res, err
			}
		}
		res.States = append(res.States, s)
		if before.AssessedAt.IsZero() && s.Scale == 1 {
			// first assessment at full size: nothing to announce
		} else if before.Scale != s.Scale || before.HaltEntries != s.HaltEntries {
			res.Changed = append(res.Changed, s)
		}
		lines = append(lines, fmt.Sprintf("  %s: equity $%.2f, peak $%.2f, drawdown %.1f%%, day %+.1f%%, long %.0f%% of equity -- %s",
			a.num, s.Equity, s.Peak, s.Drawdown*100, s.DayChange*100, s.Gross*100, s))
	}

	fmt.Fprintf(out, "OK %s accounts=%d changed=%d%s\n", JobName, len(res.States), len(res.Changed), map[bool]string{true: " dry_run", false: ""}[o.DryRun])
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
	for _, p := range problems {
		fmt.Fprintln(out, "  WARN "+p)
	}

	reason, err := AutoKillReason(db, o.Limits, res.States)
	if err != nil {
		return res, err
	}
	if reason != "" {
		res.AutoKill = reason
		cur, cerr := killswitch.Current(db)
		switch {
		case cerr != nil:
			return res, fmt.Errorf("risk: read killswitch: %w", cerr)
		case cur.Engaged:
			fmt.Fprintf(out, "  AUTO-KILL condition holds (%s); killswitch already engaged\n", reason)
		case o.DryRun:
			fmt.Fprintf(out, "  AUTO-KILL would engage the killswitch: %s\n", reason)
		default:
			if _, err := killswitch.Set(db, true, "auto: risk", reason, now); err != nil {
				return res, fmt.Errorf("risk: engage killswitch: %w", err)
			}
			fmt.Fprintf(out, "  AUTO-KILL engaged the killswitch: %s\n", reason)
			if o.Email {
				body := "The killswitch was engaged automatically: " + reason + "\n\n" +
					"Every cron job is now refused, exits included. No position was sold.\n" +
					"To sell everything: orchestrator exit_market (--dry-run first).\n" +
					"To resume once you have looked: orchestrator killswitch off.\n"
				if err := send_email.SendEmail(db, JobName, "risk: killswitch ENGAGED automatically", body); err != nil {
					return res, fmt.Errorf("auto-kill not emailed (the killswitch is engaged): %w", err)
				}
			}
		}
	}

	if o.Email && !o.DryRun && len(res.Changed) > 0 {
		var b strings.Builder
		for _, s := range res.Changed {
			fmt.Fprintf(&b, "%s: %s\n  equity $%.2f, peak $%.2f, drawdown %.1f%%, day %+.1f%%\n", s.Account, s, s.Equity, s.Peak, s.Drawdown*100, s.DayChange*100)
		}
		b.WriteString("\nOnly new entries are limited. Exits and stops keep running.\n")
		if err := send_email.SendEmail(db, JobName, "risk: entry size changed", b.String()); err != nil {
			return res, fmt.Errorf("risk change not emailed: %w", err)
		}
	}
	return res, nil
}
