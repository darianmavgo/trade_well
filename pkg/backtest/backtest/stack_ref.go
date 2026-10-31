package backtest

import (
	"fmt"
	"os"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// resolveStackRef turns a saved stack's id ("stack-001") or label into its "a+b+c" stack id, the
// form the rest of backtest runs. Anything else is returned as given: a strategy id (which wins
// over a label of the same spelling), an inline "a+b+c" stack, or a comma list.
func resolveStackRef(arg, strategiesDB string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" || strings.ContainsAny(arg, "+,") {
		return arg, nil
	}
	if _, isStrategy := strategy.Get(arg); isStrategy {
		return arg, nil
	}
	if fi, err := os.Stat(strategiesDB); err != nil || fi.Size() == 0 {
		return arg, nil
	}
	db, err := refdb.Open(strategiesDB)
	if err != nil {
		return arg, fmt.Errorf("open %s to look for a stack named %q: %w", strategiesDB, arg, err)
	}
	defer db.Close()
	st, ok, err := refdb.StackByName(db, arg)
	if err != nil {
		return arg, err
	}
	if !ok {
		return arg, nil
	}
	members := st.Members()
	if len(members) == 0 {
		return arg, fmt.Errorf("stack %s (%s) has no strategies", st.ID, st.Label)
	}
	fmt.Printf("📚 Stack %s \"%s\": %s\n", st.ID, st.Label, strings.Join(members, " → "))
	return strategy.StackID(members...), nil
}
