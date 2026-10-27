package strateval

import (
	"fmt"
	"strings"
	"time"
)

// DeploymentStatus values stored in deployments.status.
const (
	DeployStatusLive    = "live"
	DeployStatusRetired = "retired"
)

// StrategyStatus is one row of the browsable lifecycle view.
type StrategyStatus struct {
	StrategyID     string
	Lifecycle      string // never_tested | tested | scored_A/B/C/D | deployed | deployed_review
	Tier           string
	Deployed       bool
	DeployEntry    string
	OOSSharpe      float64
	OOSTrades      int
	OOSCAGR        float64
	LastEvaluated  string
	LastDeployNote string
}

// ensureLedger adds catalog/deployments tables and convenience views.
// Safe to call on every OpenStore.
func (s *Store) ensureLedger() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS strategies (
  strategy_id TEXT PRIMARY KEY,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  last_tier TEXT,
  eval_count INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS deployments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  strategy_id TEXT NOT NULL,
  status TEXT NOT NULL,           -- live | retired
  allowlist_entry TEXT NOT NULL,  -- raw allowlist token (may be a stack id)
  source TEXT,                    -- sync-allowlist | manual
  notes TEXT,
  changed_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_deployments_strat ON deployments(strategy_id, changed_at);
CREATE INDEX IF NOT EXISTS idx_deployments_live ON deployments(status, strategy_id);

-- Newest eval per strategy
CREATE VIEW IF NOT EXISTS v_latest_scores AS
SELECT e.*
FROM strategy_evals e
INNER JOIN (
  SELECT strategy_id, MAX(id) AS max_id
  FROM strategy_evals
  GROUP BY strategy_id
) x ON e.id = x.max_id;

-- Currently live allowlist entries (latest deployment row per allowlist_entry that is live)
CREATE VIEW IF NOT EXISTS v_deployed AS
SELECT d.*
FROM deployments d
INNER JOIN (
  SELECT allowlist_entry, MAX(id) AS max_id
  FROM deployments
  GROUP BY allowlist_entry
) x ON d.id = x.max_id
WHERE d.status = 'live';

-- Lifecycle browser (LEFT JOIN union; no FULL OUTER JOIN)
CREATE VIEW IF NOT EXISTS v_strategy_status AS
SELECT
  COALESCE(s.strategy_id, l.strategy_id) AS strategy_id,
  CASE
    WHEN d.strategy_id IS NOT NULL AND COALESCE(l.tier,'') IN ('C','D') THEN 'deployed_review'
    WHEN d.strategy_id IS NOT NULL THEN 'deployed'
    WHEN l.tier IS NOT NULL AND l.tier != '' THEN 'scored_' || l.tier
    WHEN s.eval_count > 0 THEN 'tested'
    ELSE 'registered'
  END AS lifecycle,
  COALESCE(l.tier, '') AS tier,
  CASE WHEN d.strategy_id IS NOT NULL THEN 1 ELSE 0 END AS deployed,
  COALESCE(d.allowlist_entry, '') AS deploy_entry,
  COALESCE(l.oos_sharpe, 0) AS oos_sharpe,
  COALESCE(l.oos_trades, 0) AS oos_trades,
  COALESCE(l.oos_cagr, 0) AS oos_cagr,
  COALESCE(l.oos_max_dd, 0) AS oos_max_dd,
  COALESCE(l.created_at, '') AS last_evaluated,
  COALESCE(d.changed_at, '') AS last_deploy_change,
  COALESCE(d.notes, '') AS deploy_notes,
  COALESCE(s.eval_count, 0) AS eval_count
FROM strategies s
LEFT JOIN v_latest_scores l ON l.strategy_id = s.strategy_id
LEFT JOIN v_deployed d ON d.strategy_id = s.strategy_id
UNION
SELECT
  l.strategy_id,
  CASE
    WHEN d.strategy_id IS NOT NULL AND l.tier IN ('C','D') THEN 'deployed_review'
    WHEN d.strategy_id IS NOT NULL THEN 'deployed'
    ELSE 'scored_' || l.tier
  END,
  l.tier,
  CASE WHEN d.strategy_id IS NOT NULL THEN 1 ELSE 0 END,
  COALESCE(d.allowlist_entry, ''),
  l.oos_sharpe, l.oos_trades, l.oos_cagr, l.oos_max_dd, l.created_at,
  COALESCE(d.changed_at, ''), COALESCE(d.notes, ''), 0
FROM v_latest_scores l
LEFT JOIN strategies s ON s.strategy_id = l.strategy_id
LEFT JOIN v_deployed d ON d.strategy_id = l.strategy_id
WHERE s.strategy_id IS NULL;

CREATE VIEW IF NOT EXISTS v_promote_candidates AS
SELECT * FROM v_strategy_status
WHERE tier = 'A' AND deployed = 0
ORDER BY oos_sharpe DESC;

CREATE VIEW IF NOT EXISTS v_demote_candidates AS
SELECT * FROM v_strategy_status
WHERE deployed = 1 AND tier IN ('C', 'D')
ORDER BY oos_sharpe ASC;

CREATE VIEW IF NOT EXISTS v_deployed_live AS
SELECT * FROM v_strategy_status
WHERE deployed = 1
ORDER BY strategy_id;
`)
	return err
}

// TouchStrategy upserts catalog bookkeeping after an eval.
func (s *Store) TouchStrategy(strategyID, tier string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
INSERT INTO strategies (strategy_id, first_seen_at, last_seen_at, last_tier, eval_count)
VALUES (?, ?, ?, ?, 1)
ON CONFLICT(strategy_id) DO UPDATE SET
  last_seen_at = excluded.last_seen_at,
  last_tier = CASE WHEN excluded.last_tier = '' THEN strategies.last_tier ELSE excluded.last_tier END,
  eval_count = CASE WHEN excluded.last_tier = '' THEN strategies.eval_count ELSE strategies.eval_count + 1 END
`, strategyID, now, now, tier)
	return err
}

