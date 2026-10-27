package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_state"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appserve"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/check_health"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/clear_stale"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/exit_market"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/killswitch"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/run_all_jobs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/run_e2e_live"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/submit_orders"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/trigger_limits"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

func runAccountState(args []string) {
	fs := flag.NewFlagSet("account_state", flag.ExitOnError)
	acc := accountFlags(fs, accountsUsage)
	fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	if err := account_state.RunCommand(context.Background(), os.Stdout, env, acc()); err != nil {
		env.DB.Close()
		fail(err)
	}
}

func runManageExits(args []string) {
	fs := flag.NewFlagSet("manage_exits", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Preview exit actions without submitting live market sell orders")
	live := fs.Bool("live", true, "Submit live exit orders to Schwab (the default; --dry-run or --live=false previews)")
	acc := accountFlags(fs, accountsUsage)
	fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	if err := manage_exits.RunCommand(context.Background(), os.Stdout, env, manage_exits.CommandOptions{
		Accounts: acc(), DryRun: *dryRun || !*live,
	}); err != nil {
		env.DB.Close()
		fail(err)
	}
}

func runScanMarket(args []string) {
	fs := flag.NewFlagSet("scan_market", flag.ExitOnError)
	symbolsFlag := fs.String("symbols", "VOO", "Comma-separated list of symbols to scan")
	strategyName := fs.String("strategy", "", "Single strategy ID (must be greenlit)")
	strategiesFlag := fs.String("strategies", "", "Comma-separated strategy IDs, must all be greenlit (default: every strategy in STRATEGY_ALLOWLIST)")
	profitPct := fs.Float64("profit", 0, "Override take-profit percent; 0 = use the strategy's DefaultConfig / signal")
	maxDays := fs.Int("max-days", 0, "Override holding window in trading days; 0 = use the strategy's window (HoldDaysOverride per signal)")
	dryRun := fs.Bool("dry-run", false, "Preview only — do not write staged orders")
	live := fs.Bool("live", true, "Write staged orders for real (the default; --dry-run or --live=false previews). Does NOT submit to the broker: submit_orders does that")
	acc := accountFlags(fs, accountsUsage)
	fs.Parse(args)

	// Optional positional strategy argument(s), e.g. `orchestrator scan_market
	// sig-voo-buy-tecl` -- shorthand for -strategy. Conflicts loudly with
	// -strategy/-strategies rather than silently picking one. Go's flag.Parse
	// stops at the first non-flag token, so any flags must come *before* the
	// positional strategy argument(s); a flag-shaped token stranded after it is
	// a usage error, not silently folded into the strategy list.
	if pos := fs.Args(); len(pos) > 0 {
		for _, p := range pos {
			if strings.HasPrefix(p, "-") {
				fail(fmt.Errorf("flags must come before the positional strategy argument (got %q after it); e.g. `scan_market --dry-run %s`", p, pos[0]))
			}
		}
		if *strategyName != "" || *strategiesFlag != "" {
			fail(fmt.Errorf("positional strategy argument %q conflicts with -strategy/-strategies", strings.Join(pos, ",")))
		}
		joined := strings.Join(pos, ",")
		strategiesFlag = &joined
	}

	env := openEnv()
	defer env.DB.Close()
	if err := stage_orders.RunScanAndStage(context.Background(), os.Stdout, env, stage_orders.ScanOptions{
		Accounts: acc(), Symbols: *symbolsFlag, Strategy: *strategyName, Strategies: *strategiesFlag,
		ProfitPct: *profitPct, MaxDays: *maxDays, DryRun: *dryRun || !*live,
	}); err != nil {
		env.DB.Close()
		fail(err)
	}
}

func runStageOrders(args []string) {
	fs := flag.NewFlagSet("stage_orders", flag.ExitOnError)
	profitPct := fs.Float64("profit", 0, "Override take-profit percent; 0 = use the strategy's value")
	maxDays := fs.Int("max-days", 0, "Override holding window in trading days; 0 = use the strategy's window")
	dryRun := fs.Bool("dry-run", false, "Preview only — size and report, write nothing")
	live := fs.Bool("live", true, "Write staged orders for real (the default; --dry-run or --live=false previews). Does NOT submit to the broker: submit_orders does that")
	acc := accountFlags(fs, accountsUsage)
	fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	if err := stage_orders.RunCommand(context.Background(), os.Stdout, env, stage_orders.CommandOptions{
		Accounts: acc(), ProfitPct: *profitPct, MaxDays: *maxDays, DryRun: *dryRun || !*live,
	}); err != nil {
		env.DB.Close()
		fail(err)
	}
}

func runSubmitOrders(args []string) {
	fs := flag.NewFlagSet("submit_orders", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Preview which staged orders would submit/reject without calling the Schwab API")
	live := fs.Bool("live", true, "Submit staged orders live to Schwab (the default; --dry-run or --live=false previews)")
	acc := accountFlags(fs, accountsUsage)
	fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	if err := submit_orders.RunCommand(context.Background(), os.Stdout, env, submit_orders.CommandOptions{
		Accounts: acc(), DryRun: *dryRun || !*live,
	}); err != nil {
		env.DB.Close()
		fail(err)
	}
}

func runTriggerLimits(args []string) {
	fs := flag.NewFlagSet("trigger_limits", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Preview which limits would be seated; place nothing")
	live := fs.Bool("live", true, "Place real orders at Schwab (the default; --dry-run or --live=false previews)")
	strategyIDs := fs.String("strategy", "", "Strategy ids to stage and seat, comma-separated (default: every greenlit strategy with a limit entry)")
	acc := accountFlags(fs, accountsUsage)
	fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	if err := trigger_limits.RunCommand(context.Background(), os.Stdout, env, trigger_limits.CommandOptions{
		Accounts: acc(), Strategy: *strategyIDs, DryRun: *dryRun || !*live,
	}); err != nil {
		env.DB.Close()
		fail(err)
	}
}

// runProbeSeat is `orchestrator probe_seat`: for each account, rest day-limit buy
// brackets far below the market until Schwab refuses one for buying power, report how many
// were accepted, then cancel them all.
func runProbeSeat(args []string) {
	fs := flag.NewFlagSet("probe_seat", flag.ExitOnError)
	live := fs.Bool("live", true, "Place (and cancel) real orders at Schwab; it has no dry run")
	alloc := fs.Float64("alloc", 0.20, "share of equity per order")
	below := fs.Float64("below", 0.25, "how far below the last price each limit rests, so none can fill")
	cancel := fs.String("cancel", "", "cancel these orders instead of probing: account:orderid,account:orderid,...")
	acc := accountFlags(fs, accountsUsage)
	fs.Parse(args)
	if *cancel == "list" {
		runListOpenOrders(acc)
		return
	}
	if *cancel != "" {
		runCancelOrders(*cancel, acc)
		return
	}
	if !*live {
		fail(fmt.Errorf("probe_seat has no dry run: it places and cancels real orders, so it cannot be run with --live=false"))
	}
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
	logPath := fmt.Sprintf("probe_seat_%s.log", time.Now().Format("20060102_150405"))
	logFile, lerr := os.Create(logPath)
	if lerr != nil {
		fail(lerr)
	}
	defer logFile.Close()
	w := io.MultiWriter(os.Stdout, logFile) // every response is kept in the log file
	fmt.Fprintf(w, "logging every response to %s\n", logPath)
	for _, a := range accts {
		fmt.Fprintf(w, "\naccount %s\n", a.AccountNumber)
		r, err := trigger_limits.Probe(ctx, w, tc, a, trigger_limits.ProbeSymbols, *alloc, *below)
		if r != nil {
			fmt.Fprintf(w, "  => accepted %d, cancelled %d, not confirmed cancelled %v\n  => ended by: %s\n", r.Accepted, r.Cancelled, r.Left, r.Refusal)
		}
		if err != nil {
			fmt.Fprintf(w, "  ERROR: %v\n", err)
		}
		if r != nil && (r.Refusal != "" && !trigger_limits.IsBuyingPowerRefusal(r.Refusal)) {
			fmt.Fprintln(w, "stopping: Schwab is refusing for a reason other than buying power; no more accounts probed")
			break
		}
	}
}

// runCancelOrders cancels each account:orderid pair (see trigger_limits.CancelOrders).
func runCancelOrders(spec string, acc func() jobenv.Accounts) {
	withResolvedAccounts(acc, func(ctx context.Context, tc *trader.TraderClient, accts []*trader.ResolvedAccount) {
		trigger_limits.CancelOrders(ctx, os.Stdout, tc, accts, spec)
	})
}

// runListOpenOrders prints every order that is not in a final state, per account.
func runListOpenOrders(acc func() jobenv.Accounts) {
	withResolvedAccounts(acc, func(ctx context.Context, tc *trader.TraderClient, accts []*trader.ResolvedAccount) {
		trigger_limits.ListOpenOrders(ctx, os.Stdout, tc, accts)
	})
}

// runExitMarket is `orchestrator exit_market [--dry-run] [--killswitch]`: cancel every working
// order and sell every long position at market in every managed account. With --dry-run it
// only prints what it would do. Not a cron job.
func runExitMarket(args []string) {
	fs := flag.NewFlagSet("exit_market", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Preview only: report what would be cancelled and sold, change nothing")
	live := fs.Bool("live", true, "Actually cancel orders and SELL at market (the default; --dry-run or --live=false previews)")
	engage := fs.Bool("killswitch", false, "After a live run, engage the killswitch so the next cron does not buy it back")
	acc := accountFlags(fs, accountsUsage)
	fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	targets, err := acc().Targets()
	if err != nil {
		env.DB.Close()
		fail(err)
	}
	tc, accounts, err := jobenv.ResolveAccounts(context.Background(), env.Cfg, env.DB, targets)
	if err != nil {
		env.DB.Close()
		fail(fmt.Errorf("failed to resolve target account(s): %w", err))
	}
	res, err := exit_market.Run(context.Background(), os.Stdout, env.DB, tc, accounts, exit_market.Options{DryRun: *dryRun || !*live, Killswitch: *engage})
	if err != nil {
		env.DB.Close()
		fail(err)
	}
	if res.Failed > 0 {
		env.DB.Close()
		os.Exit(1)
	}
}

// runClearStale is `orchestrator clear_stale [--dry-run]`:
// retire the staged orders and orphan OPEN trades of strategies that are no longer greenlit,
// in Turso. The broker is not
// contacted and nothing is deleted. Not a cron job.
func runClearStale(args []string) {
	fs := flag.NewFlagSet("clear_stale", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Preview only: list what would be cleared, change nothing")
	live := fs.Bool("live", true, "Actually clear the rows (the default; --dry-run or --live=false previews)")
	fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	allow, err := strategies.Parse(os.Getenv(strategies.EnvAllowlist))
	if err == nil {
		_, err = clear_stale.Run(context.Background(), os.Stdout, env.DB, clear_stale.Options{DryRun: *dryRun || !*live, Allowlist: allow})
	}
	if err != nil {
		env.DB.Close()
		fail(err)
	}
}

func runKillswitch(args []string) {
	if len(args) < 1 || (args[0] != "on" && args[0] != "off" && args[0] != "status") {
		fail(fmt.Errorf("usage: orchestrator killswitch on|off|status [reason]"))
	}
	env := openEnv()
	defer env.DB.Close()
	if args[0] != "status" {
		user := os.Getenv("USER")
		if user == "" {
			user = "cli"
		}
		if _, err := killswitch.Set(env.DB, args[0] == "on", "cli:"+user, strings.Join(args[1:], " "), time.Now()); err != nil {
			env.DB.Close()
			fail(err)
		}
	}
	st, err := killswitch.Current(env.DB)
	if err != nil {
		env.DB.Close()
		fail(err)
	}
	fmt.Println(st)
}

func runAllJobs(args []string) {
	fs := flag.NewFlagSet("run_all_jobs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	acc := accountFlags(fs, "Comma-separated broker account numbers, max 5")
	live := fs.Bool("live", true, "Live execution (the default; --dry-run or --live=false previews)")
	dryRun := fs.Bool("dry-run", false, "Preview actions")
	symbolsFlag := fs.String("symbols", "VOO", "Watchlist symbols")
	strategyName := fs.String("strategy", "", "Single strategy ID (must be greenlit)")
	strategiesFlag := fs.String("strategies", "", "Comma-separated strategy IDs, must all be greenlit (default: every strategy in STRATEGY_ALLOWLIST)")
	profitPct := fs.Float64("profit", 0, "Override take-profit percent; 0 = use strategy")
	maxDays := fs.Int("max-days", 0, "Override holding window; 0 = use strategy")
	_ = fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	if err := run_all_jobs.Run(context.Background(), os.Stdout, env, run_all_jobs.Options{
		Accounts: acc(), Symbols: *symbolsFlag, Strategy: *strategyName, Strategies: *strategiesFlag,
		ProfitPct: *profitPct, MaxDays: *maxDays, Live: *live && !*dryRun,
	}); err != nil {
		env.DB.Close()
		fail(err)
	}
}

// runRunE2ELive is `orchestrator run_e2e_live [--oracle]`: the same
// pipeline run the /run_e2e_live endpoint streams, printed to the terminal. It
// places real orders and has no dry run.
func runRunE2ELive(args []string) {
	fs := flag.NewFlagSet("run_e2e_live", flag.ExitOnError)
	live := fs.Bool("live", true, "Run the pipeline LIVE (stages, submits and manages real orders); it has no dry run")
	oracle := fs.Bool("oracle", false, "Also scan again over a scratch market database and compare signal counts (slow)")
	fs.Parse(args)
	if !*live {
		fail(fmt.Errorf("run_e2e_live has no dry run: it places real orders on your Schwab accounts, so it cannot be run with --live=false"))
	}
	env := openEnv()
	defer env.DB.Close()
	res, err := run_e2e_live.Run(context.Background(), os.Stdout, env.DB, appserve.CronHandler(), run_e2e_live.Options{Oracle: *oracle})
	if err != nil {
		env.DB.Close()
		fail(err)
	}
	if !res.Passed() {
		env.DB.Close()
		os.Exit(1)
	}
}

func runCheckHealth(args []string) {
	fs := flag.NewFlagSet("check_health", flag.ContinueOnError)
	acc := accountFlags(fs, accountsUsage)
	_ = fs.Parse(args)

	env := openEnv()
	defer env.DB.Close()
	if err := check_health.RunCommand(context.Background(), os.Stdout, env, acc()); err != nil {
		env.DB.Close()
		fail(err)
	}
}
