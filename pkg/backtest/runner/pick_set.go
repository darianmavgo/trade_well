package runner

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// PickSource is one family's finished backtest results.
type PickSource struct {
	Name       string // the family, for messages
	InSampleDB string // <run>/<family>.db
	HeldOutDB  string // <run>/oos/<family>.db
}

// PickOptions selects strategies out of the finished backtest results of one or more families.
type PickOptions struct {
	Sources      []PickSource
	Exclude      []string // strategy ids that may not be chosen
	OutDB        string   // the pick database: replaced on each run
	Count        int      // strategies to choose
	MinWinRate   float64  // in both windows
	MinTrades    int      // in-sample
	MinOOSTrades int      // held-out
	TopN         int      // candidates compared for overlap (the comparison is quadratic)
	MaxOverlap   float64  // largest entry-day overlap a chosen strategy may have with another
}

// Pick is one chosen strategy.
type Pick struct {
	Round      int
	Src        int    // index into PickOptions.Sources
	Family     string // that source's name
	ID         string
	ISWinRate  float64
	ISTrades   int
	ISCAGR     float64
	ISDD       float64
	OOSWinRate float64
	OOSTrades  int
	OOSCAGR    float64
	OOSDD      float64
	MaxOverlap float64 // largest overlap with a strategy chosen earlier
}

// PickResult is what PickSet found.
type PickResult struct {
	Candidates int // strategies that passed the win-rate and trade gates
	Compared   int // the best of them that were compared
	Picks      []Pick
	Set        *SetReport // the chosen strategies analysed together, from their standalone trades
}

// PickSet chooses up to opts.Count strategies from the families' results: each wins at least
// MinWinRate of its trades in-sample and held-out, and no two share more than MaxOverlap of their
// entry days. The gating, the overlap and each choice are the SQL stages pick_init, pick_gate,
// pick_top, pick_source_days, pick_overlap and pick_next. This only attaches the results, runs
// them and reads the answer. Strategy ids are unique across families.
func PickSet(ctx context.Context, opts PickOptions) (PickResult, error) {
	var res PickResult
	if opts.Count <= 0 {
		opts.Count = 10
	}
	if opts.TopN <= 0 {
		opts.TopN = 200
	}
	freshFile(opts.OutDB)
	db, err := storage.OpenSQLite(opts.OutDB)
	if err != nil {
		return res, err
	}
	defer db.Close()
	// ATTACH belongs to a connection, so the stages must all run on one.
	db.SetMaxOpenConns(1)
	if len(opts.Sources) == 0 {
		return res, fmt.Errorf("pick: no result databases to read")
	}
	for k, src := range opts.Sources {
		for _, a := range []struct{ file, name string }{{src.InSampleDB, pickAlias("i", k)}, {src.HeldOutDB, pickAlias("o", k)}} {
			if _, err := db.ExecContext(ctx, fmt.Sprintf("ATTACH DATABASE '%s' AS %s", strings.ReplaceAll(a.file, "'", "''"), a.name)); err != nil {
				return res, fmt.Errorf("attach %s: %w", a.file, err)
			}
		}
	}
	repl := map[string]string{
		"__MIN_WIN__":        strconv.FormatFloat(opts.MinWinRate, 'f', -1, 64),
		"__MIN_TRADES__":     strconv.Itoa(opts.MinTrades),
		"__MIN_OOS_TRADES__": strconv.Itoa(opts.MinOOSTrades),
		"__TOP__":            strconv.Itoa(opts.TopN),
		"__MAX_OVERLAP__":    strconv.FormatFloat(opts.MaxOverlap, 'f', -1, 64),
	}
	// perSource runs a stage once for each source, with that source's databases.
	perSource := func(stage string) error {
		for k := range opts.Sources {
			repl["__SRC__"] = strconv.Itoa(k)
			repl["__INS__"] = pickAlias("i", k)
			repl["__OOS__"] = pickAlias("o", k)
			if err := storage.RunStage(db, stage, repl); err != nil {
				return err
			}
		}
		return nil
	}
	if err := storage.RunStage(db, "pick_init", repl); err != nil {
		return res, err
	}
	for _, id := range opts.Exclude {
		if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO pick_exclude (strategy_id) VALUES (?)`, id); err != nil {
			return res, err
		}
	}
	if err := perSource("pick_gate"); err != nil {
		return res, err
	}
	if err := storage.RunStage(db, "pick_top", repl); err != nil {
		return res, err
	}
	if err := perSource("pick_source_days"); err != nil {
		return res, err
	}
	if err := storage.RunStage(db, "pick_overlap", repl); err != nil {
		return res, err
	}
	if err := db.GetContext(ctx, &res.Candidates, `SELECT COUNT(*) FROM pick_candidate`); err != nil {
		return res, err
	}
	if err := db.GetContext(ctx, &res.Compared, `SELECT COUNT(*) FROM pick_top`); err != nil {
		return res, err
	}
	for round := 1; round <= opts.Count; round++ {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		repl["__ROUND__"] = strconv.Itoa(round)
		if err := storage.RunStage(db, "pick_next", repl); err != nil {
			return res, err
		}
		var n int
		if err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM pick`); err != nil {
			return res, err
		}
		if n < round {
			break // nothing left that stays under MaxOverlap
		}
	}
	if err := db.SelectContext(ctx, &res.Picks, `
		SELECT p.round, c.src, p.strategy_id AS id, c.is_win_rate AS iswinrate, c.is_trades AS istrades, c.is_cagr AS iscagr, c.is_dd AS isdd,
		       c.oos_win_rate AS ooswinrate, c.oos_trades AS oostrades, c.oos_cagr AS ooscagr, c.oos_dd AS oosdd, p.max_overlap AS maxoverlap
		FROM pick p JOIN pick_top c ON c.strategy_id = p.strategy_id ORDER BY p.round`); err != nil {
		return res, err
	}
	for i := range res.Picks {
		res.Picks[i].Family = opts.Sources[res.Picks[i].Src].Name
	}
	if len(res.Picks) == 0 {
		return res, nil
	}
	rep, err := analyzePicks(ctx, db, res.Picks, len(opts.Sources))
	if err != nil {
		return res, err
	}
	res.Set = &rep
	return res, nil
}

