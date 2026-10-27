package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/backfill_fills"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/check_health"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_report"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_slippage"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_sync"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_trades"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_winrate"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/orders"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/ready"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategy_status"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/trade_history"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/weekly_digest"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

func runReconcileTrades(args []string) {
	fs := flag.NewFlagSet("live_vs_backtest_trades", flag.ExitOnError)
	account := fs.String("account", "", "Limit staged orders and managed trades to this account. Signals that were never staged are still included.")
	marketDB := fs.String("market-db", "", "Market bars working copy (default: MARKET_DB). A missing file still writes the live side.")
	table := fs.String("table", "backtest_start", "Bars table inside the market database")
	fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	path := strings.TrimSpace(*marketDB)
	if path == "" {
		path = env.Cfg.MarketDB
	}
	if err := live_vs_backtest_trades.RunCommand(context.Background(), os.Stdout, env, *account, path, *table); err != nil {
		env.DB.Close()
		fail(err)
	}
}

func runReconcileReport(args []string) {
	fs := flag.NewFlagSet("live_vs_backtest_report", flag.ExitOnError)
	account := fs.String("account", "", "Limit the ledger rows this run replaces, and the cash table, to this account.")
	marketDB := fs.String("market-db", "", "Market bars working copy (default: MARKET_DB). A missing file still writes the live side.")
	table := fs.String("table", "backtest_start", "Bars table inside the market database")
	htmlPath := fs.String("html", "", "Report HTML file (default: live_vs_backtest_report.html in the OS temp folder, trade_orchestrator/). The report tables are in Turso.")
	fs.Parse(args)

	ctx := context.Background()
	env := openEnv()
	defer env.DB.Close()
	path := strings.TrimSpace(*marketDB)
	if path == "" {
		path = env.Cfg.MarketDB
	}
	res, err := live_vs_backtest_report.Run(ctx, os.Stdout, env, live_vs_backtest_report.Options{
		Account:   *account,
		MarketDB:  path,
		Table:     *table,
		HTML:      *htmlPath,
		Allowlist: env.Cfg.StrategyAllowlist,
	})
	if err != nil {
		env.DB.Close()
		reportFailed(err)
	}
	printReportStatus(res)
}

func reportFailed(err error) {
	fmt.Printf("%slive_vs_backtest_report FAILED%s\n%v\n", colRed, colReset, err)
	os.Exit(1)
}

func printReportStatus(res live_vs_backtest_report.Result) {
	text := live_vs_backtest_report.FormatStatus(res)
	head := "live_vs_backtest_report OK"
	color := colGreen
	if len(res.Warnings) > 0 {
		head = "live_vs_backtest_report OK, with warnings"
		color = colYellow
	}
	fmt.Printf("%s%s%s%s", color, head, colReset, strings.TrimPrefix(text, head))
}

// runBacktestSync is `orchestrator live_vs_backtest_sync [-reports-dir DIR] [-dest DIR] [--dry-run]`: keep
// the newest backtest (in-sample and out-of-sample pass) of every STRATEGY_ALLOWLIST strategy in
// an archive folder, record which run each came from in live_vs_backtest_deployed, then refresh the win
// rates /live_vs_backtest_winrate compares live trades against. With --dry-run nothing is copied or written.
func runBacktestSync(args []string) {
	fs := flag.NewFlagSet("live_vs_backtest_sync", flag.ExitOnError)
	reports := fs.String("reports-dir", appenv.Reports(), "backtest reports folder (data/reports under APP_FOLDER)")
	dest := fs.String("dest", "", "archive folder (default: live_vs_backtest_deployeds in the OS temp folder (trade_orchestrator/))")
	dryRun := fs.Bool("dry-run", false, "Show which run would be kept for each strategy; copy and write nothing")
	fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	if *dest == "" {
		*dest = filepath.Join(live_vs_backtest_report.ScratchDir(env.Cfg), "live_vs_backtest_deployeds")
	}
	ids := strategy_status.Members(os.Getenv("STRATEGY_ALLOWLIST"))
	ctx := context.Background()
	res, err := live_vs_backtest_sync.Run(ctx, env.DB, live_vs_backtest_sync.Options{ReportsDir: *reports, DestDir: *dest, IDs: ids, DryRun: *dryRun})
	if err != nil {
		env.DB.Close()
		fail(err)
	}
	live_vs_backtest_sync.Format(os.Stdout, res)
	if *dryRun {
		return
	}
	n, err := live_vs_backtest_winrate.Refresh(ctx, env.DB, live_vs_backtest_winrate.Sources{Reports: *dest, IDs: ids}, false)
	if err != nil {
		env.DB.Close()
		fail(err)
	}
	fmt.Printf("[live_vs_backtest_sync] archive %s; live_vs_backtest_winrate holds %d strategies\n", *dest, n)
}

