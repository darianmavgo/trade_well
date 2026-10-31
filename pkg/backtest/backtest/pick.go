package backtest

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// runPickCommand implements `backtest pick`: choose strategies from the finished runs of one or
// more families (-family hmm,hold or hmm@54,hold@60, in -run-id or the latest run) that win at
// least -min-win-rate of their trades in-sample and held-out and rarely enter on the same days.
// It reads results only. With several families the pick database goes in the first family's run.
func runPickCommand(conf Config) error {
	if strings.TrimSpace(conf.Strategy) == "" {
		return fmt.Errorf("pick needs the families whose results to read: -family hmm,hold (or hmm@54,hold@60)")
	}
	var sources []runner.PickSource
	var outDir, outName string
	for _, item := range strings.Split(conf.Strategy, ",") {
		family, runPart, _ := strings.Cut(strings.TrimSpace(item), "@")
		c := conf
		if runPart != "" {
			id, err := strconv.Atoi(runPart)
			if err != nil {
				return fmt.Errorf("pick: %q: the run after @ must be a number", item)
			}
			c.RunID = id
		}
		_, dir, err := runFolder(c)
		if err != nil {
			return err
		}
		if outDir == "" {
			outDir, outName = dir, family
		}
		sources = append(sources, runner.PickSource{
			Name:       family,
			InSampleDB: filepath.Join(dir, family+".db"),
			HeldOutDB:  filepath.Join(dir, "oos", family+".db"),
		})
	}
	if len(sources) > 1 {
		outName = "multi"
	}
	opts := runner.PickOptions{
		Sources:      sources,
		Exclude:      splitIDs(conf.PickExclude),
		OutDB:        filepath.Join(outDir, "pick_"+outName+".db"),
		Count:        conf.Pick,
		MinWinRate:   conf.MinWinRate,
		MinTrades:    conf.MinTrades,
		MinOOSTrades: conf.MinOOSTrades,
		TopN:         conf.TopCandidates,
		MaxOverlap:   conf.MaxOverlap,
	}
	res, err := runner.PickSet(context.Background(), opts)
	if err != nil {
		return err
	}
	runner.PrintPickResult(res, opts)
	if conf.SaveStack != "" {
		return saveStack(conf, res)
	}
	return nil
}

// saveStack adds the picks, in the order they were chosen, to the stack table of the strategies
// database under the label -save-stack. Run it with -run-id/-family to see what is saved first.
func saveStack(conf Config, res runner.PickResult) error {
	if len(res.Picks) == 0 {
		return fmt.Errorf("-save-stack %q: pick chose nothing to save", conf.SaveStack)
	}
	ids := make([]string, len(res.Picks))
	for i, p := range res.Picks {
		ids[i] = p.ID
	}
	db, err := refdb.Open(conf.StrategiesDb)
	if err != nil {
		return err
	}
	defer db.Close()
	note := fmt.Sprintf("backtest pick: win rate >= %.0f%% in-sample and held-out, overlap <= %.0f%%", conf.MinWinRate*100, conf.MaxOverlap*100)
	st, err := refdb.AddStack(db, conf.SaveStack, ids, note)
	if err != nil {
		return err
	}
	fmt.Printf("   Saved as %s \"%s\" in %s. Backtest it with: backtest -strategy %s\n", st.ID, st.Label, conf.StrategiesDb, st.ID)
	return nil
}

// runFolder is the run folder of conf: -run-id, or the latest run.
func runFolder(conf Config) (int, string, error) {
	if conf.RunID > 0 {
		dir, err := storage.RunDir(conf.OutDir, conf.RunID)
		return conf.RunID, dir, err
	}
	id, dir := storage.LatestRunDir(conf.OutDir)
	if dir == "" {
		return 0, "", fmt.Errorf("no runs in %s yet", conf.OutDir)
	}
	return id, dir, nil
}

// splitIDs splits a comma separated list of strategy ids.
func splitIDs(list string) []string {
	var ids []string
	for _, id := range strings.Split(list, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
