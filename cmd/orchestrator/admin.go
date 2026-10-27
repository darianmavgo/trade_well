package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appserve"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/auth"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/bootstrap"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/cronclient"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/deploy_strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/ready"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/risk"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategy_allowlist"
)

func runAuth(ctx context.Context, args []string) error {
	env, err := bootstrap.Load()
	if err != nil {
		fmt.Printf("%sError:%s %v\n", colRed, colReset, err)
		return err
	}
	defer env.DB.Close()
	return auth.RunLogin(ctx, env)
}

func runTokenSync(args []string) {
	env := openEnv()
	defer env.DB.Close()
	if err := auth.RunTokenCommand(context.Background(), os.Stdout, env, args); err != nil {
		env.DB.Close()
		fail(err)
	}
}

// runStrategyAllowlist is `orchestrator strategy_allowlist [show|set LIST|add ID...|remove ID...] [--dry-run]`.
func runStrategyAllowlist(args []string) {
	fs := flag.NewFlagSet("strategy_allowlist", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Show the resulting list and write nothing")
	// Flags may follow the action, as in `strategy_allowlist add x --dry-run`.
	var pos []string
	for len(args) > 0 {
		_ = fs.Parse(args)
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
	opts := strategy_allowlist.CommandOptions{DryRun: *dryRun}
	if len(pos) > 0 {
		opts.Action, opts.Args = pos[0], pos[1:]
	}
	if err := strategy_allowlist.RunCommand(context.Background(), os.Stdout, opts); err != nil {
		fail(err)
	}
}

// runDeployStrategy is `orchestrator deploy_strategy <id> [--dry-run] [--no-restart]`.
func runDeployStrategy(args []string) {
	fs := flag.NewFlagSet("deploy_strategy", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Check the row and the live allowlist and print the steps; run none")
	noRestart := fs.Bool("no-restart", false, "Stop before the final deploy that restarts the service")
	// The id may come before or after the flags.
	var id string
	for len(args) > 0 {
		_ = fs.Parse(args)
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		id, args = rest[0], rest[1:]
	}
	if err := deploy_strategy.Run(context.Background(), os.Stdout, deploy_strategy.Options{ID: id, DryRun: *dryRun, NoRestart: *noRestart}); err != nil {
		fail(err)
	}
}

// runKillswitch is `orchestrator killswitch on|off|status [reason...]`: the same switch
// /killswitch flips, on the database this process opens. Cancelling running jobs only
// happens inside the server (/killswitch); from here it sets the flag every cron route reads.
// runReady is `orchestrator ready [--static] [--email] [--json]`. It exits 1 when the status is bad,
// so deploy.sh can stop before uploading a bundle that does not resolve the live allowlist.
func runReady(args []string) {
	fs := flag.NewFlagSet("ready", flag.ExitOnError)
	static := fs.Bool("static", false, "Only check that the allowlist resolves from the bundle compiled into this binary (no database, market data or broker)")
	email := fs.Bool("email", false, "Email the report to the operator when anything is not ok")
	asJSON := fs.Bool("json", false, "Print the report as JSON")
	fs.Parse(args)

	allowlist, err := strategies.Parse(os.Getenv(strategies.EnvAllowlist))
	if err != nil {
		fail(err)
	}
	opts := ready.JobOptions{Options: ready.Options{Allowlist: allowlist, Static: *static}, Email: *email}
	var rep ready.Report
	if *static {
		rep = ready.Run(context.Background(), opts.Options)
	} else {
		env := openEnv()
		defer env.DB.Close()
		opts.MarketDB, opts.TokenPath = env.Cfg.MarketDB, env.Cfg.Schwab.TokenPath
		var sink strings.Builder
		var rerr error
		rep, rerr = ready.RunJob(context.Background(), env.DB, &sink, opts)
		if rerr != nil {
			log.Printf("ready: %v", rerr)
		}
	}
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(rep)
	} else {
		fmt.Print(ready.Text(rep))
	}
	if rep.Status == ready.Bad {
		os.Exit(1)
	}
}

// runRisk is `orchestrator risk [--dry-run] [--reset-peak]`: record each account's equity and set
// what new entries may do (full size, reduced, or halted). It reads the account_state mirror and
// makes no broker call; exits are never limited. It writes the risk tables unless --dry-run.
func runRisk(args []string) {
	fs := flag.NewFlagSet("risk", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Print the assessment and write nothing")
	reset := fs.Bool("reset-peak", false, "Restart the equity high-water mark at today's equity (after a withdrawal, which otherwise looks like a drawdown)")
	fs.Parse(args)
	env := openEnv()
	defer env.DB.Close()
	if _, err := risk.RunJob(context.Background(), env.DB, os.Stdout, risk.JobOptions{
		Limits: env.Cfg.Risk, DryRun: *dryRun, Email: !*dryRun, ResetPeak: *reset,
	}); err != nil {
		fail(err)
	}
}

func runDashboard(args []string) {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	port := fs.Int("port", 8080, "HTTP port for dashboard web server")
	fs.Parse(args)

	env := openEnv()
	env.DB.Close() // bootstrap only ensures the schema; the server opens its own handle

	srv, err := dashboard.NewServer(env.Cfg.DBPath, *port)
	if err != nil {
		fail(fmt.Errorf("failed to initialize dashboard server: %w", err))
	}
	if err := srv.Start(); err != nil {
		fail(fmt.Errorf("dashboard server exited: %w", err))
	}
}

func runCron(args []string) {
	fs := flag.NewFlagSet("cron", flag.ExitOnError)
	baseURL := fs.String("url", "", "Deployed service base URL (default: $ORCHESTRATOR_URL, else https://<project>.uc.r.appspot.com for $GOOGLE_CLOUD_PROJECT or the project gcloud is set to)")
	dryRun := fs.Bool("dry-run", false, "Ask for a preview (adds ?dry_run=1; only stage_orders honors it)")
	// Allow `cron stage_orders --dry-run` as well as `cron --dry-run stage_orders`.
	var pos []string
	for len(args) > 0 {
		_ = fs.Parse(args)
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
	if err := cronclient.RunCommand(context.Background(), os.Stdout, cronclient.CommandOptions{
		BaseURL: *baseURL, DryRun: *dryRun, Args: pos, Jobs: appserve.CronJobNames(),
	}); err != nil {
		fail(err)
	}
}
