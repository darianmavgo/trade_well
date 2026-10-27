package strategies

import (
	"fmt"
	"io"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
)

// ByID indexes resolved strategies by their ID.
func ByID(strats []strategy.Strategy) map[string]strategy.Strategy {
	byID := make(map[string]strategy.Strategy, len(strats))
	for _, s := range strats {
		byID[s.ID()] = s
	}
	return byID
}

// PrintRegistered is the `orchestrator strategies` command: every strategy
// registered in backtestgosqlite, marking which are greenlit by rawAllowlist
// (the STRATEGY_ALLOWLIST value; the caller has loaded the config object before reading it).
func PrintRegistered(out io.Writer, rawAllowlist string) {
	allowlist, allowErr := Parse(rawAllowlist)

	fmt.Fprintf(out, "\n%s%s=== Registered Strategies (backtestgosqlite) ===%s\n\n", jobenv.ColBold, jobenv.ColCyan, jobenv.ColReset)
	fmt.Fprintf(out, "  %-40s | %-8s | %s\n", "ID", "Greenlit", "Name")
	fmt.Fprintln(out, "  "+strings.Repeat("-", 100))
	for _, s := range strategy.List() {
		mark := ""
		if MemberGreenlit(allowlist, s.ID()) {
			mark = fmt.Sprintf("%s✓%s", jobenv.ColGreen, jobenv.ColReset)
		}
		fmt.Fprintf(out, "  %-40s | %-17s | %s\n", s.ID(), mark, s.Name())
	}
	for _, g := range allowlist {
		if strategy.IsStack(g) {
			fmt.Fprintf(out, "  %-40s | %s✓%s               | stack %s\n", g, jobenv.ColGreen, jobenv.ColReset, strings.Join(strategy.ParseStack(g), " → "))
		}
	}
	fmt.Fprintln(out)

	if allowErr != nil {
		fmt.Fprintf(out, "%s[Warning]%s %v — nothing is greenlit yet, scan_market will refuse to run\n\n", jobenv.ColYellow, jobenv.ColReset, allowErr)
	} else {
		fmt.Fprintf(out, "Greenlit for active trading (%s): %s%s%s\n\n", EnvAllowlist, jobenv.ColBold, strings.Join(allowlist, ", "), jobenv.ColReset)
	}
}
