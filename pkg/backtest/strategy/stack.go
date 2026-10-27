package strategy

import "strings"

// StackSep joins strategy IDs into a priority-stack ID ("primary+overlay+...").
const StackSep = "+"

// IsStack reports whether id names a priority stack rather than one strategy.
func IsStack(id string) bool {
	return strings.Contains(id, StackSep)
}

// ParseStack splits a stack ID into member strategy IDs. The first member is
// the primary (capital precedence). Blank and duplicate pieces are dropped, so
// a plain strategy ID yields a one-element slice.
func ParseStack(id string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(id, StackSep) {
		p := strings.TrimSpace(part)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// StackID joins member IDs, primary first, into a stack ID (the inverse of
// ParseStack for already-clean input).
func StackID(members ...string) string {
	return strings.Join(members, StackSep)
}
