// Package deploy_strategy puts one strategy row live: it adds the row to the strategy bundle,
// ships a build that holds it, adds its id to STRATEGY_ALLOWLIST and restarts the service so the
// new list is read. Every step is an existing tool (scripts/export_strategies.sh, go build,
// deploy_gae/deploy.sh, strategy_allowlist), run in order, and each can be run again, so a
// run that stops can be started again from the top.
//
// It needs the repository checkout (the strategies database is its refdata/strategies.db), gcloud and the
// project's credentials, so it runs from a developer machine and not on App Engine.
package deploy_strategy

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategy_allowlist"
)

// JobName is the subcommand and the log prefix.
const JobName = "deploy_strategy"

// rowTables are the strategy tables a row can be in.
var rowTables = []string{"streak_strategy", "hold_strategy", "tree_strategy", "markov_strategy", "hmm_strategy", "rotation_strategy", "price_action_reclaim_strategy"}

// Options are the inputs to the job.
type Options struct {
	ID         string // the strategy row to deploy
	Root       string // this repository (default: the current directory)
	BacktestDB string // strategies.db the bundle is exported from (default: refdata/strategies.db in Root)
	DryRun     bool   // check and print the plan, run no step
	NoRestart  bool   // stop before the final restart deploy
}

// Step is one thing the job does.
type Step struct {
	Name string
	Dir  string
	Env  []string // added to the environment
	Cmd  []string // empty for the allowlist step, which runs in the built binary
	Note string
}

// Plan returns the steps that deploy id when the live allowlist is current. The bundle must keep
// every entry already live, and every row it already holds (tests and other lists rely on them),
// so the export list is the live list, then id, then the bundled rows not already in it.
// alreadyLive means id is in the allowlist, so the allowlist step is left out and the run only
// refreshes the bundle.
func Plan(root, id string, current, bundled []string, noRestart bool) (steps []Step, exportList []string, alreadyLive bool) {
	alreadyLive = slices.Contains(flatten(current), id)
	liveList := slices.Clone(current)
	if !alreadyLive {
		liveList = append(liveList, id)
	}
	exportList = slices.Clone(liveList)
	covered := flatten(liveList)
	for _, b := range bundled {
		if !slices.Contains(covered, b) {
			exportList = append(exportList, b)
		}
	}
	check := filepath.Join(root, "data", "deploy_check")
	checkEnv := []string{"APP_FOLDER=" + check, "STRATEGY_ALLOWLIST=" + strings.Join(liveList, ",")}
	steps = []Step{
		{Name: "export", Dir: root, Cmd: []string{"scripts/export_strategies.sh", strings.Join(exportList, ",")}, Note: "write the strategy bundle and models for the live list plus " + id},
		{Name: "build", Dir: root, Cmd: []string{"go", "build", "-o", "bin/orchestrator", "./cmd/orchestrator"}, Note: "build the binary that holds the new bundle"},
		{Name: "check", Dir: root, Env: checkEnv, Cmd: []string{"bin/orchestrator", "ready", "--static"}, Note: "the new bundle must resolve every allowlist entry, " + id + " included"},
		{Name: "deploy", Dir: root, Cmd: []string{"deploy_gae/deploy.sh"}, Note: "ship the bundle while the live list is unchanged, so no instance sees an entry it cannot resolve"},
	}
	if !alreadyLive {
		steps = append(steps, Step{Name: "allowlist", Dir: root, Env: []string{"APP_FOLDER=" + check}, Cmd: []string{"bin/orchestrator", "strategy_allowlist", "add", id}, Note: "add " + id + " to STRATEGY_ALLOWLIST in the config object"})
	}
	if !noRestart {
		steps = append(steps, Step{Name: "restart", Dir: root, Env: []string{"FORCE_DEPLOY=1"}, Cmd: []string{"deploy_gae/deploy.sh"}, Note: "redeploy so the running service reads the new list"})
	}
	return steps, exportList, alreadyLive
}

// defaultRefPath is the strategies database the bundle is exported from: refdata/strategies.db in the
// repository. (It used to be in a sibling backtestgosqlite checkout; that code is now pkg/backtest here.)
func defaultRefPath(root string) string { return filepath.Join(root, "refdata", "strategies.db") }

