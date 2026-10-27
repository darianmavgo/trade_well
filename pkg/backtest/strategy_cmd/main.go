// Package strategy_cmd is the strategy command: list the strategies, export a
// small reference database of chosen rows, or derive a new strategy from a
// sweep's parameter set.
//
//	strategy                 list the strategies with their own pipeline and the family counts
//	strategy export          write a reference database holding only some rows
//	strategy coverage        report how complete the daily (or -hourly) bars are for the symbols a strategy needs
//	strategy derive          copy a rotation row with a sweep's parameter set and/or a position size
//	strategy seed            write a table's schema and rows as a seed SQL file
//	strategy defaults        make one gridsearch row's hold, take-profit and stop-loss the strategy row's defaults
//	strategy rotation_universe  rank the liquid universe point in time and store the rotation candidate symbols as a symbol list
package strategy_cmd

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/olekukonko/tablewriter"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/cliutils"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/rotation_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
)

// Main is the CLI entry point of cmd/strategy.
func Main() {
	switch cliutils.PopSubcommand(map[string]string{"export": "export", "derive": "derive", "coverage": "coverage", "defaults": "defaults", "seed": "seed", "rotation_universe": "rotation_universe"}) {
	case "export":
		export()
	case "derive":
		deriveMain()
	case "coverage":
		coverageMain()
	case "defaults":
		defaultsMain()
	case "seed":
		seedMain()
	case "rotation_universe":
		rotationUniverseMain()
	default:
		list()
	}
}

// export writes a reference database holding only the rows of the named strategies
// or stacks, for a program that cannot carry the whole file.
func export() {
	src := flag.String("src", appenv.RefDB(), "full reference database to copy rows from")
	out := flag.String("out", "", "reference database to write")
	ids := flag.String("ids", "", "strategy ids and a+b+c stacks, comma-separated")
	modelsOut := flag.String("models-out", "", "directory to also write markov_models.db and tree_models.db into, holding only the trained models of the exported rows' signal symbols")
	alloc := flag.Float64("alloc", 0, "size every exported row at this fraction of equity per position (0.20 = 20%) and record it in the bundle; use the -alloc the backtest ran with. 0 keeps each row's own allocation")
	markovSrc := flag.String("markov-src", appenv.MarkovDB(), "full Markov model database to copy models from")
	treeSrc := flag.String("tree-src", appenv.TreeDB(), "full tree model database to copy models from")
	flag.Parse()
	if *out == "" || *ids == "" {
		log.Fatal("export needs -ids and -out")
	}
	var members []string
	seen := map[string]bool{}
	for _, entry := range strings.Split(*ids, ",") {
		for _, id := range strategy.ParseStack(strings.TrimSpace(entry)) {
			if !seen[id] {
				seen[id] = true
				members = append(members, id)
			}
		}
	}
	found, err := refdb.Export(*src, *out, members)
	for table, got := range found {
		fmt.Printf("  %-16s %v\n", table, got)
	}
	if err != nil {
		log.Fatal(err)
	}
	if *alloc > 0 {
		if err := refdb.SetAllocation(*out, *alloc); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("every row sized at %.0f%% of equity per position (bundle_meta alloc_pct)\n", *alloc*100)
	}
	fmt.Printf("wrote %s with %d strategies\n", *out, len(members))
	if *modelsOut == "" {
		return
	}
	for family, src := range map[string]string{"markov": *markovSrc, "tree": *treeSrc} {
		dst := filepath.Join(*modelsOut, family+"_models.db")
		syms, err := refdb.ExportModels(family, src, *out, dst)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("wrote %s with %d %s models\n", dst, len(syms), family)
	}
}

// list prints the strategies that have their own pipeline and the row family counts.
func list() {
	// Initialize databases and register all strategies.
	strategy.AutoRegisterSQLStrategies(appenv.Folder(), appenv.MarketDB())
	stratreg.RegisterFamilies()

	strategies := strategy.List()

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"ID", "Name", "Description"})
	table.SetBorder(true)
	table.SetRowLine(true)

	for _, s := range strategies {
		table.Append([]string{s.ID(), s.Name(), s.Description()})
	}

	fmt.Printf("\nFound %d Strategies defined by their own sql/strategies pipeline:\n", len(strategies))
	table.Render()
	fmt.Println("\nRow-backed families (not listed one by one):")
	strategy.PrintFamilyCounts(os.Stdout)
	fmt.Println()
}

// deriveMain is `strategy derive`.
func deriveMain() {
	from := flag.String("from", "", "rotation strategy id to copy")
	params := flag.String("params", "", "parameter set, like Period-1d/Limit-95%/Hold-2d/TP+3%/SL-3%, or a whole sweep output line holding one (bare arguments work too)")
	position := flag.Float64("position-pct", 0, "size of each position as a fraction of portfolio value, for a pick = all row (0.20 is 20%); sets allocation_pct and max_weight_pct. Alone, it copies the row with only that change")
	id := flag.String("id", "", "id of the new strategy (default: the source id with the parameters)")
	name := flag.String("name", "", "name of the new strategy (default: the source name with the parameters)")
	ref := flag.String("ref", appenv.RefDB(), "reference database to add the strategy to")
	flag.Parse()
	text := *params
	if text == "" {
		text = strings.Join(flag.Args(), " ")
	}
	if err := DeriveWith(os.Stdout, *ref, *from, *id, *name, text, *position); err != nil {
		log.Fatal(err)
	}
}

// Derive is DeriveWith with the source's position size.
func Derive(out io.Writer, refPath, from, id, name, text string) error {
	return DeriveWith(out, refPath, from, id, name, text, 0)
}

// DeriveWith adds to the reference database at refPath a copy of the rotation
// strategy from, with the parameter set in text (a gridsearch label, alone or inside
// the line a sweep printed) when there is one, and with positionPct as the size of
// each position when it is above 0. It says how to run the copy.
func DeriveWith(out io.Writer, refPath, from, id, name, text string, positionPct float64) error {
	if from == "" {
		return fmt.Errorf("derive needs -from <rotation strategy id>")
	}
	var p *rotation_strategy.Params
	if strings.TrimSpace(text) != "" || positionPct <= 0 {
		parsed, err := rotation_strategy.ParseParams(text)
		if err != nil {
			return err
		}
		p = &parsed
	}
	db, err := refdb.Open(refPath)
	if err != nil {
		return err
	}
	defer db.Close()
	row, err := rotation_strategy.DeriveWith(db, from, id, name, p, positionPct)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "added %s (%s) to %s\n", row.ID, row.Name, refPath)
	if p != nil {
		fmt.Fprintf(out, "  from %s with %s\n", from, p.Label())
	}
	if positionPct > 0 {
		fmt.Fprintf(out, "  positions %.0f%% of portfolio value each (from %s)\n", positionPct*100, from)
	}
	fmt.Fprintf(out, "  run it: ./bin/backtest -strategy %s\n", row.ID)
	return nil
}
