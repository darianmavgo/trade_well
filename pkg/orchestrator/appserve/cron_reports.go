package appserve

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/check_health"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/ready"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/risk"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/send_email"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/weekly_digest"
)

func handleCronCheckHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Minute)
	defer cancel()

	// Which scheduled job did not do its job. Read from the orchestrator database (Turso) only,
	// so it is reported even when the broker, the token or the account list is
	// what is broken.
	pipeRows, pipeFindings := pipelineDiagnosis()

	cfg, db, tc, live, err := jobDepsResolved(ctx)
	if err != nil {
		writeJobErr(w, "check_health", fmt.Errorf("%w\n\n%s", err, check_health.FormatPipeline(pipeFindings)))
		return
	}
	defer db.Close()
	_, stratsByID, err := resolveStrategies(db, cfg)
	if err != nil {
		writeJobErr(w, "check_health", err)
		return
	}
	allRows := check_health.EmailRows(pipeRows)
	var positionLines []string
	for _, acc := range live {
		res, err := check_health.Run(ctx, db, tc, acc, stratsByID)
		if err != nil {
			writeJobErr(w, "check_health", err)
			return
		}
		for _, pa := range res.PositionAudits {
			positionLines = append(positionLines, fmt.Sprintf("  %-8s %-8s %-6s %s", pa.Status, acc.AccountNumber, pa.Symbol, pa.Summary()))
		}
		for _, row := range res.Rows() {
			allRows = append(allRows, send_email.Row{
				Account: row.Account, Strategy: row.Strategy, Symbol: row.Symbol,
				Signal: row.Signal, Status: row.Status, Detail: row.Detail,
			})
		}
		for _, rec := range res.Recommendations {
			log.Printf("[appserve] check_health %s: %s", acc.AccountNumber, rec)
		}
		log.Printf("[appserve] check_health %s: status=%s positions=%d unprotected=%d orphan_orders=%d critical=%d warning=%d",
			acc.AccountNumber, res.OverallStatus, res.TotalPositions, res.UnprotectedPositions, res.OrphanOrders, res.CriticalFindings, res.WarningFindings)
	}

	subject, body := send_email.BuildJobReport("check_health", allRows)
	if mailErr := send_email.SendHTMLEmailWithAttachments(db, "check_health", subject, body); mailErr != nil {
		log.Printf("[appserve] check_health report email not sent: %v", mailErr)
	} else {
		log.Printf("[appserve] check_health report email sent (accounts=%d)", len(live))
	}

	fmt.Fprintf(w, "OK check_health accounts=%d\n", len(live))
	for _, line := range positionLines {
		fmt.Fprintln(w, line)
	}
}

// pipelineDiagnosis opens the database and runs check_health.PipelineRows. A
// failure to do so is logged and yields no rows: the diagnosis is an addition
// to the health report and must never stop it.
func pipelineDiagnosis() ([]check_health.Row, []check_health.Finding) {
	cfg, err := appconfig.Load()
	if err != nil {
		log.Printf("[check_health] pipeline diagnosis skipped: config: %v", err)
		return nil, nil
	}
	db, err := openAndEnsureSchemas(cfg.DBPath)
	if err != nil {
		log.Printf("[check_health] pipeline diagnosis skipped: open db: %v", err)
		return nil, nil
	}
	defer db.Close()
	rows, findings, err := check_health.PipelineRows(db, time.Now().UTC())
	if err != nil {
		log.Printf("[check_health] pipeline diagnosis error: %v", err)
	}
	for _, f := range findings {
		log.Printf("[check_health] PIPELINE %s %s: %s", f.CheckName, f.Subject, f.Detail)
	}
	if len(findings) == 0 && err == nil {
		log.Printf("[check_health] pipeline ok: every scheduled job recorded a run for its last slot")
	}
	return rows, findings
}

// handleCronRisk is /cron/risk: record each account's equity and set what new entries may do.
// It reads the account_state mirror, so it makes no broker call. ?dry_run=1 writes and sends nothing;
// ?reset_peak=1 restarts the high-water mark (after a withdrawal).
func handleCronRisk(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	cfg, err := appconfig.Load()
	if err != nil {
		writeJobErr(w, risk.JobName, err)
		return
	}
	db, err := openAndEnsureSchemas(cfg.DBPath)
	if err != nil {
		writeJobErr(w, risk.JobName, err)
		return
	}
	defer db.Close()
	q := r.URL.Query()
	if _, err := risk.RunJob(ctx, db, w, risk.JobOptions{
		Limits:    cfg.Risk,
		DryRun:    q.Get("dry_run") == "1",
		Email:     q.Get("dry_run") != "1",
		ResetPeak: q.Get("reset_peak") == "1",
	}); err != nil {
		writeJobErr(w, risk.JobName, err)
	}
}

// handleCronReady is /cron/ready: the readiness checks, emailed only when something is not ok.
// ?dry_run=1 sends nothing.
func handleCronReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	cfg, err := appconfig.Load()
	if err != nil {
		writeJobErr(w, ready.JobName, err)
		return
	}
	db, err := openAndEnsureSchemas(cfg.DBPath)
	if err != nil {
		writeJobErr(w, ready.JobName, err)
		return
	}
	defer db.Close()
	_, err = ready.RunJob(ctx, db, w, ready.JobOptions{
		Options: ready.Options{Allowlist: cfg.StrategyAllowlist, MarketDB: cfg.MarketDB, TokenPath: cfg.Schwab.TokenPath},
		Email:   r.URL.Query().Get("dry_run") != "1",
		// The email is the /ready page itself, so the two cannot drift apart.
		RenderHTML: func(rep ready.Report) (string, error) { return readyEmailHTML(ctx, rep) },
	})
	if err != nil {
		log.Printf("[appserve] ready: %v", err)
	}
}

// handleCronWeeklyDigest is /cron/weekly_digest: the Saturday summary email. ?dry_run=1 sends nothing.
func handleCronWeeklyDigest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	cfg, err := appconfig.Load()
	if err != nil {
		writeJobErr(w, weekly_digest.JobName, err)
		return
	}
	db, err := openAndEnsureSchemas(cfg.DBPath)
	if err != nil {
		writeJobErr(w, weekly_digest.JobName, err)
		return
	}
	defer db.Close()
	if _, err := weekly_digest.RunJob(ctx, db, w, weekly_digest.JobOptions{
		Ready: ready.Options{Allowlist: cfg.StrategyAllowlist, MarketDB: cfg.MarketDB, TokenPath: cfg.Schwab.TokenPath},
		Email: r.URL.Query().Get("dry_run") != "1",
	}); err != nil {
		writeJobErr(w, weekly_digest.JobName, err)
	}
}
