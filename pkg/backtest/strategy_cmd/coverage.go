package strategy_cmd

import (
	"flag"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/barcoverage"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/sessions"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
)

// coverageMain is `strategy coverage`.
func coverageMain() {
	ids := flag.String("id", "", "strategy ids and a+b+c stacks, comma-separated; reports every symbol they need")
	symbols := flag.String("symbols", "", "extra symbols to report, comma-separated (with no -id, the only ones)")
	start := flag.String("start", "2021-01-01", "first session to judge, YYYY-MM-DD (the standard backtest start)")
	end := flag.String("end", "", "last session to judge; empty = latest bar")
	db := flag.String("db", appenv.MarketDB(), "hourly market database")
	flag.Parse()
	if *ids == "" && *symbols == "" {
		log.Fatal("coverage needs -id <strategy> or -symbols A,B")
	}

	strategy.AutoRegisterSQLStrategies(appenv.Folder(), *db)
	stratreg.RegisterFamilies()
	set := map[string]bool{}
	for _, entry := range strings.Split(*ids, ",") {
		for _, id := range strategy.ParseStack(strings.TrimSpace(entry)) {
			if id == "" {
				continue
			}
			s, ok := strategy.Get(id)
			if !ok {
				log.Fatalf("unknown strategy %q (see ./bin/strategy)", id)
			}
			for _, sym := range runner.RequiredSymbolsFor([]strategy.Strategy{s}, "") {
				set[sym] = true
			}
		}
	}
	for _, sym := range strings.Split(*symbols, ",") {
		if sym = strings.ToUpper(strings.TrimSpace(sym)); sym != "" {
			set[sym] = true
		}
	}
	list := make([]string, 0, len(set))
	for sym := range set {
		list = append(list, sym)
	}
	sort.Strings(list)
	if len(list) == 0 {
		log.Fatal("those strategies name no symbols (a universe-wide strategy uses every symbol); pass -symbols")
	}

	if err := appenv.RequireHourly(*db); err != nil {
		log.Fatal(err)
	}
	if _, err := sessions.Ensure(*db); err != nil {
		log.Fatal(err)
	}
	opts := barcoverage.CoverageOptions{Symbols: list, DB: *db, Start: *start, End: *end}
	rows, err := barcoverage.Coverage(opts)
	if err != nil {
		log.Fatal(err)
	}
	barcoverage.WriteCoverage(os.Stdout, opts, rows)
}
