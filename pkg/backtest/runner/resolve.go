package runner

import (
	"fmt"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// ResolveStrategies parses a strategy arg the same way cmd/backtest and
// cmd/livescan always have: "all", comma/space-separated IDs, or empty →
// defaultID when provided.
func ResolveStrategies(arg, defaultID string) ([]strategy.Strategy, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		arg = strings.TrimSpace(defaultID)
	}
	if arg == "" {
		return nil, fmt.Errorf("no strategies selected")
	}
	if strings.EqualFold(arg, "all") {
		list := strategy.ListAll()
		if len(list) == 0 {
			return nil, fmt.Errorf("no strategies in the reference database (strategies.db)")
		}
		return list, nil
	}
	var out []strategy.Strategy
	for _, token := range strings.FieldsFunc(arg, func(r rune) bool { return r == ',' || r == ' ' }) {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		s, ok := strategy.Get(token)
		if !ok {
			// A family name stands for every row of that family: -strategy markov
			if members, isFamily := strategy.FamilyMembers(token); isFamily {
				out = append(out, members...)
				continue
			}
			return nil, fmt.Errorf("strategy %q not found in registry (use backtest -strategylist)", token)
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid strategies selected")
	}
	return out, nil
}
