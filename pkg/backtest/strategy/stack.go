package strategy

import "github.com/darianmavgo/trade_well/pkg/backtest/stackid"

// StackSep joins strategy IDs into a priority-stack ID ("primary+overlay+...").
const StackSep = stackid.Sep

// IsStack reports whether id names a priority stack rather than one strategy.
func IsStack(id string) bool { return stackid.IsStack(id) }

// ParseStack splits a stack ID into member strategy IDs. The first member is
// the primary (capital precedence). Blank and duplicate pieces are dropped, so
// a plain strategy ID yields a one-element slice.
func ParseStack(id string) []string { return stackid.Parse(id) }

// StackID joins member IDs, primary first, into a stack ID (the inverse of
// ParseStack for already-clean input).
func StackID(members ...string) string { return stackid.Join(members...) }