// tradeRow is one standalone trade read back for the set analysis.
type tradeRow struct {
	ID        string  `db:"strategy_id"`
	Symbol    string  `db:"symbol"`
	Entry     string  `db:"entry_date"`
	Exit      string  `db:"exit_date"`
	NetPnL    float64 `db:"net_pnl"`
	ReturnPct float64 `db:"return_pct"`
	HoldDays  int     `db:"hold_days"`
}

// pickAlias names the attached database of source k: i0, o0, i1, o1...
func pickAlias(prefix string, k int) string { return prefix + strconv.Itoa(k) }

// analyzePicks runs the set analysis on the chosen strategies, from the days they entered and the
// trades they took on their own (not in a shared account).
func analyzePicks(ctx context.Context, db *sqlx.DB, picks []Pick, sources int) (SetReport, error) {
	set := make([]SetStrategy, len(picks))
	index := map[string]int{}
	for i, p := range picks {
		role := SetMember
		if i == 0 {
			role = SetPrimary
		}
		set[i] = SetStrategy{ID: p.ID, Role: role}
		index[p.ID] = i
	}
	var days []struct {
		ID   string `db:"strategy_id"`
		Date string `db:"entry_date"`
	}
	if err := db.SelectContext(ctx, &days, `SELECT d.strategy_id, d.entry_date FROM pick_entry_day d JOIN pick p ON p.strategy_id = d.strategy_id`); err != nil {
		return SetReport{}, err
	}
	for _, d := range days {
		set[index[d.ID]].EntryDates = append(set[index[d.ID]].EntryDates, d.Date)
	}
	var rows []tradeRow
	const cols = `t.strategy_id, t.symbol, t.entry_date, t.exit_date, t.net_pnl, t.return_pct, t.hold_days`
	for k := 0; k < sources; k++ {
		var part []tradeRow
		q := `SELECT ` + cols + ` FROM %s.trades t JOIN pick p ON p.strategy_id = t.strategy_id
			UNION ALL SELECT ` + cols + ` FROM %s.trades t JOIN pick p ON p.strategy_id = t.strategy_id`
		if err := db.SelectContext(ctx, &part, fmt.Sprintf(q, pickAlias("i", k), pickAlias("o", k))); err != nil {
			return SetReport{}, err
		}
		rows = append(rows, part...)
	}
	trades := make([]models.Trade, len(rows))
	for i, r := range rows {
		trades[i] = models.Trade{StrategyID: r.ID, Symbol: r.Symbol, EntryDate: r.Entry, ExitDate: r.Exit, NetPnL: r.NetPnL, ReturnPct: r.ReturnPct, HoldDays: r.HoldDays}
	}
	return AnalyzeSet(db, set, trades)
}

// PrintPickResult prints what PickSet found.
func PrintPickResult(res PickResult, opts PickOptions) {
	fmt.Printf("\nPICK — win rate >= %.0f%% in-sample and held-out, entry-day overlap <= %.0f%%\n", opts.MinWinRate*100, opts.MaxOverlap*100)
	fmt.Printf("   %d strategies pass the win-rate and trade gates, the best %d were compared, %d chosen (wanted %d).\n",
		res.Candidates, res.Compared, len(res.Picks), opts.Count)
	if len(res.Picks) > 0 {
		fmt.Printf("   %-3s %-9s %-32s %8s %6s %8s %8s %6s %8s %12s\n", "#", "family", "strategy", "IS win %", "IS n", "IS CAGR", "OOS win %", "OOS n", "OOS CAGR", "max overlap")
		for _, p := range res.Picks {
			fmt.Printf("   %-3d %-9s %-32s %7.1f%% %6d %7.1f%% %8.1f%% %6d %7.1f%% %11.1f%%\n",
				p.Round, p.Family, p.ID, p.ISWinRate*100, p.ISTrades, p.ISCAGR*100, p.OOSWinRate*100, p.OOSTrades, p.OOSCAGR*100, p.MaxOverlap*100)
		}
	}
	if res.Set != nil {
		PrintSetReport(*res.Set)
	}
	fmt.Printf("   Pick database (every slice table): %s\n", opts.OutDB)
}
