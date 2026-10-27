package appserve

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_allowlist"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/killswitch"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/runtimeenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/send_email"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// registerCron mounts /cron/* using the same package entrypoints the CLI uses
// with --live. No second binary exec — local CLI predicts GAE behavior.
//
// Route names, job identifiers (log/email), and CLI subcommand names
// all match the pkg/ they dispatch into (e.g. /cron/scan_market ->
// pkg/scan_market, job identifier "scan_market"). deploy_gae/cron.yaml only
// schedules these current names; every pre-rename alias and job identifier
// has been removed entirely -- no fallback routes are mounted.
func registerCron(mux *http.ServeMux) {
	for _, j := range cronJobs {
		handler := j.handler
		// trigger_limits runs every few minutes and usually finds nothing to do: it
		// records a run itself, only when it placed or cancelled an order.
		if j.name != "trigger_limits" {
			handler = recordRun(j.name, handler)
		}
		mux.HandleFunc("/cron/"+j.name, cronOnly(handler))
	}
}

// cronJobs is the one list of jobs served under /cron/<name>, in pipeline
// order. `orchestrator cron <name>` (pkg/cronclient) triggers the same names.
var cronJobs = []struct {
	name    string
	handler http.HandlerFunc
}{
	{"account_state", handleCronAccountState},
	{"risk", handleCronRisk},
	{"scan_market", handleCronScanMarket},
	{"stage_orders", handleCronStageOrders},
	{"submit_orders", handleCronSubmitOrders},
	{"trigger_limits", handleCronTriggerLimits},
	{"manage_exits", handleCronManageExits},
	{"check_health", handleCronCheckHealth},
	{"ready", handleCronReady},
	{"weekly_digest", handleCronWeeklyDigest},
}

// CronJobNames lists every job that has a /cron/<name> route.
func CronJobNames() []string {
	names := make([]string, len(cronJobs))
	for i, j := range cronJobs {
		names[i] = j.name
	}
	return names
}

// cronOnly admits App Engine cron, or (when CRON_ALLOWED_EMAILS is set) an
// allowlisted Google account presenting an ID token. See authorizeCron.
func cronOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, ok, reason := authorizeCron(r)
		if !ok {
			log.Printf("[appserve] cron %s refused: %s", r.URL.Path, reason)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		job := strings.TrimPrefix(r.URL.Path, "/cron/")
		// The killswitch turns cron off: refuse before anything starts (no
		// job_run row, no broker call).
		if st := currentKillswitch(); st.Engaged {
			log.Printf("[appserve] cron %s by %s REFUSED: %s", r.URL.Path, caller, st)
			http.Error(w, st.String()+" -- /cron/"+job+" did not run", http.StatusServiceUnavailable)
			return
		}
		// Run inside a context the killswitch can cancel, so engaging it also stops a
		// job that is already running, mid broker call.
		ctx, done := killswitch.Track(r.Context(), job)
		defer done()
		r = r.WithContext(ctx)
		log.Printf("[appserve] cron %s by %s %s", r.URL.Path, caller, runtimeenv.Detect())
		defer func() {
			if rec := recover(); rec != nil {
				writeJobErr(w, strings.TrimPrefix(r.URL.Path, "/cron/"), fmt.Errorf("panic: %v", rec))
			}
		}()
		next(w, r)
	}
}

// resolveStrategies resolves cfg.StrategyAllowlist into both the slice form
// (manage_exits) and the map form (check_health), the concrete backtestgosqlite
// injection point — no package below appserve calls strategy.Get itself.
func resolveStrategies(db *sql.DB, cfg *appconfig.Config) ([]strategy.Strategy, map[string]strategy.Strategy, error) {
	strats, err := strategies.ResolveGreenlit(db, cfg.StrategyAllowlist)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]strategy.Strategy, len(strats))
	for _, s := range strats {
		byID[s.ID()] = s
	}
	return strats, byID, nil
}

func jobDeps(ctx context.Context) (*appconfig.Config, *sql.DB, *trader.TraderClient, []string, error) {
	cfg, err := appconfig.Load()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	targets, err := account_allowlist.FromEnv()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	db, err := openAndEnsureSchemas(cfg.DBPath)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	tc, err := trader.NewTraderClient(ctx, cfg.Schwab)
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, nil, err
	}
	return cfg, db, tc, targets, nil
}

func jobDepsResolved(ctx context.Context) (*appconfig.Config, *sql.DB, *trader.TraderClient, []*trader.ResolvedAccount, error) {
	cfg, db, tc, targets, err := jobDeps(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	live, err := tc.GetAccounts(ctx, targets)
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, nil, fmt.Errorf("resolve accounts: %w", err)
	}
	return cfg, db, tc, live, nil
}

// failureSubject is the subject of a failed cron job's email; inbox filters match "<job> FAILED".
func failureSubject(job string) string { return job + " FAILED" }

// failureEmailBody is the HTML for a failed cron job: the same report template every job's
// own email uses (send_email.BuildJobReportSummary), with the failure as its one red row, so a
// failure reads like the job's normal report. The subject is built by writeJobErr and stays
// "<job> FAILED" for inbox filters.
func failureEmailBody(job string, err error, at time.Time) string {
	_, body := send_email.BuildJobReportSummary(job, send_email.Summary{Lines: []string{
		"GAE /cron/" + job + " failed.",
		"Time: " + at.UTC().Format(time.RFC3339),
	}}, []send_email.Row{{Strategy: job, Signal: "cron", Status: "FAILED", Detail: err.Error()}})
	return body
}

// writeJobErr fails a cron request: it logs, emails the operator (every job,
// not just account_state, so a silent failure at 18:00 or 09:28 cannot go
// unnoticed), and returns HTTP 500 so App Engine cron records the failure too.
func writeJobErr(w http.ResponseWriter, label string, err error) {
	log.Printf("[appserve] %s: %v", label, err)
	if mailErr := send_email.SendHTMLEmail(nil, label, failureSubject(label), failureEmailBody(label, err, time.Now())); mailErr != nil {
		log.Printf("[appserve] %s failure email not sent: %v", label, mailErr)
	}
	http.Error(w, label+": "+err.Error(), http.StatusInternalServerError)
}
