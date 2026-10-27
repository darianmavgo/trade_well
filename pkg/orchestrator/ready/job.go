package ready

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/send_email"
)

// Text renders the report as plain lines: one per check, worst first within the same order.
func Text(rep Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ready: %s at %s\n", strings.ToUpper(rep.Status), rep.GeneratedAt.Format("2006-01-02 15:04 MST"))
	for _, c := range rep.Checks {
		fmt.Fprintf(&b, "  %-4s %-15s %s\n", strings.ToUpper(c.Status), c.Name, c.Detail)
	}
	return b.String()
}

// JobOptions are the inputs of RunJob.
type JobOptions struct {
	Options
	// Email sends the report to the operator when anything is not ok. A clean run sends nothing,
	// so a message from this job always means something needs a look.
	Email bool
	// RenderHTML turns the report into the HTML body of that email. The operator pages live in a
	// higher layer (appserve) than this package, so the caller passes the renderer in. Nil, or a
	// render error, sends the plain-text report instead.
	RenderHTML func(Report) (string, error)
}

// RunJob is the `ready` job: run the checks, print the report to out, and email it when
// something is wrong. The first line of out is the one-line outcome the job_run row keeps.
func RunJob(ctx context.Context, db *sql.DB, out io.Writer, o JobOptions) (Report, error) {
	o.DB = db
	rep := Run(ctx, o.Options)
	fmt.Fprint(out, "OK "+Text(rep))
	if o.Email && rep.Status != OK {
		subject := "ready: " + strings.ToUpper(rep.Status)
		send := func() error { return send_email.SendEmail(db, JobName, subject, Text(rep)) }
		if o.RenderHTML != nil {
			if body, err := o.RenderHTML(rep); err == nil {
				send = func() error { return send_email.SendHTMLEmail(db, JobName, subject, body) }
			} else {
				fmt.Fprintf(out, "  WARN html email not rendered, sending text: %v\n", err)
			}
		}
		if err := send(); err != nil {
			return rep, fmt.Errorf("ready report not emailed: %w", err)
		}
	}
	return rep, nil
}
