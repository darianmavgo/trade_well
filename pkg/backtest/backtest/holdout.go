package backtest

import (
	"fmt"
	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"log"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/sessions"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
)

// DefaultHoldoutMonths is how much of the end of history a plain run or a
// stack-eval keeps out of the main pass. Selection, ranking and tuning see
// only the earlier data; the held-out months are run once, afterwards, with
// the strategy or stack that pass produced.
const DefaultHoldoutMonths = storage.DefaultHoldoutMonths

// stackEvalOutcome is what stack-eval found, for the out-of-sample pass.
type stackEvalOutcome struct {
	StackID      string                    // a+b+c, empty when no stack was built
	DefaultAsset string                    // park symbol applied to every run, if any
	Report       *models.PerformanceReport // the single strategy's report, for the out-of-sample comparison
}

func holdoutApplies(conf Config) bool {
	if conf.HoldoutMonths <= 0 || conf.List || conf.SignalsOnly {
		return false
	}
	return conf.Mode == "" || conf.Mode == "stack-eval"
}

// holdoutSplit returns the last in-sample date and the first out-of-sample
// date. ok is false when history is too short to split.
func holdoutSplit(conf Config) (inEnd, oosStart string, ok bool, err error) {
	inEnd, ok, err = storage.InSampleEnd(conf.Db, conf.Table, conf.Start, conf.End, conf.HoldoutMonths)
	if err != nil || !ok {
		return "", "", false, err
	}
	cut, _ := time.Parse("2006-01-02", inEnd)
	return inEnd, cut.AddDate(0, 0, 1).Format("2006-01-02"), true, nil
}

// oosPath puts an output file beside the original with an _oos suffix.
func oosPath(p string) string {
	if p == "" {
		return p
	}
	ext := filepath.Ext(p)
	return strings.TrimSuffix(p, ext) + "_oos" + ext
}

func banner(title string) {
	fmt.Printf("\n%s\n%s\n%s\n", strings.Repeat("#", 100), title, strings.Repeat("#", 100))
}

// Run executes the command with cfg. It returns errors instead of exiting.
//
// By default the last HoldoutMonths of history are held out: the main pass
// runs through the cutoff, then the same strategy (or the stack stack-eval
// built) runs once on the held-out months, flat at the start, with results in
// <out-dir>/oos. Pass -holdout-months 0 to simulate all history in one pass.
func Run(conf Config) error {
	if conf.Serial {
		conf.Concurrency = 1
	}
	runner.KeepCalc = conf.KeepCalc
	if conf.CPUProfile != "" {
		pf, err := os.Create(conf.CPUProfile)
		if err != nil {
			return err
		}
		defer pf.Close()
		if err := pprof.StartCPUProfile(pf); err != nil {
			return err
		}
		defer pprof.StopCPUProfile()
	}
	ensureSessions(conf.Db, sessionSymbols(conf))
	conf, err := prepareRun(conf)
	if err != nil {
		return err
	}
	if !holdoutApplies(conf) {
		return runOnce(conf)
	}
	inEnd, oosStart, ok, err := holdoutSplit(conf)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Printf("\nNot enough history to hold out %d months; running on all of it.\n", conf.HoldoutMonths)
		return runOnce(conf)
	}

	in := conf
	in.End = inEnd
	var outcome stackEvalOutcome
	in.outcome = &outcome
	in.phase = "in-sample"
	banner(fmt.Sprintf("IN-SAMPLE: %s → %s   (last %d months held out; use this window to select and tune)", conf.Start, inEnd, conf.HoldoutMonths))
	if err := runOnce(in); err != nil {
		return err
	}

	oos := conf
	oos.Start = oosStart
	oos.OutDir = filepath.Join(conf.OutDir, "oos")
	oos.Html = oosPath(conf.Html)
	oos.outcome = nil
	oos.phase = "out-of-sample"
	oos.prior = outcome.Report
	if conf.Mode == "stack-eval" {
		if outcome.StackID == "" {
			fmt.Println("\nstack built no stack, so there is nothing to test out of sample.")
			return nil
		}
		oos.Mode = ""
		oos.Strategy = outcome.StackID
		oos.Primary, oos.Secondary, oos.Args = "", "", nil
		oos.DefaultAsset = outcome.DefaultAsset
	}
	if err := os.MkdirAll(oos.OutDir, 0o755); err != nil {
		return err
	}
	banner(fmt.Sprintf("OUT-OF-SAMPLE: %s → %s   (held out; run once, flat at the start, not used for selection)", oosStart, lastOrLatest(conf.End)))
	return runOnce(oos)
}

