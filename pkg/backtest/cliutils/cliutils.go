package cliutils

import (
	"os"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
)

// GetDefaultMarketDB resolves the default market history database path.
func GetDefaultMarketDB() string {
	return appenv.MarketDB()
}

// PopSubcommand checks whether os.Args[1] is one of the given subcommand
// names (aliases map name -> canonical name). If so it removes it from
// os.Args, so flag.Parse still works, and returns the canonical name;
// otherwise it returns "".
func PopSubcommand(aliases map[string]string) string {
	if len(os.Args) > 1 {
		if canon, ok := aliases[os.Args[1]]; ok {
			os.Args = append(os.Args[:1], os.Args[2:]...)
			return canon
		}
	}
	return ""
}

// SplitList splits a comma-separated flag value, trimming whitespace and dropping empty entries.
func SplitList(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
