package runner

import (
	"fmt"
	"os"
	"sort"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// Roles of a strategy in a set under analysis.
const (
	SetPrimary   = "primary"   // the strategy the others are measured against
	SetMember    = "member"    // chosen together with the primary
	SetCandidate = "candidate" // tried against the primary, not chosen
)

// SetStrategy is one strategy of a set: its role and the days it signals an entry.
type SetStrategy struct {
	ID         string
	Role       string
	EntryDates []string
}

// SetPairOverlap is how much two strategies' entry days coincide.
type SetPairOverlap struct {
	A, B       string
	Kind       string // "member" or "candidate"
	ADays      int
	BDays      int
	SharedDays int
	OverlapPct float64 // shared days over the smaller strategy's days
	Jaccard    float64
}

// SetStrategyStat is a strategy's signal frequency and the trades the shared account took from it.
type SetStrategyStat struct {
	ID             string
	Role           string
	EntryDays      int
	EntriesPerYear float64
	Trades         int
	Wins           int
	WinRate        float64
	AvgReturnPct   float64 // a fraction, as models.Trade.ReturnPct
	WorstReturnPct float64
	AvgHoldDays    float64
	NetPnL         float64
}

// SetReport is what AnalyzeSet calculates about a set of strategies. The calculation is the SQL
// stage sql/stages/set_analysis. This only reads its slice tables.
type SetReport struct {
	SignalDays    int
	Signals       int
	CollisionDays int     // days on which two or more members signal
	CollisionPct  float64 // collision days over signal days
	MaxPerDay     int
	FirstDay      string
	LastDay       string
	Pairs         int // member pairs
	AvgOverlapPct float64
	MaxOverlapPct float64
	AvgJaccard    float64
	Trades        int
	Wins          int
	WinRate       float64
	AvgReturnPct  float64
	WorstReturn   float64
	NetPnL        float64

	Members    []SetPairOverlap  // every pair among the primary and the chosen members
	Candidates []SetPairOverlap  // the primary against each candidate, least overlap first
	Stats      []SetStrategyStat // the primary and the chosen members
}

// AnalyzeSet loads the set into db (set_member, set_signal, set_trade), runs the set_analysis
// stage and reads the result. db should be a fresh database: the tables are dropped and
// recreated, and the stage's slice tables stay in it for inspection. trades are the trades the
// shared account took for the primary and the chosen members.
func AnalyzeSet(db *sqlx.DB, set []SetStrategy, trades []models.Trade) (SetReport, error) {
	var rep SetReport
	if _, err := db.Exec(`
		DROP TABLE IF EXISTS set_member;
		DROP TABLE IF EXISTS set_signal;
		DROP TABLE IF EXISTS set_trade;
		CREATE TABLE set_member (strategy_id TEXT PRIMARY KEY, role TEXT NOT NULL);
		CREATE TABLE set_signal (strategy_id TEXT NOT NULL, entry_date TEXT NOT NULL);
		CREATE TABLE set_trade (strategy_id TEXT NOT NULL, symbol TEXT, entry_date TEXT, exit_date TEXT,
			net_pnl REAL, return_pct REAL, hold_days INTEGER);`); err != nil {
		return rep, fmt.Errorf("set analysis tables: %w", err)
	}
	tx, err := db.Beginx()
	if err != nil {
		return rep, err
	}
	for _, s := range set {
		if _, err := tx.Exec(`INSERT INTO set_member (strategy_id, role) VALUES (?, ?)`, s.ID, s.Role); err != nil {
			_ = tx.Rollback()
			return rep, err
		}
		for _, d := range s.EntryDates {
			if _, err := tx.Exec(`INSERT INTO set_signal (strategy_id, entry_date) VALUES (?, ?)`, s.ID, d); err != nil {
				_ = tx.Rollback()
				return rep, err
			}
		}
	}
	for _, t := range trades {
		if _, err := tx.Exec(`INSERT INTO set_trade VALUES (?, ?, ?, ?, ?, ?, ?)`,
			t.StrategyID, t.Symbol, t.EntryDate, t.ExitDate, t.NetPnL, t.ReturnPct, t.HoldDays); err != nil {
			_ = tx.Rollback()
			return rep, err
		}
	}
	if err := tx.Commit(); err != nil {
		return rep, err
	}
	if err := storage.RunStage(db, "set_analysis", nil); err != nil {
		return rep, err
	}
	return readSetReport(db)
}

func readSetReport(db *sqlx.DB) (SetReport, error) {
	var rep SetReport
	var sum struct {
		SignalDays    int     `db:"signal_days"`
		Signals       *int    `db:"signals"`
		CollisionDays *int    `db:"collision_days"`
		CollisionPct  float64 `db:"collision_pct"`
		MaxPerDay     *int    `db:"max_per_day"`
		FirstDay      *string `db:"first_day"`
		LastDay       *string `db:"last_day"`
	}
	if err := db.Get(&sum, `SELECT * FROM set_summary`); err != nil {
		return rep, err
	}
	rep.SignalDays, rep.CollisionPct = sum.SignalDays, sum.CollisionPct
	if sum.Signals != nil {
		rep.Signals = *sum.Signals
	}
	if sum.CollisionDays != nil {
		rep.CollisionDays = *sum.CollisionDays
	}
	if sum.MaxPerDay != nil {
		rep.MaxPerDay = *sum.MaxPerDay
	}
	if sum.FirstDay != nil {
		rep.FirstDay = *sum.FirstDay
	}
	if sum.LastDay != nil {
		rep.LastDay = *sum.LastDay
	}
	var ps struct {
		Pairs   int      `db:"pairs"`
		Avg     *float64 `db:"avg_overlap_pct"`
		Max     *float64 `db:"max_overlap_pct"`
		Jaccard *float64 `db:"avg_jaccard"`
	}
	if err := db.Get(&ps, `SELECT * FROM set_pair_summary`); err != nil {
		return rep, err
	}
	rep.Pairs = ps.Pairs
	if ps.Avg != nil {
		rep.AvgOverlapPct, rep.MaxOverlapPct, rep.AvgJaccard = *ps.Avg, *ps.Max, *ps.Jaccard
	}
	var ts struct {
		Trades int      `db:"trades"`
		Wins   *int     `db:"wins"`
		Win    float64  `db:"win_rate"`
		Avg    *float64 `db:"avg_return_pct"`
		Worst  *float64 `db:"worst_return_pct"`
		Net    *float64 `db:"net_pnl"`
	}
	if err := db.Get(&ts, `SELECT * FROM set_trade_summary`); err != nil {
		return rep, err
	}
	rep.Trades, rep.WinRate = ts.Trades, ts.Win
	if ts.Wins != nil {
		rep.Wins, rep.AvgReturnPct, rep.WorstReturn, rep.NetPnL = *ts.Wins, *ts.Avg, *ts.Worst, *ts.Net
	}
	const pairCols = `SELECT a_id AS a, b_id AS b, kind, a_days AS adays, b_days AS bdays, shared_days AS shareddays, overlap_pct AS overlappct, jaccard FROM set_pair_overlap`
	if err := db.Select(&rep.Members, pairCols+` WHERE kind = 'member' ORDER BY overlap_pct DESC, a_id, b_id`); err != nil {
		return rep, err
	}
	if err := db.Select(&rep.Candidates, pairCols+` WHERE kind = 'candidate' ORDER BY overlap_pct, b_days DESC, b_id`); err != nil {
		return rep, err
	}
	if err := db.Select(&rep.Stats, `
		SELECT d.strategy_id AS id, d.role, d.entry_days AS entrydays, d.entries_per_year AS entriesperyear,
		       COALESCE(s.trades, 0) AS trades, COALESCE(s.wins, 0) AS wins, COALESCE(s.win_rate, 0) AS winrate,
		       COALESCE(s.avg_return_pct, 0) AS avgreturnpct, COALESCE(s.worst_return_pct, 0) AS worstreturnpct,
		       COALESCE(s.avg_hold_days, 0) AS avgholddays, COALESCE(s.net_pnl, 0) AS netpnl
		FROM set_strategy_days d LEFT JOIN set_strategy_stats s ON s.strategy_id = d.strategy_id
		WHERE d.role <> 'candidate' ORDER BY d.role DESC, d.strategy_id`); err != nil {
		return rep, err
	}
	return rep, nil
}

// entryDates returns the sorted distinct days on which sigs open a position.
func entryDates(sigs []models.Signal) []string {
	seen := map[string]bool{}
	for _, s := range sigs {
		if s.Entry == 1 && len(s.Date) >= 10 {
			seen[s.Date[:10]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// freshFile removes a database file and its journal files so the next open starts empty.
func freshFile(path string) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(path + suffix)
	}
}

// PrintSetReport prints the entry-collision and trade statistics of a set.
func PrintSetReport(r SetReport) {
	fmt.Printf("\nSET ANALYSIS — the primary and the chosen overlays (signals, not fills)\n")
	fmt.Printf("   Signal days: %d (%s to %s), signals: %d. Days with 2+ strategies signalling: %d (%.1f%%), most on one day: %d.\n",
		r.SignalDays, r.FirstDay, r.LastDay, r.Signals, r.CollisionDays, r.CollisionPct*100, r.MaxPerDay)
	if r.Pairs > 0 {
		fmt.Printf("   Pair overlap (shared days over the smaller strategy's days): average %.1f%%, worst %.1f%%, average Jaccard %.2f over %d pairs.\n",
			r.AvgOverlapPct*100, r.MaxOverlapPct*100, r.AvgJaccard, r.Pairs)
	}
	if r.Trades > 0 {
		fmt.Printf("   Trades the shared account took: %d, win rate %.1f%% (%d wins), average return %.2f%%, worst %.2f%%, net P&L $%.2f.\n",
			r.Trades, r.WinRate*100, r.Wins, r.AvgReturnPct*100, r.WorstReturn*100, r.NetPnL)
	}
	if len(r.Stats) > 0 {
		fmt.Printf("   %-34s %-8s %6s %8s %6s %7s %9s %9s\n", "strategy", "role", "days", "per year", "trades", "win %", "avg ret %", "worst %")
		for _, s := range r.Stats {
			fmt.Printf("   %-34s %-8s %6d %8.1f %6d %6.1f%% %9.2f %9.2f\n",
				s.ID, s.Role, s.EntryDays, s.EntriesPerYear, s.Trades, s.WinRate*100, s.AvgReturnPct*100, s.WorstReturnPct*100)
		}
	}
	if len(r.Members) > 0 {
		fmt.Printf("   Pairs, most overlapping first:\n")
		for _, p := range r.Members {
			fmt.Printf("     %-30s x %-30s shared %4d of %d and %d days, overlap %5.1f%%, Jaccard %.2f\n",
				p.A, p.B, p.SharedDays, p.ADays, p.BDays, p.OverlapPct*100, p.Jaccard)
		}
	}
	if len(r.Candidates) > 0 {
		n := len(r.Candidates)
		if n > 10 {
			n = 10
		}
		fmt.Printf("   Candidates least overlapping the primary (of %d):\n", len(r.Candidates))
		for _, p := range r.Candidates[:n] {
			fmt.Printf("     %-40s %4d days, shared %4d with the primary, overlap %5.1f%%\n", p.B, p.BDays, p.SharedDays, p.OverlapPct*100)
		}
	}
}
