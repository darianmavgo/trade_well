package strategies

import (
	"slices"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// MemberGreenlit is true if id is a greenlit allowlist entry, or a member of
// a greenlit stack (so mara_tree inside sig-voo-buy-tecl+mara_tree+pdd_tree
// shows as live without being independently allowlisted). Stack syntax is
// backtestgosqlite's strategy.ParseStack.
func MemberGreenlit(allowlist []string, id string) bool {
	if slices.Contains(allowlist, id) {
		return true
	}
	for _, entry := range allowlist {
		if slices.Contains(strategy.ParseStack(entry), id) {
			return true
		}
	}
	return false
}

// Members lists the strategy ids behind an allowlist: each entry itself, or the members of a
// stack entry in order, without repeats. It is "what is deployed", as opposed to the entries.
func Members(allowlist []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, entry := range allowlist {
		ids := []string{entry}
		if strategy.IsStack(entry) {
			ids = strategy.ParseStack(entry)
		}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}