// runBackfillFills is `orchestrator backfill_fills [--dry-run]`: give closed manage_exit rows the
// broker's real entry and exit fill prices, read from Schwab's orders, and re-measure their P&L.
// It reads Schwab and writes only manage_exit; it places and cancels nothing. --dry-run prints
// what it would change and writes nothing.
func runBackfillFills(args []string) {
	fs := flag.NewFlagSet("backfill_fills", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Show which rows would get fills and write nothing")
	fs.Parse(args)
	live := strategy_status.FetchLiveFromEnv(context.Background(), time.Now(), trader.NewTraderClient, 15*time.Second, 60*time.Second)
	if len(live.Accounts) == 0 {
		for _, e := range live.Errors {
			fmt.Fprintf(os.Stderr, "[backfill_fills] %s\n", e)
		}
		fail(fmt.Errorf("no account could be read from Schwab"))
	}
	env := openEnv()
	defer env.DB.Close()
	lines, err := backfill_fills.Run(env.DB, live, *dryRun)
	if err != nil {
		env.DB.Close()
		fail(err)
	}
	mode := "corrected"
	if *dryRun {
		mode = "would correct"
	}
	for _, l := range lines {
		fmt.Println("[backfill_fills]", l)
	}
	fmt.Printf("[backfill_fills] %s %d row(s)\n", mode, countRows(lines))
}

func countRows(lines []string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, "broker fills recorded") {
			n++
		}
	}
	return n
}

// runOrders is `orchestrator orders [-symbol S] [-status S] [-days N]`: every order Schwab lists for
// the allowlisted accounts, brackets unfolded, with the price each really executed at beside the
// limit and stop that were sent. It only reads Schwab: it places and cancels nothing and writes no
// database, so it has no --dry-run.
func runOrders(args []string) {
	fs := flag.NewFlagSet("orders", flag.ExitOnError)
	symbol := fs.String("symbol", "", "only this symbol")
	status := fs.String("status", "", "only this broker status (FILLED, WORKING, CANCELED, REJECTED, ...)")
	days := fs.Int("days", orders.DefaultDays, "how many days back, 1 to 58")
	fs.Parse(args)
	if *days < 1 || *days > 58 {
		fail(fmt.Errorf("--days must be 1 to 58"))
	}
	now := time.Now()
	live := strategy_status.FetchLiveFromEnv(context.Background(), now, trader.NewTraderClient, 15*time.Second, 60*time.Second)
	for _, e := range live.Errors {
		fmt.Fprintf(os.Stderr, "[orders] %s\n", e)
	}
	if len(live.Accounts) == 0 {
		fail(fmt.Errorf("no account could be read from Schwab"))
	}
	rows := orders.Rows(live.Orders, orders.Options{Symbol: *symbol, Status: *status, Since: now.AddDate(0, 0, -*days)})
	if len(rows) == 0 {
		fmt.Println("[orders] no orders match")
		return
	}
	orders.Format(os.Stdout, rows)
}