// SyncAllowlist records what is currently live in STRATEGY_ALLOWLIST.
// Marks missing previous live entries as retired. Does not edit any .env file.
func (s *Store) SyncAllowlist(entries []string, source string) (live, retired int, err error) {
	if source == "" {
		source = "sync-allowlist"
	}
	now := time.Now().UTC().Format(time.RFC3339)

	// Normalize + expand stack members so strategy_id is browseable.
	type item struct{ id, entry string }
	var items []item
	seenEntry := map[string]bool{}
	for _, raw := range entries {
		raw = strings.TrimSpace(raw)
		if raw == "" || seenEntry[raw] {
			continue
		}
		seenEntry[raw] = true
		primary := raw
		if i := strings.IndexByte(raw, '+'); i > 0 {
			primary = raw[:i]
		}
		items = append(items, item{id: primary, entry: raw})
	}

	want := map[string]bool{}
	for _, it := range items {
		want[it.entry] = true
		if _, err := s.db.Exec(`
INSERT INTO deployments (strategy_id, status, allowlist_entry, source, notes, changed_at)
VALUES (?, ?, ?, ?, ?, ?)`,
			it.id, DeployStatusLive, it.entry, source, "allowlist sync", now); err != nil {
			return live, retired, err
		}
		live++
		_ = s.TouchStrategy(it.id, "")
	}

	// Retire previously live entries not in the new set.
	rows, err := s.db.Query(`
SELECT allowlist_entry, strategy_id FROM v_deployed`)
	if err != nil {
		return live, retired, err
	}
	defer rows.Close()
	type prev struct{ entry, id string }
	var toRetire []prev
	for rows.Next() {
		var p prev
		if err := rows.Scan(&p.entry, &p.id); err != nil {
			return live, retired, err
		}
		if !want[p.entry] {
			toRetire = append(toRetire, p)
		}
	}
	if err := rows.Err(); err != nil {
		return live, retired, err
	}
	for _, p := range toRetire {
		if _, err := s.db.Exec(`
INSERT INTO deployments (strategy_id, status, allowlist_entry, source, notes, changed_at)
VALUES (?, ?, ?, ?, ?, ?)`,
			p.id, DeployStatusRetired, p.entry, source, "removed from allowlist sync", now); err != nil {
			return live, retired, err
		}
		retired++
	}
	return live, retired, nil
}

