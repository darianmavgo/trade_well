package export_views

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
)

// Main is the export_views command. It writes only new files: the script, and
// a copy of the market database with the views in it. It never changes the
// market database.
func Main() {
	var o Options
	var symbols, sqlOut, dbOut string
	flag.StringVar(&o.MarketDB, "db", appenv.MarketDB(), "market database the views read (copied for -db-out, never written)")
	flag.StringVar(&sqlOut, "sql-out", "auto", "file for the CREATE VIEW script: auto is data/reports/views.sql (<id>.sql with -strategy), none skips it")
	flag.StringVar(&dbOut, "db-out", "auto", "new database holding the relevant daily bars and the views: auto is data/reports/views.db (<id>.db with -strategy), none skips it")
	flag.StringVar(&symbols, "symbols", "", "comma-separated symbols whose bars go in -db-out (default: the strategy's own; with no -strategy, every symbol)")
	flag.StringVar(&o.Strategy, "strategy", "", "strategy id: export that strategy's own signal pipeline as views (default: the shared indicator views)")
	flag.BoolVar(&o.DryRun, "dry-run", false, "list the views and write nothing")
	flag.Parse()
	o.SQLOut = resolve(sqlOut, o.Strategy, "views.sql", ".sql")
	o.DBOut = resolve(dbOut, o.Strategy, "views.db", ".db")
	for _, sym := range strings.Split(symbols, ",") {
		if sym = strings.TrimSpace(sym); sym != "" {
			o.Symbols = append(o.Symbols, sym)
		}
	}

	views, err := Run(context.Background(), o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[export_views]", err)
		os.Exit(1)
	}
	for _, v := range views {
		fmt.Println(v)
	}
}

// resolve maps a path flag: "auto" is the default file in the reports folder, named after the strategy
// when there is one; "none" is no file.
func resolve(v, id, def, ext string) string {
	switch v {
	case "none":
		return ""
	case "auto":
		if id != "" {
			return appenv.ReportFile(id + ext)
		}
		return appenv.ReportFile(def)
	}
	return v
}