func lastOrLatest(end string) string {
	if end == "" {
		return "latest bar"
	}
	return end
}

// prepareRun turns the reports root in conf.OutDir into the folder of this run.
// A command that writes results starts the next numbered folder (or, with
// -run-id, goes back into an existing one), and OutDir becomes that folder: the
// family databases are written there and the held-out pass writes to its oos
// folder. stale only reads, so it takes the named run or the latest. The HTML
// report, unless -html names a path, goes in the run folder as report.html.
func prepareRun(conf Config) (Config, error) {
	if conf.Mode == "covered-call" || conf.Mode == "newrun" || conf.Mode == "lastrun" {
		return conf, nil
	}
	root := conf.OutDir
	var dir string
	var id int
	var err error
	switch {
	case conf.RunID > 0:
		id = conf.RunID
		dir, err = storage.RunDir(root, id)
	case conf.Mode == "stale":
		id, dir = storage.LatestRunDir(root)
		if dir == "" {
			err = fmt.Errorf("no runs in %s yet", root)
		}
	default:
		id, dir, err = storage.NewRun(root)
	}
	if err != nil {
		return conf, err
	}
	// An absolute path, so later steps that resolve relative paths against the
	// reports folder (the HTML report) do not nest it twice.
	if abs, aerr := filepath.Abs(dir); aerr == nil {
		dir = abs
	}
	fmt.Printf("\n📁 Run %d: %s\n", id, dir)
	conf.OutDir = dir
	if conf.Html == DefaultConfig().Html {
		conf.Html = filepath.Join(dir, "report.html")
	}
	return conf, nil
}

// sessionSymbols returns the symbols a plain or stack run reads, with the benchmark ETFs that
// pick the held-out window, so only those get fresh session rows. It returns nil, meaning every
// symbol, for any other mode and for any strategy that cannot name the symbols it reads.
func sessionSymbols(conf Config) []string {
	if conf.Mode != "" || conf.List || conf.SignalsOnly {
		return nil
	}
	ids := strings.FieldsFunc(strings.Join(append([]string{conf.Strategy, conf.Primary, conf.Secondary}, conf.Args...), ","),
		func(r rune) bool { return r == ',' || r == '+' || r == ' ' })
	if len(ids) == 0 {
		return nil
	}
	stratreg.RegisterAll(appenv.Folder(), conf.Db)
	var strats []strategy.Strategy
	for _, id := range ids {
		s, ok := strategy.Get(id)
		if !ok {
			return nil
		}
		if _, sql := s.(*strategy.SQLPipelineStrategy); sql { // reads symbols its SQL does not name
			return nil
		}
		if rp, ok := s.(strategy.RequiredSymbolsProvider); !ok || len(rp.RequiredSymbols()) == 0 {
			return nil
		}
		strats = append(strats, s)
	}
	syms := runner.RequiredSymbolsFor(strats, conf.Symbol)
	if da := strings.ToUpper(strings.TrimSpace(conf.DefaultAsset)); da != "" {
		syms = append(syms, da)
	}
	return append(syms, "SPY", "QQQ", "VOO")
}

// ensureSessions brings the derived session rows of the hourly database up to
// date before a run reads them. A database that is not there is left alone.
// A nil only derives every symbol.
func ensureSessions(marketDB string, only []string) {
	path := appenv.BarDB(marketDB, "1h")
	if _, err := os.Stat(path); err != nil {
		return
	}
	if _, err := sessions.EnsureSymbols(path, only); err != nil {
		log.Printf("Warning: deriving sessions in %s: %v", path, err)
	}
}
