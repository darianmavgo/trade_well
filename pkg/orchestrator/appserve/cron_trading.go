package appserve

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_state"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/send_email"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/submit_orders"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/trigger_limits"
)

func handleCronAccountState(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Minute)
	defer cancel()

	const maxAttempts = 3
	var lastErr error
	var targets []string
	var res *account_state.Result

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		cfg, db, tc, tgts, err := jobDeps(ctx)
		if err != nil {
			lastErr = err
			log.Printf("[appserve] account_state attempt %d/%d deps: %v", attempt, maxAttempts, err)
			if attempt < maxAttempts {
				time.Sleep(time.Duration(attempt) * 2 * time.Second)
			}
			continue
		}
		targets = tgts
		_ = cfg
		res, err = account_state.Sync(ctx, db, tc, targets)
		_ = db.Close()
		if err == nil {
			fmt.Fprintf(w, "OK account_state accounts=%d attempts=%d\n", len(targets), attempt)
			if res != nil {
				fmt.Fprintf(w, "%+v\n", res)
			}
			return
		}
		lastErr = err
		log.Printf("[appserve] account_state attempt %d/%d failed: %v", attempt, maxAttempts, err)
		// schwab.Client.ensureFreshToken already pulls the freshest GCS token
		// and reloads from disk before every call (see schwaber/pkg/tokensync),
		// so a dead refresh token means real re-auth is needed — no reload
		// special-case here, just the ordinary backoff/retry for transient errors.
		if attempt < maxAttempts {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
	}

	writeJobErr(w, "account_state", fmt.Errorf("after %d attempts: %w", maxAttempts, lastErr))
}

func handleCronScanMarket(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Minute)
	defer cancel()
	cfg, err := appconfig.Load()
	if err != nil {
		writeJobErr(w, "scan_market", err)
		return
	}
	db, err := openAndEnsureSchemas(cfg.DBPath)
	if err != nil {
		writeJobErr(w, "scan_market", err)
		return
	}
	defer db.Close()

	// Schwab-free: livescan → scan_market_buy_signal only. No account
	// resolution, no stage_order. Staging happens later after dashboard review.
	// Scan every entry of STRATEGY_ALLOWLIST (stacks and single strategies alike),
	// as the CLI does.
	strategyIDs, err := strategies.ResolveRequest("", "")
	if err != nil {
		writeJobErr(w, "scan_market", err)
		return
	}
	members, err := strategies.ResolveGreenlit(db, strategyIDs)
	if err != nil {
		writeJobErr(w, "scan_market", err)
		return
	}
	membersByID := make(map[string]strategy.Strategy, len(members))
	for _, s := range members {
		membersByID[s.ID()] = s
	}
	result, err := scan_market.RunSignalScan(ctx, db, cfg, strategyIDs, membersByID)
	if err != nil {
		writeJobErr(w, "scan_market", err)
		return
	}
	for _, d := range result.Details {
		log.Printf("[appserve] scan_market: %s", d)
	}

	// One row per scanned strategy -- BUY or NO_BUY with the reason -- not only the ones that fired.
	allRows := scan_market.EmailRows(result.Strategies)
	buys := 0
	for _, st := range result.Strategies {
		if st.Status == scan_market.ScanBuy {
			buys++
		}
	}
	// ?dry_run=1 scans and records signals but sends no report email (the live_local test).
	if r.URL.Query().Get("dry_run") == "1" {
		log.Printf("[appserve] scan_market dry_run: report email not sent")
	} else {
		subject, body := send_email.BuildJobReportSummary("scan_market", send_email.Summary{
			SubjectSuffix: fmt.Sprintf("%d of %d strategies BUY, bars through %s", buys, len(result.Strategies), result.AsOf),
			Lines:         []string{fmt.Sprintf("Scanned %d strategies on daily bars through %s for the %s session; %d BUY signal(s) recorded.", len(result.Strategies), result.AsOf, result.NextSession, result.BuySignalsWritten)},
		}, allRows)
		if mailErr := send_email.SendHTMLEmail(db, "scan_market", subject, body); mailErr != nil {
			log.Printf("[appserve] scan_market report email not sent: %v", mailErr)
		} else {
			log.Printf("[appserve] scan_market report email sent")
		}
	}

	fmt.Fprintf(w, "OK scan_market run=%s as_of=%s next=%s members=%d livescan_signals=%d buy_signals=%d strategies=%s\n",
		result.ScanRunID, result.AsOf, result.NextSession, result.MembersEvaluated,
		result.LivescanSignals, result.BuySignalsWritten, strings.Join(strategyIDs, ","))
}

// handleCronStageOrders is the stage_orders job: it turns the buy signals
// scan_market wrote into sized stage_order rows for submit_orders. Add
// ?dry_run=1 to size and report without writing (the CLI's --dry-run).
func handleCronStageOrders(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Minute)
	defer cancel()
	cfg, db, _, live, err := jobDepsResolved(ctx)
	if err != nil {
		writeJobErr(w, "stage_orders", err)
		return
	}
	defer db.Close()
	strats, byID, err := resolveStrategies(db, cfg)
	if err != nil {
		writeJobErr(w, "stage_orders", err)
		return
	}
	_ = strats
	dryRun := r.URL.Query().Get("dry_run") == "1"
	res, err := stage_orders.RunJob(ctx, db, live, stage_orders.JobOptions{DryRun: dryRun, Members: byID})
	if err != nil {
		writeJobErr(w, "stage_orders", err)
		return
	}
	for _, d := range res.Details {
		log.Printf("[appserve] stage_orders: %s", d)
	}
	if !dryRun {
		rows := make([]send_email.Row, len(res.Rows))
		for i, row := range res.Rows {
			rows[i] = send_email.Row{Account: row.Account, Strategy: row.Strategy, Symbol: row.Symbol, Signal: row.Signal, Status: row.Status, Detail: row.Detail}
		}
		subject, body := send_email.BuildJobReport("stage_orders", rows)
		if mailErr := send_email.SendHTMLEmail(db, "stage_orders", subject, body); mailErr != nil {
			log.Printf("[appserve] stage_orders report email not sent: %v", mailErr)
		}
	}
	fmt.Fprintf(w, "OK stage_orders dry_run=%v accounts=%d signals=%d staged=%d dismissed=%d\n",
		dryRun, res.Accounts, res.Signals, res.Staged, res.Dismissed)
}

