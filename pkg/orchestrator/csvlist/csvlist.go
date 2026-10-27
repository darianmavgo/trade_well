// Package csvlist splits the comma-separated lists the orchestrator reads from flags and the
// config object (strategy allowlist, Schwab account numbers).
package csvlist

import "strings"

// Unique splits raw on commas, trims whitespace, drops empty entries and removes repeats,
// keeping the first occurrence of each in order. It returns nil for an empty list; callers
// decide whether empty is an error and what the message says.
func Unique(raw string) []string {
	seen := make(map[string]bool)
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		item := strings.TrimSpace(part)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}
