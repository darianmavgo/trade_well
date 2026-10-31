// Package stackid is the one spelling of a stack id: the member strategy ids joined with "+",
// primary first. strategy, refdb and everything that reads a stack use it.
package stackid

import "strings"

// Sep joins strategy IDs into a priority-stack ID ("primary+overlay+...").
const Sep = "+"

// IsStack reports whether id names a priority stack rather than one strategy.
func IsStack(id string) bool {
	return strings.Contains(id, Sep)
}

// Parse splits a stack ID into member strategy IDs. The first member is the primary (capital
// precedence). Blank and duplicate pieces are dropped, so a plain strategy ID yields a
// one-element slice.
func Parse(id string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(id, Sep) {
		p := strings.TrimSpace(part)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// Join joins member IDs, primary first, into a stack ID (the inverse of Parse for already-clean input).
func Join(members ...string) string {
	return strings.Join(members, Sep)
}