// flatten lists the strategy ids in allowlist entries, where a+b+c is a stack of three.
func flatten(entries []string) []string {
	var out []string
	for _, e := range entries {
		out = append(out, strings.Split(e, "+")...)
	}
	return out
}

// Run is the job. It checks the row and the live list, prints the plan and, unless DryRun, runs
// each step, stopping at the first that fails.
func Run(ctx context.Context, out io.Writer, opts Options) error {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return fmt.Errorf("%s needs a strategy id: %s <id> [--dry-run] [--no-restart]", JobName, JobName)
	}
	root := opts.Root
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		root = wd
	}
	refPath := opts.BacktestDB
	if refPath == "" {
		refPath = defaultRefPath(root)
	}
	fmt.Fprintf(out, "\n%s%s=== %s ===%s\n", jobenv.ColBold, jobenv.ColCyan, JobName, jobenv.ColReset)
	jobenv.WriteMode(out, opts.DryRun)

	table, err := findStrategyAssetRowTable(refPath, id)
	if err != nil {
		return err
	}
	current, err := strategy_allowlist.Current(ctx)
	if err != nil {
		return err
	}
	bundle, err := strategies.ReadBundledStrategyAssetInfo()
	if err != nil {
		return fmt.Errorf("read the bundle compiled into this binary: %w", err)
	}
	steps, exportList, alreadyLive := Plan(root, id, current, bundle.IDs, opts.NoRestart)
	fmt.Fprintf(out, "[%s] %s is a row of %s in %s\n", JobName, id, table, refPath)
	fmt.Fprintf(out, "[%s] live allowlist: %s\n", JobName, strings.Join(current, ","))
	if alreadyLive {
		fmt.Fprintf(out, "[%s] %s is already in the allowlist: this run only refreshes its row in the bundle\n", JobName, id)
	}
	fmt.Fprintf(out, "[%s] bundle will hold: %s\n", JobName, strings.Join(exportList, ","))
	for i, s := range steps {
		fmt.Fprintf(out, "  %d. %-9s %s\n", i+1, s.Name, s.Note)
	}
	if opts.DryRun {
		fmt.Fprintf(out, "[%s] dry run: no step was run\n", JobName)
		return nil
	}

	check := filepath.Join(root, "data", "deploy_check")
	for i, s := range steps {
		if s.Name == "check" || s.Name == "allowlist" {
			// The build is a cache of the bundle it was built with. A copy left from an earlier
			// run would hide the new rows, so each of these steps starts from a fresh folder.
			if err := os.RemoveAll(filepath.Join(check, "refdata")); err != nil {
				return err
			}
		}
		fmt.Fprintf(out, "\n[%s] step %d of %d: %s\n", JobName, i+1, len(steps), s.Name)
		if err := runStep(ctx, out, s); err != nil {
			return fmt.Errorf("step %d (%s) failed: %w. Every step can be run again: run %s %s again", i+1, s.Name, err, JobName, id)
		}
	}
	fmt.Fprintf(out, "\n[%s] done: %s is deployed", JobName, id)
	if opts.NoRestart {
		fmt.Fprint(out, ", but the service has not restarted: run FORCE_DEPLOY=1 deploy_gae/deploy.sh to read the new list")
	}
	fmt.Fprintln(out)
	return nil
}

// runStep runs one step's command, streaming its output.
func runStep(ctx context.Context, out io.Writer, s Step) error {
	if len(s.Cmd) == 0 {
		return fmt.Errorf("step %s has no command", s.Name)
	}
	cmd := exec.CommandContext(ctx, s.Cmd[0], s.Cmd[1:]...)
	cmd.Dir = s.Dir
	cmd.Env = append(os.Environ(), s.Env...)
	cmd.Stdout, cmd.Stderr = out, out
	if strings.Contains(s.Cmd[0], "/") && !filepath.IsAbs(s.Cmd[0]) {
		cmd.Path = filepath.Join(s.Dir, s.Cmd[0])
	}
	return cmd.Run()
}