func handleCronSubmitOrders(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Minute)
	defer cancel()
	_, db, tc, live, err := jobDepsResolved(ctx)
	if err != nil {
		writeJobErr(w, "submit_orders", err)
		return
	}
	defer db.Close()

	res, err := submit_orders.RunJob(ctx, db, tc, live, submit_orders.JobOptions{})
	if res == nil {
		writeJobErr(w, "submit_orders", err)
		return
	}
	summary := fmt.Sprintf("submit_orders accounts=%d found=%d submitted=%d filled=%d pending_fill=%d rejected=%d unresolved=%d errors=%d",
		res.Accounts, res.StagedOrdersFound, res.Submitted, res.Filled, res.PendingFill, res.Rejected, res.Unresolved, len(res.Errors))
	if err != nil {
		// RunJob already logged, recorded the run and emailed the report with
		// the errors in it; only the HTTP status is left, so App Engine cron
		// records the failure without a second email.
		http.Error(w, "FAILED "+summary+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, "OK %s\n", summary)
}

// handleCronTriggerLimits places the limit entries stage_orders held back (WATCHING)
// whose limit the day's low has reached, then submits them, and cancels at the close the
// ones never reached. It runs every five minutes through the session. Add ?dry_run=1 to
// report without placing or cancelling anything.
func handleCronTriggerLimits(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	cfg, db, tc, targets, err := jobDeps(ctx)
	if err != nil {
		writeJobErr(w, "trigger_limits", err)
		return
	}
	defer db.Close()
	// Most passes have nothing to do: look before calling the broker.
	any, err := trigger_limits.AnyWatching(db)
	if err != nil {
		writeJobErr(w, "trigger_limits", err)
		return
	}
	if !any {
		fmt.Fprintln(w, "OK trigger_limits watching=0")
		return
	}
	live, err := tc.GetAccounts(ctx, targets)
	if err != nil {
		writeJobErr(w, "trigger_limits", fmt.Errorf("resolve accounts: %w", err))
		return
	}
	_, byID, err := resolveStrategies(db, cfg)
	if err != nil {
		writeJobErr(w, "trigger_limits", err)
		return
	}
	dryRun := r.URL.Query().Get("dry_run") == "1"
	res, err := trigger_limits.RunJob(ctx, db, tc, live, trigger_limits.Options{DryRun: dryRun, Members: byID, Quotes: trigger_limits.SchwabQuotes(tc)})
	if res != nil {
		for _, d := range res.Details {
			log.Printf("[appserve] trigger_limits: %s", d)
		}
	}
	if res == nil {
		writeJobErr(w, "trigger_limits", err)
		return
	}
	summary := res.Summary(dryRun)
	if err != nil {
		// submit_orders (called by the job) has already emailed its own report.
		http.Error(w, "FAILED "+summary+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, "OK %s\n", summary)
}

func handleCronManageExits(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Minute)
	defer cancel()
	cfg, db, tc, live, err := jobDepsResolved(ctx)
	if err != nil {
		writeJobErr(w, "manage_exits", err)
		return
	}
	defer db.Close()
	strats, _, err := resolveStrategies(db, cfg)
	if err != nil {
		writeJobErr(w, "manage_exits", err)
		return
	}
	// ?dry_run=1 previews without selling or writing (the CLI's --dry-run).
	dryRun := r.URL.Query().Get("dry_run") == "1"
	var allRows []send_email.Row
	for _, acc := range live {
		res, err := manage_exits.Run(ctx, db, tc, acc, strats, time.Now().UTC(), dryRun)
		if err != nil {
			writeJobErr(w, "manage_exits", err)
			return
		}
		for _, row := range res.Rows {
			allRows = append(allRows, send_email.Row{
				Account: row.Account, Strategy: row.Strategy, Symbol: row.Symbol,
				Signal: row.Signal, Status: row.Status, Detail: row.Detail,
			})
		}
		for _, d := range res.Details {
			log.Printf("[appserve] manage_exits %s: %s", acc.AccountNumber, d)
		}
		log.Printf("[appserve] manage_exits %s: checked=%d closed_profit=%d closed_expiry=%d active=%d no_position=%d",
			acc.AccountNumber, res.TotalChecked, res.ClosedProfit, res.ClosedTimeExpiry, res.StillActive, res.SkippedNoPosition)
	}

	if dryRun {
		fmt.Fprintf(w, "OK manage_exits dry_run=true accounts=%d\n", len(live))
		for _, row := range allRows {
			fmt.Fprintf(w, "  %-9s %-8s %-6s %-14s %s\n", row.Status, row.Account, row.Symbol, row.Signal, row.Detail)
		}
		return
	}

	subject, body := send_email.BuildJobReport("manage_exits", allRows)
	if mailErr := send_email.SendHTMLEmail(db, "manage_exits", subject, body); mailErr != nil {
		log.Printf("[appserve] manage_exits report email not sent: %v", mailErr)
	} else {
		log.Printf("[appserve] manage_exits report email sent")
	}

	fmt.Fprintf(w, "OK manage_exits accounts=%d\n", len(live))
}