// ListStatus returns the lifecycle browser rows.
func (s *Store) ListStatus() ([]StrategyStatus, error) {
	// SQLite FULL OUTER JOIN may not exist on older builds — use a compatible query.
	q := `
SELECT
  strategy_id,
  lifecycle,
  tier,
  deployed,
  deploy_entry,
  oos_sharpe,
  oos_trades,
  oos_cagr,
  last_evaluated,
  deploy_notes
FROM (
  SELECT
    COALESCE(s.strategy_id, l.strategy_id) AS strategy_id,
    CASE
      WHEN d.strategy_id IS NOT NULL AND COALESCE(l.tier,'') IN ('C','D') THEN 'deployed_review'
      WHEN d.strategy_id IS NOT NULL THEN 'deployed'
      WHEN l.tier IS NOT NULL AND l.tier != '' THEN 'scored_' || l.tier
      WHEN s.eval_count > 0 THEN 'tested'
      ELSE 'registered'
    END AS lifecycle,
    COALESCE(l.tier, '') AS tier,
    CASE WHEN d.strategy_id IS NOT NULL THEN 1 ELSE 0 END AS deployed,
    COALESCE(d.allowlist_entry, '') AS deploy_entry,
    COALESCE(l.oos_sharpe, 0) AS oos_sharpe,
    COALESCE(l.oos_trades, 0) AS oos_trades,
    COALESCE(l.oos_cagr, 0) AS oos_cagr,
    COALESCE(l.created_at, '') AS last_evaluated,
    COALESCE(d.notes, '') AS deploy_notes
  FROM strategies s
  LEFT JOIN v_latest_scores l ON l.strategy_id = s.strategy_id
  LEFT JOIN (
    SELECT strategy_id, allowlist_entry, notes FROM v_deployed
  ) d ON d.strategy_id = s.strategy_id

  UNION

  SELECT
    l.strategy_id,
    CASE
      WHEN d.strategy_id IS NOT NULL AND l.tier IN ('C','D') THEN 'deployed_review'
      WHEN d.strategy_id IS NOT NULL THEN 'deployed'
      ELSE 'scored_' || l.tier
    END,
    l.tier,
    CASE WHEN d.strategy_id IS NOT NULL THEN 1 ELSE 0 END,
    COALESCE(d.allowlist_entry, ''),
    l.oos_sharpe, l.oos_trades, l.oos_cagr, l.created_at,
    COALESCE(d.notes, '')
  FROM v_latest_scores l
  LEFT JOIN strategies s ON s.strategy_id = l.strategy_id
  LEFT JOIN (SELECT strategy_id, allowlist_entry, notes FROM v_deployed) d
    ON d.strategy_id = l.strategy_id
  WHERE s.strategy_id IS NULL
)
ORDER BY
  CASE lifecycle
    WHEN 'deployed_review' THEN 0
    WHEN 'deployed' THEN 1
    WHEN 'scored_A' THEN 2
    WHEN 'scored_B' THEN 3
    WHEN 'scored_C' THEN 4
    WHEN 'scored_D' THEN 5
    ELSE 6
  END,
  oos_sharpe DESC,
  strategy_id
`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StrategyStatus
	for rows.Next() {
		var st StrategyStatus
		var dep int
		if err := rows.Scan(
			&st.StrategyID, &st.Lifecycle, &st.Tier, &dep, &st.DeployEntry,
			&st.OOSSharpe, &st.OOSTrades, &st.OOSCAGR, &st.LastEvaluated, &st.LastDeployNote,
		); err != nil {
			return nil, err
		}
		st.Deployed = dep == 1
		out = append(out, st)
	}
	return out, rows.Err()
}

// FormatStatusTable is a terminal-friendly lifecycle table.
func FormatStatusTable(rows []StrategyStatus) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%-28s %-16s %-4s %8s %7s %8s %s\n",
		"STRATEGY", "LIFECYCLE", "TIER", "OOS_SHP", "TRADES", "DEPLOYED", "LAST_EVAL"))
	b.WriteString(strings.Repeat("-", 100) + "\n")
	for _, r := range rows {
		dep := "no"
		if r.Deployed {
			dep = "YES"
		}
		b.WriteString(fmt.Sprintf("%-28s %-16s %-4s %8.2f %7d %8s %s\n",
			r.StrategyID, r.Lifecycle, r.Tier, r.OOSSharpe, r.OOSTrades, dep, r.LastEvaluated))
	}
	return b.String()
}

// BrowserHints documents how to open the ledger in GUI tools.
func BrowserHints(dbPath string) string {
	return fmt.Sprintf(`SQLite ledger: %s

Browse views (DB Browser for SQLite, Datasette, VS Code SQLite, etc.):
  SELECT * FROM v_strategy_status;
  SELECT * FROM v_latest_scores;
  SELECT * FROM v_deployed;
  SELECT * FROM v_promote_candidates;
  SELECT * FROM v_demote_candidates;

CLI:
  strateval status -db %s
  strateval sync-deployed -db %s -allowlist "$STRATEGY_ALLOWLIST"
`, dbPath, dbPath, dbPath)
}