// runLiveVsBacktestWinrate is `orchestrator live_vs_backtest_winrate [-evals-db FILE] [--dry-run]`: it copies the newest
// backtest in-sample and out-of-sample win rates of every strategy in FILE (a backtestgosqlite
// strategy_evals database) into the orchestrator database, then prints backtest against live win rate.
// Without -evals-db it only prints. With --dry-run nothing is written.
func runLiveVsBacktestWinrate(args []string) {
	fs := flag.NewFlagSet("live_vs_backtest_winrate", flag.ExitOnError)
	evals := fs.String("evals-db", "", "strategy_evals database to copy in-sample / out-of-sample win rates from (writes live_vs_backtest_winrate in the orchestrator database)")
	reports := fs.String("reports-dir", "", "backtestgosqlite reports folder (data/reports): each run's in-sample pass and its oos/ held-out pass (writes live_vs_backtest_winrate)")
	export := fs.String("export-baseline", "", "write the strategies' stored backtest rates to this file in the form pkg/live_vs_backtest_winrate/baseline.json takes (compiled into the binary so a fresh deploy shows them); reads only")
	ref := fs.String("ref-db", "", "refdata strategies.db to copy each strategy's last whole-window backtest win rate from (writes live_vs_backtest_winrate)")
	dryRun := fs.Bool("dry-run", false, "Count what would be copied and print the comparison; write nothing")
	fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	ctx := context.Background()
	if *evals != "" || *ref != "" || *reports != "" {
		n, err := live_vs_backtest_winrate.Refresh(ctx, env.DB, live_vs_backtest_winrate.Sources{EvalsDB: *evals, RefDB: *ref, Reports: *reports, IDs: strategy_status.Members(os.Getenv("STRATEGY_ALLOWLIST"))}, *dryRun)
		if err != nil {
			env.DB.Close()
			fail(err)
		}
		verb := "copied"
		if *dryRun {
			verb = "would copy"
		}
		fmt.Printf("[live_vs_backtest_winrate] %s backtest win rates for %d strategies from %s\n", verb, n, strings.TrimSpace(*reports+" "+*evals+" "+*ref))
	}
	opt := live_vs_backtest_winrate.Options{RefDB: *ref}
	if opt.RefDB == "" {
		opt.RefDB = appenv.RefDB()
	}
	opt.IDs = strategy_status.Members(os.Getenv("STRATEGY_ALLOWLIST"))
	if *export != "" {
		n, err := live_vs_backtest_winrate.ExportBaseline(ctx, env.DB, *export, opt.IDs)
		if err != nil {
			env.DB.Close()
			fail(err)
		}
		fmt.Printf("[live_vs_backtest_winrate] wrote the backtest rates of %d strategies to %s; rebuild to compile them in\n", n, *export)
	}
	rows, err := live_vs_backtest_winrate.Compare(ctx, env.DB, opt)
	if err != nil {
		env.DB.Close()
		fail(err)
	}
	live_vs_backtest_winrate.Format(os.Stdout, rows)
}

func runAuditSlippage(args []string) {
	fs := flag.NewFlagSet("live_vs_backtest_slippage", flag.ExitOnError)
	fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	if err := live_vs_backtest_slippage.RunCommand(context.Background(), os.Stdout, env); err != nil {
		env.DB.Close()
		fail(err)
	}
}

