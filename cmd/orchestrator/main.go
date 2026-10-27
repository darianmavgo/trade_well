package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appserve"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/bootstrap"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_report"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/runtimeenv"
	"github.com/darianmavgo/trade_well/pkg/schwaber/config"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// ANSI colors (shared with pkg/ job output via jobenv).
const (
	colReset  = jobenv.ColReset
	colRed    = jobenv.ColRed
	colGreen  = jobenv.ColGreen
	colYellow = jobenv.ColYellow
	colBlue   = jobenv.ColBlue
	colCyan   = jobenv.ColCyan
	colBold   = jobenv.ColBold
	colDim    = jobenv.ColDim
)

func main() {
	info := runtimeenv.Detect()
	if info.IsCloud() {
		appenv.IgnoreDotEnv() // a .env is for local runs; nothing on App Engine may read one
	}

	// Configuration comes from one place: gs://$GCS_BUCKET/config/env. Fill the
	// environment before anything reads it (no Secret Manager, app.yaml env or .env).
	if len(os.Args) >= 2 && os.Args[1] != "help" && os.Args[1] != "-h" && os.Args[1] != "--help" || info.ShouldAutoServe() {
		set, err := config.Bootstrap(context.Background())
		if err != nil {
			log.Fatalf("[orchestrator] %v", err)
		}
		log.Printf("[orchestrator] config: %d variable(s) from gs://%s/%s", len(set), config.Bucket(), config.ConfigObject)
	}

	// No args on GAE → same HTTP surface as former cmd/gae.
	if len(os.Args) < 2 {
		if info.ShouldAutoServe() {
			if info.Service == live_vs_backtest_report.ServiceName {
				log.Printf("[orchestrator] no args + %s → reconcile report", info)
				if err := live_vs_backtest_report.Serve(context.Background(), 0); err != nil {
					log.Fatal(err)
				}
				return
			}
			log.Printf("[orchestrator] no args + %s → serve HTTP", info)
			if err := appserve.Serve(context.Background(), 0); err != nil {
				log.Fatal(err)
			}
			return
		}
		printUsage()
		return
	}

	subcommand := os.Args[1]
	subArgs := os.Args[2:]
	log.Printf("[orchestrator] %s cmd=%s", info, subcommand)

	switch subcommand {
	case "auth":
		if err := runAuth(context.Background(), subArgs); err != nil {
			os.Exit(1)
		}
	case "token":
		runTokenSync(subArgs)
	case "account_state":
		runAccountState(subArgs)
	case "manage_exits":
		runManageExits(subArgs)
	case "scan_market":
		runScanMarket(subArgs)
	case "stage_orders":
		runStageOrders(subArgs)
	case "submit_orders":
		runSubmitOrders(subArgs)
	case "trigger_limits":
		runTriggerLimits(subArgs)
	case "strategy_allowlist":
		runStrategyAllowlist(subArgs)
	case "deploy_strategy":
		runDeployStrategy(subArgs)
	case "probe_seat":
		runProbeSeat(subArgs)
	case "check_health":
		runCheckHealth(subArgs)
	case "ready":
		runReady(subArgs)
	case "risk":
		runRisk(subArgs)
	case "weekly_digest":
		runWeeklyDigest(subArgs)
	case "live_vs_backtest_trades":
		runReconcileTrades(subArgs)
	case "backfill_fills":
		// A dry run restores the database and leaves the bucket alone; only a real run replicates.
		runBackfillFills(subArgs)
	case "orders":
		// Read-only: it asks Schwab and prints. No database, so nothing to restore or replicate.
		runOrders(subArgs)
	case "live_vs_backtest_sync":
		// A dry run restores the database and leaves the bucket alone; only a real sync replicates.
		runBacktestSync(subArgs)
	case "live_vs_backtest_winrate":
		// Only a run that writes live_vs_backtest_winrate replicates. Printing or --dry-run restores the
		// database and leaves the bucket alone, like the live_vs_backtest_report service.
		runLiveVsBacktestWinrate(subArgs)
	case "live_vs_backtest_slippage":
		runAuditSlippage(subArgs)
	case "live_vs_backtest_report":
		runReconcileReport(subArgs)
	case "run_e2e_live":
		runRunE2ELive(subArgs)
	case "killswitch":
		runKillswitch(subArgs)
	case "exit_market":
		runExitMarket(subArgs)
	case "clear_stale":
		runClearStale(subArgs)
	case "health_checks":
		runHealthChecks(subArgs)
	case "run_all_jobs":
		runAllJobs(subArgs)
	case "active_trades":
		runActiveTrades(subArgs)
	case "staged_orders":
		runStagedOrders(subArgs)
	case "strategies":
		runListStrategies(subArgs)
	case "cron":
		runCron(subArgs)
	case "runtime":
		fmt.Println(runtimeenv.Detect().String())
	case "dashboard":
		runDashboard(subArgs)
	case "serve":
		// Explicit HTTP (local preview of GAE). Same code as auto-serve.
		port := 8080
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		p := fs.Int("port", 8080, "HTTP port")
		_ = fs.Parse(subArgs)
		if *p > 0 {
			port = *p
		}
		if err := appserve.Serve(context.Background(), port); err != nil {
			log.Fatal(err)
		}
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Printf("%sUnknown command: %s%s\n", colRed, subcommand, colReset)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf("%s%s=== orchestrator - trading job CLI (schedulers wrap these flags) ===%s\n\n", colBold, colCyan, colReset)
	fmt.Println("Usage: orchestrator <command> [options]")
	fmt.Println("\nData Ingestion & Auth Commands:")
	fmt.Printf("  %sauth%s           Complete one-time OAuth authentication (broker; implemented in schwaber)\n", colGreen, colReset)
	fmt.Printf("  %stoken%s          Sync local token.json with the shared token in GCS, schwab-token/<env>.json (pull|push)\n", colGreen, colReset)
	fmt.Printf("  %srun_e2e_live%s       Runs the whole daily pipeline once, LIVE (real orders), and checks it worked (no dry run; also /run_e2e_live)\n", colGreen, colReset)
	fmt.Printf("  %skillswitch%s     on|off|status: turns every /cron route off (and, in the server, cancels running jobs); also /killswitch\n", colGreen, colReset)
	fmt.Printf("  %sexit_market%s    Cancels all working orders and sells every long position at market in every managed account (LIVE by default, --dry-run previews; also /exit_market; never scheduled)\n", colGreen, colReset)
	fmt.Printf("  %sclear_stale%s    Retires the REJECTED/PENDING staged orders and orphan OPEN trades of strategies that are no longer greenlit, in Turso only (the broker is not contacted), so check_health stops reporting them (LIVE by default, --dry-run previews; also /clear_stale; never scheduled)\n", colGreen, colReset)
	fmt.Printf("  %saccount_state%s      Fetches configured accounts, positions, orders, transactions into Turso (via schwaber)\n", colGreen, colReset)
	fmt.Println("\nAutomated Trading Jobs:")
	fmt.Printf("  %smanage_exits%s   Job 1: Checks open positions against the creating strategy's holding window and profit target\n", colGreen, colReset)
	fmt.Printf("  %sscan_market%s    Job 2: Scans for buy signals (stage_orders turns them into orders)\n", colGreen, colReset)
	fmt.Printf("  %sstage_orders%s   Job 2a: Sizes scan_market's buy signals against live cash and stages bracket orders\n", colGreen, colReset)
	fmt.Printf("  %ssubmit_orders%s  Job 2b: Re-validates and submits orders staged by stage_orders\n", colGreen, colReset)
	fmt.Printf("  %strigger_limits%s Job 2c: Seats the limit entries stage_orders held back as WATCHING, nearest to filling first; places LIVE unless --dry-run; never cancels; quits after 2 refusals (--account picks the account)\n", colGreen, colReset)
	fmt.Printf("  %sdeploy_strategy%s    Puts one strategy row live: export the bundle, vendor, check, deploy, add it to STRATEGY_ALLOWLIST, restart (live; --dry-run prints the plan; --no-restart stops before the last deploy)\n", colGreen, colReset)
	fmt.Printf("  %sstrategy_allowlist%s Shows or changes STRATEGY_ALLOWLIST in the config object: show | set LIST | add ID... | remove ID... (live; --dry-run previews; validates every entry first)\n", colGreen, colReset)
	fmt.Printf("  %scheck_health%s   Job 3: Audits account health -- protective orders, scheduled exits, plus every enabled row in check_health\n", colGreen, colReset)
	fmt.Printf("  %srisk%s           Record each account's equity and set what NEW entries may do from drawdown, daily loss and exposure (exits never limited; --dry-run previews; --reset-peak after a withdrawal; also /cron/risk)\n", colGreen, colReset)
	fmt.Printf("  %sready%s          Pre-flight: does the allowlist resolve from the bundle, are models and market data current, is the Schwab token good (--static = bundle check only, for deploy.sh; --email sends the report if not ok; also /ready)\n", colGreen, colReset)
	fmt.Printf("  %sweekly_digest%s  Builds the Saturday summary (signals, orders, job health, positions, readiness) and emails it (--dry-run prints only)\n", colGreen, colReset)
	fmt.Printf("  %slive_vs_backtest_trades%s  Compares each live decision with a one-share backtest replay (not scheduled)\n", colGreen, colReset)
	fmt.Printf("  %sbackfill_fills%s    Gives closed manage_exit rows the broker's real entry/exit fill prices (from Schwab's orders) and re-measures their P&L; places nothing (--dry-run writes nothing; not scheduled, manage_exits does it too)\n", colGreen, colReset)
	fmt.Printf("  %sorders%s            Lists every Schwab order (brackets unfolded) with its real fill price beside the limit and stop sent; -symbol, -status, -days. Read-only, no --dry-run. Page: /orders\n", colGreen, colReset)
	fmt.Printf("  %slive_vs_backtest_sync%s     Keeps the newest in-sample + out-of-sample backtest of each greenlit strategy in an archive folder (-reports-dir, -dest), records the run in live_vs_backtest_deployed and refreshes the /live_vs_backtest_winrate backtest rates (--dry-run copies nothing; not scheduled)\n", colGreen, colReset)
	fmt.Printf("  %swinrate%s           Compares each strategy's backtest in-sample, out-of-sample and live win rate; -reports-dir / -ref-db / -evals-db first copy the newest backtest rates in (--dry-run copies nothing). Page: /live_vs_backtest_winrate (not scheduled)\n", colGreen, colReset)
	fmt.Printf("  %slive_vs_backtest_slippage%s    Summarizes entry and exit slippage and fill delay from live_vs_backtest_trade (not scheduled)\n", colGreen, colReset)
	fmt.Printf("  %slive_vs_backtest_report%s  Runs live_vs_backtest_trades and live_vs_backtest_slippage, then writes live_vs_backtest_report.db and HTML (not scheduled)\n", colGreen, colReset)
	fmt.Printf("  %srun_all_jobs%s   Executes Health Check -> Exit Manager -> Strategy Scan -> Order Submission in sequence\n", colGreen, colReset)
	fmt.Printf("  %sactive_trades%s  Displays active strategy-managed trades from the orchestrator database\n", colGreen, colReset)
	fmt.Printf("  %sstaged_orders%s  Displays orders staged by stage_orders awaiting submit_orders\n", colGreen, colReset)
	fmt.Printf("  %shealth_checks%s  Lists/enables/disables check_health rows, or shows recent findings (--findings)\n", colGreen, colReset)
	fmt.Printf("  %sstrategies%s     Lists strategies registered in backtestgosqlite and which are greenlit (STRATEGY_ALLOWLIST)\n", colGreen, colReset)
	fmt.Printf("  %scron%s           Trigger a job on the DEPLOYED service by hand: cron <job>|list [--url URL] [--dry-run] (Google ID token; see README)\n", colGreen, colReset)
	fmt.Printf("  %sdashboard%s      Launches local web UI only (no /cron)\n", colGreen, colReset)
	fmt.Printf("  %sserve%s          HTTP: dashboard + /cron/* + OAuth (same as GAE)\n", colGreen, colReset)
	fmt.Printf("  %sruntime%s        Print detected runtime context (local|gae)\n", colGreen, colReset)
	fmt.Println("\nFlags for all jobs:")
	fmt.Println("  --dry-run      Preview only. Commands are LIVE by default; --live is accepted and --live=false also previews")
	fmt.Println("  --account      Single broker account number")
	fmt.Println("  --accounts     Comma-separated broker account numbers, max 5 (default: SCHWAB_ACCOUNT_NUMBERS from the config object)")
	fmt.Println("\nFlags for scan_market / run_all_jobs:")
	fmt.Println("  --strategy     Strategy ID or stack (primary+overlay+...), must be in STRATEGY_ALLOWLIST")
	fmt.Println("  --strategies   Comma-separated IDs/stacks, all must be greenlit (default: every STRATEGY_ALLOWLIST entry)")
	fmt.Println("  scan_market also takes a strategy ID as a positional arg, e.g. `scan_market sig-voo-buy-tecl`")
	fmt.Println("  -- shorthand for -strategy; conflicts with -strategy/-strategies. Flags must come BEFORE it.")
	fmt.Println("\nExamples:")
	fmt.Println("  orchestrator auth")
	fmt.Println("  orchestrator token pull   # before Mac CLI use; requires gcloud auth")
	fmt.Println("  orchestrator token push   # after local refresh if auto-push did not run")
	fmt.Println("  orchestrator strategies")
	fmt.Println("  orchestrator manage_exits --dry-run")
	fmt.Println("  orchestrator scan_market -strategy sig-voo-buy-tecl+mara_tree+pdd_tree --dry-run")
	fmt.Println("  orchestrator scan_market --dry-run sig-voo-buy-tecl   # positional shorthand (flags first)")
	fmt.Println("  orchestrator submit_orders --dry-run")
	fmt.Println("  orchestrator run_all_jobs --dry-run -accounts 111111,222222,333333")
	fmt.Println("  orchestrator check_health")
	fmt.Println("  orchestrator run_all_jobs")
	fmt.Println("  orchestrator active_trades")
	fmt.Println("  orchestrator dashboard --port 8080")
}

// -----------------------------------------------------------------------------
// Everything below is a thin wrapper: parse flags, build the environment, call
// the same pkg/ function the /cron/<job> route uses (or its CLI form), print the
// error, set the exit code. No business logic lives here.
// -----------------------------------------------------------------------------

// fail prints err (unless the command already reported it) and exits non-zero.
func fail(err error) {
	if !errors.Is(err, jobenv.ErrReported) {
		fmt.Printf("%s[Error]%s %v\n", colRed, colReset, err)
	}
	os.Exit(1)
}

// openEnv loads config and opens the database with every schema ensured.
func openEnv() jobenv.Env {
	env, err := bootstrap.Load()
	if err != nil {
		fail(fmt.Errorf("failed to initialize: %w", err))
	}
	return env
}

// accountFlags registers --account/--accounts on fs; call Accounts() after Parse.
func accountFlags(fs *flag.FlagSet, accountsUsage string) func() jobenv.Accounts {
	acc := fs.String("account", "", "Target Schwab account number (single)")
	accs := fs.String("accounts", "", accountsUsage)
	return func() jobenv.Accounts { return jobenv.Accounts{Account: *acc, Accounts: *accs} }
}

const accountsUsage = "Comma-separated broker account numbers, max 5 (default: SCHWAB_ACCOUNT_NUMBERS)"

// withResolvedAccounts opens the environment, resolves the accounts the flags name, and hands
// them to f.
func withResolvedAccounts(acc func() jobenv.Accounts, f func(ctx context.Context, tc *trader.TraderClient, accts []*trader.ResolvedAccount)) {
	env := openEnv()
	defer env.DB.Close()
	targets, err := acc().Targets()
	if err != nil {
		fail(err)
	}
	ctx := context.Background()
	tc, accts, err := jobenv.ResolveAccounts(ctx, env.Cfg, env.DB, targets)
	if err != nil {
		fail(err)
	}
	f(ctx, tc, accts)
}
