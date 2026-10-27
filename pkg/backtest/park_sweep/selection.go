package park_sweep

import (
	"fmt"
	"strings"
)

// ParseIDs splits a comma-separated -strategy value. Empty or "all" means every
// strategy and returns nil.
func ParseIDs(arg string) []string {
	arg = strings.TrimSpace(arg)
	if arg == "" || strings.EqualFold(arg, "all") {
		return nil
	}
	var ids []string
	for _, id := range strings.Split(arg, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// inClause returns " AND <col> IN (?, ...)" (case-insensitive) and its arguments for ids, or an
// empty clause when ids is empty (every strategy).
func inClause(col string, ids []string) (string, []interface{}) {
	if len(ids) == 0 {
		return "", nil
	}
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = strings.ToLower(id)
	}
	return fmt.Sprintf(" AND lower(%s) IN (%s)", col, strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")), args
}