// runWeeklyDigest is `orchestrator weekly_digest [--dry-run]`: the Saturday email, by hand.
func runWeeklyDigest(args []string) {
	fs := flag.NewFlagSet("weekly_digest", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Print the digest and send nothing")
	fs.Parse(args)
	allowlist, err := strategies.Parse(os.Getenv(strategies.EnvAllowlist))
	if err != nil {
		fail(err)
	}
	env := openEnv()
	defer env.DB.Close()
	_, err = weekly_digest.RunJob(context.Background(), env.DB, os.Stdout, weekly_digest.JobOptions{
		Ready: ready.Options{Allowlist: allowlist, MarketDB: env.Cfg.MarketDB, TokenPath: env.Cfg.Schwab.TokenPath},
		Email: !*dryRun,
	})
	if err != nil {
		fail(err)
	}
}

func runListStrategies(args []string) {
	fs := flag.NewFlagSet("strategies", flag.ExitOnError)
	fs.Parse(args)

	appconfig.MustLoad() // config was loaded from GCS in main; read STRATEGY_ALLOWLIST after it
	strategies.PrintRegistered(os.Stdout, os.Getenv(strategies.EnvAllowlist))
}

func runActiveTrades(args []string) {
	fs := flag.NewFlagSet("active_trades", flag.ExitOnError)
	accFlag := fs.String("account", "", "Target Schwab account number")
	fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	if err := manage_exits.ListActive(os.Stdout, env.DB, *accFlag); err != nil {
		env.DB.Close()
		fail(err)
	}
}

func runStagedOrders(args []string) {
	fs := flag.NewFlagSet("staged_orders", flag.ExitOnError)
	accFlag := fs.String("account", "", "Target Schwab account number")
	reopen := fs.String("reopen", "", "Comma-separated staged order ids: put each REJECTED order that never reached the broker back to PENDING so submit_orders tries it again (changes the database; places nothing)")
	fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	if *reopen != "" {
		for _, part := range strings.Split(*reopen, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if err != nil {
				fail(fmt.Errorf("--reopen: %q is not a staged order id", part))
			}
			ok, err := stage_orders.ReopenRejectedOrder(env.DB, id)
			if err != nil {
				fail(fmt.Errorf("--reopen %d: %w", id, err))
			}
			if !ok {
				fail(fmt.Errorf("--reopen %d: not reopened: the order is not REJECTED, or it has a broker order id", id))
			}
			fmt.Printf("staged order %d is PENDING again\n", id)
		}
	}
	if err := stage_orders.ListStaged(os.Stdout, env.DB, *accFlag); err != nil {
		env.DB.Close()
		fail(err)
	}
}

func runHealthChecks(args []string) {
	fs := flag.NewFlagSet("health_checks", flag.ExitOnError)
	accFlag := fs.String("account", "", "Filter recent findings to this account (with -findings)")
	enableName := fs.String("enable", "", "Enable a health check by name")
	disableName := fs.String("disable", "", "Disable a health check by name")
	showFindings := fs.Bool("findings", false, "Show recent findings instead of the check definitions")
	limit := fs.Int("limit", 25, "Max rows to show with -findings")
	fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	if err := check_health.RunChecksCommand(os.Stdout, env.DB, check_health.ChecksOptions{
		Account: *accFlag, Enable: *enableName, Disable: *disableName, ShowFindings: *showFindings, Limit: *limit,
	}); err != nil {
		env.DB.Close()
		fail(err)
	}
}

// runTradeHistory is `orchestrator trade_history [--dry-run]`: copy the trades that actually happened
// (closed positions with both fills recorded) into trade_history, then print the live_vs_backtest
// view, the ongoing live win rate of each strategy against its backtest. It also runs at the end of
// every manage_exits run. With --dry-run it counts the trades it would add and writes nothing.
func runTradeHistory(args []string) {
	fs := flag.NewFlagSet("trade_history", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Count the trades that would be captured; write nothing")
	fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	ctx := context.Background()
	res, err := trade_history.Capture(ctx, env.DB, *dryRun)
	if err != nil {
		env.DB.Close()
		fail(err)
	}
	if !res.Ready {
		fmt.Println("[trade_history] trade_history does not exist yet; a real run creates it and its views")
		return
	}
	verb := "captured"
	if *dryRun {
		verb = "would capture"
	}
	fmt.Printf("[trade_history] %s %d new trade(s); trade_history holds %d\n", verb, res.New, res.Total)
	rows, err := trade_history.LiveVsBacktest(ctx, env.DB)
	if err != nil {
		env.DB.Close()
		fail(err)
	}
	trade_history.Format(os.Stdout, rows)
}
