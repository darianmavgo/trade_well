package cronclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
)

// CommandOptions are the inputs to the `orchestrator cron` command.
type CommandOptions struct {
	BaseURL string   // deployed service base URL; "" = $ORCHESTRATOR_URL or the project default
	DryRun  bool     // ask for a preview (?dry_run=1; only stage_orders honors it)
	Args    []string // positional args: exactly one job name, or "list"/"help"
	Jobs    []string // every job served under /cron/<job> (appserve.CronJobNames)
}

// RunCommand triggers a job on the deployed service through its /cron/<job>
// route, authenticated as you (a Google ID token; the server must list your
// email in CRON_ALLOWED_EMAILS). Job names are exactly the ones cron.yaml
// schedules and the local subcommands use. Failures the user has already been
// told about return jobenv.ErrReported.
func RunCommand(ctx context.Context, out io.Writer, opts CommandOptions) error {
	pos := opts.Args
	if len(pos) != 1 || pos[0] == "list" || pos[0] == "help" {
		fmt.Fprintf(out, "%sUsage:%s orchestrator cron <job> [--url URL] [--dry-run]\n\nJobs (same names as the local subcommands):\n", jobenv.ColYellow, jobenv.ColReset)
		for _, j := range opts.Jobs {
			fmt.Fprintf(out, "  %s\n", j)
		}
		fmt.Fprintln(out, "\nNeeds CRON_ALLOWED_EMAILS on the server to include your Google email, and either")
		fmt.Fprintln(out, "`gcloud auth login` here or CRON_ID_TOKEN set. Equivalent to:")
		fmt.Fprintln(out, `  curl -H "Authorization: Bearer $(gcloud auth print-identity-token)" https://<project>.uc.r.appspot.com/cron/<job>`)
		if len(pos) == 1 {
			return nil
		}
		return jobenv.ErrReported
	}
	job := pos[0]
	if !slices.Contains(opts.Jobs, job) {
		return fmt.Errorf("unknown job %q (jobs: %s)", job, strings.Join(opts.Jobs, ", "))
	}

	trigger := Options{BaseURL: opts.BaseURL}
	if opts.DryRun {
		trigger.Query = url.Values{"dry_run": {"1"}}
	}
	fmt.Fprintf(out, "Triggering %s on the deployed service...\n", job)
	res, err := Trigger(ctx, job, trigger)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "HTTP %d\n%s\n", res.Status, res.Body)
	if !res.OK() {
		if res.Status == http.StatusForbidden {
			fmt.Fprintf(out, "%s[Hint]%s 403: the server refused this identity. Check CRON_ALLOWED_EMAILS on the service includes your Google email.\n", jobenv.ColYellow, jobenv.ColReset)
		}
		return jobenv.ErrReported
	}
	return nil
}
