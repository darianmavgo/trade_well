// Package live_vs_backtest_winrate puts a strategy's win rate side by side in three places: the backtest
// in-sample window, the backtest out-of-sample window, and live closed trades. It also
// gives the backtest's own rate on the same trades that traded live, so a live shortfall
// can be told apart as fills (live below model) or edge (model below backtest).
//
// Go opens the databases and runs the SQL files in sql/; every number is calculated there,
// one slice table per stage. Refresh copies the newest evaluation of each strategy from a
// backtestgosqlite strategy_evals database into live_vs_backtest_winrate in the orchestrator
// database, so the cloud service can show the backtest side without that file. Compare
// reads only the orchestrator database.
package live_vs_backtest_winrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/runtables"
)

// JobName is the CLI subcommand, the /live_vs_backtest_winrate route and the log prefix.
const JobName = "live_vs_backtest_winrate"

// MinLive is the number of closed live trades before a verdict other than TOO_EARLY.
// It is the same number as in sql/06_compare.sql.
const MinLive = 10

// Verdicts.
const (
	NoBacktest = "NO_BACKTEST"
	TooEarly   = "TOO_EARLY"
	Below      = "BELOW"
	Above      = "ABOVE"
	InLine     = "IN_LINE"
)

//go:embed sql/*.sql
var sqlFiles embed.FS

// Row is one strategy. Win rates are fractions 0 to 1; nil means no data for that column.
type Row struct {
	Strategy   string   `json:"strategy"`
	Verdict    string   `json:"verdict"`
	Greenlit   bool     `json:"greenlit"`
	ISWinRate  *float64 `json:"is_win_rate"`
	ISTrades   int      `json:"is_trades"`
	OOSWinRate *float64 `json:"oos_win_rate"`
	OOSTrades  int      `json:"oos_trades"`
	// AllWinRate is the strategy's last whole-window backtest, with no in/out-of-sample split.
	AllWinRate  *float64 `json:"backtest_win_rate"`
	AllTrades   int      `json:"backtest_trades"`
	LiveWinRate *float64 `json:"live_win_rate"`
	LiveWins    int      `json:"live_wins"`
	// LiveDecisions is the number of closed live decisions (a strategy buying a symbol on a day,
	// however many accounts it filled in), LiveWins the ones with a positive combined P&L, and
	// LiveFills the account trades behind them.
	LiveDecisions int      `json:"live_decisions"`
	LiveFills     int      `json:"live_fills"`
	LiveCILow     *float64 `json:"live_ci_low"`
	LiveCIHigh    *float64 `json:"live_ci_high"`
	// ModelWinRate is the backtest engine replayed on the trades that traded live.
	ModelWinRate  *float64 `json:"model_win_rate"`
	ModelTrades   int      `json:"model_trades"`
	BaseKind      string   `json:"baseline"` // "oos", "is" or "all": what live is judged against
	OpenPositions int      `json:"open_positions"`
	PendingFills  int      `json:"pending_fills"`
	LiveMinusBase *float64 `json:"live_minus_baseline"`
	LiveMinusMod  *float64 `json:"live_minus_model"`
	LiveAvgReturn *float64 `json:"live_avg_return"`
	FirstClose    string   `json:"first_close,omitempty"`
	LastClose     string   `json:"last_close,omitempty"`
	EvaluatedAt   string   `json:"backtest_evaluated_at,omitempty"`
	// BacktestSource says where the backtest rates came from: bundled with the deploy, or a refresh.
	BacktestSource string `json:"backtest_source,omitempty"`
	// Progress is the running live win rate after each closed trade, oldest first.
	Progress []Point `json:"progress"`
	// ISProgress and OOSProgress are the same curve for the backtest's two passes.
	ISProgress  []BTPoint `json:"is_progress"`
	OOSProgress []BTPoint `json:"oos_progress"`
}

// BTPoint is the backtest win rate after the N-th trade of a pass.
type BTPoint struct {
	N      int     `json:"n"`
	Date   string  `json:"date"`
	Symbol string  `json:"symbol"`
	Win    bool    `json:"win"`
	Return float64 `json:"return_pct"`
	Rate   float64 `json:"win_rate"`
}

// Point is the live win rate after the N-th closed trade, with its 95% interval.
type Point struct {
	N      int     `json:"n"`
	Date   string  `json:"date"`
	Symbol string  `json:"symbol"`
	Win    bool    `json:"win"`
	Return float64 `json:"return_pct"`
	Wins   int     `json:"wins"`
	Rate   float64 `json:"win_rate"`
	CILow  float64 `json:"ci_low"`
	CIHigh float64 `json:"ci_high"`
}

// EnsureSchema creates live_vs_backtest_winrate and adds columns a older copy lacks.
func EnsureSchema(db *sql.DB) error {
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS live_vs_backtest_winrate (
	strategy_id TEXT PRIMARY KEY,
	is_start TEXT, is_end TEXT, oos_start TEXT, oos_end TEXT,
	is_trades INTEGER NOT NULL DEFAULT 0,
	is_win_rate REAL,
	oos_trades INTEGER NOT NULL DEFAULT 0,
	oos_win_rate REAL,
	evaluated_at TEXT,
	synced_at TEXT NOT NULL
)`); err != nil {
		return err
	}
	for _, c := range [][2]string{{"all_trades", "INTEGER NOT NULL DEFAULT 0"}, {"all_win_rate", "REAL"}, {"all_evaluated_at", "TEXT"}} {
		var have int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('live_vs_backtest_winrate') WHERE name = ?`, c[0]).Scan(&have); err != nil {
			return err
		}
		if have == 0 {
			if _, err := db.Exec(`ALTER TABLE live_vs_backtest_winrate ADD COLUMN ` + c[0] + ` ` + c[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// Sources are the backtest files Refresh reads. Either may be empty.
//
//	EvalsDB  a strategy_evals database: in-sample and out-of-sample win rates
//	RefDB    a refdata strategies.db: each strategy's last whole-window backtest
//	Reports  a backtest reports folder: <run>/*.db is each run's in-sample pass and
//	         <run>/oos/*.db its held-out pass; the newest run of a strategy wins
type Sources struct {
	EvalsDB string
	RefDB   string
	Reports string
	// IDs limits the copy to these strategies. Empty copies every strategy in the files, which for
	// a refdata strategies.db is tens of thousands.
	IDs []string
}

// Refresh copies the backtest win rates in src into live_vs_backtest_winrate and returns how many
// strategies it holds afterwards (with dryRun, how many it found to copy). With dryRun it
// reads the files and changes nothing.
//
// The files are backtest ASSETS built in backtestgosqlite. Each is read once, read-only, for the
// few columns the comparison needs (sql/asset_*.sql); the rows are then upserted into Turso in
// one transaction. Nothing is staged in a local database.
func Refresh(ctx context.Context, db *sql.DB, src Sources, dryRun bool) (int, error) {
	if src.EvalsDB == "" && src.RefDB == "" && src.Reports == "" {
		return 0, fmt.Errorf("live_vs_backtest_winrate: nothing to refresh from: give a reports folder, an evaluations database or a refdata strategies.db")
	}
	if src.RefDB != "" && len(src.IDs) == 0 {
		return 0, fmt.Errorf("live_vs_backtest_winrate: a refdata strategies.db holds tens of thousands of strategies: name the ones to copy")
	}
	for _, p := range []string{src.EvalsDB, src.RefDB, src.Reports} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			return 0, fmt.Errorf("live_vs_backtest_winrate: %w", err)
		}
	}
	wanted := func(id string) bool { return len(src.IDs) == 0 || slices.Contains(src.IDs, id) }
	synced := time.Now().UTC().Format(time.RFC3339)
	type upsert struct {
		file string
		args []any
	}
	var ups []upsert
	found := 0
	if src.EvalsDB != "" {
		rows, err := readStrategyEvalsAssetLatest(ctx, src.EvalsDB)
		if err != nil {
			return 0, err
		}
		for _, r := range rows {
			if wanted(r.ID) {
				found++
				ups = append(ups, upsert{"upsert_evals.sql", []any{r.ID, r.ISStart, r.ISEnd, r.OOSStart, r.OOSEnd, r.ISTrades, r.ISWinRate, r.OOSTrades, r.OOSWinRate, r.EvaluatedAt, synced}})
			}
		}
	}
	if src.RefDB != "" {
		rows, _, err := readStrategyLastBacktestAsset(ctx, src.RefDB)
		if err != nil {
			return 0, err
		}
		for _, r := range rows {
			if wanted(r.ID) {
				found++
				ups = append(ups, upsert{"upsert_last_backtest.sql", []any{r.ID, r.Trades, r.WinRate, r.CreatedAt, synced}})
			}
		}
	}
	if src.Reports != "" {
		files, err := reportDBs(src.Reports)
		if err != nil {
			return 0, err
		}
		for _, f := range files {
			rows, err := readPerformanceSummaryAsset(ctx, f.path)
			if err != nil {
				return 0, err
			}
			for _, r := range rows {
				if wanted(r.ID) {
					found++
					ups = append(ups, upsert{"upsert_summary_" + f.pass + ".sql", []any{r.ID, r.Start, r.End, r.Trades, r.WinRate, r.CreatedAt, synced}})
				}
			}
		}
	}
	if dryRun {
		return found, nil
	}
	if err := EnsureSchema(db); err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, u := range ups {
		if _, err := tx.ExecContext(ctx, assetQuery(u.file), u.args...); err != nil {
			return 0, fmt.Errorf("live_vs_backtest_winrate: %s: %w", u.file, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	var n int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM live_vs_backtest_winrate`).Scan(&n)
	return n, err
}

// Options say what Compare should list beyond the strategies that have data.
type Options struct {
	// IDs are the greenlit strategies; each gets a row even with no data yet.
	IDs []string
	// RefDB is an optional refdata strategies.db read in place for whole-window backtest rates
	// that no refresh has copied yet. A missing file is ignored.
	RefDB string
	// MaxAge lets Compare read the tables of an earlier run with the same IDs and RefDB, if one
	// finished within this long, instead of building new ones. Zero always builds. A page that is
	// viewed again and again sets it; the CLI leaves it zero.
	MaxAge time.Duration
}

// Compare returns one row per greenlit strategy, and per other strategy that has traded live, worst
// verdict first. The calculation runs in Turso: each stage writes a slice table of its own for
// this run (see runtables), the newest few runs are kept for audit, and the tables it reads are
// never changed. A database with no live_vs_backtest_winrate or no live_vs_backtest_trade yet is
// treated as having no rows on that side.
func Compare(ctx context.Context, db *sql.DB, opt Options) ([]Row, error) {
	key := compareKey(opt)
	if run, ok, err := runtables.FindRecent(ctx, db, stageTables, "winrate_done", key, opt.MaxAge); err != nil {
		return nil, err
	} else if ok {
		return readCompare(ctx, run)
	}
	run := runtables.BeginKeyed(db, stageTables, key)
	defer func() { _ = runtables.Prune(context.WithoutCancel(ctx), db, stageTables, keepRuns) }()

	if err := run.Exec(ctx, `DROP TABLE IF EXISTS winrate_ids; CREATE TABLE winrate_ids (strategy_id TEXT PRIMARY KEY)`); err != nil {
		return nil, err
	}
	ids := make([][]any, len(opt.IDs))
	for i, id := range opt.IDs {
		ids[i] = []any{id}
	}
	if err := run.InsertRows(ctx, "winrate_ids", []string{"strategy_id"}, ids); err != nil {
		return nil, err
	}
	// Whole-window backtest rates read from a refdata strategies.db (a backtest ASSET) for the
	// strategies no refresh has copied yet. A missing file is ignored.
	var refs []lastBacktestAssetRow
	if opt.RefDB != "" {
		if _, err := os.Stat(opt.RefDB); err == nil {
			if refs, _, err = readStrategyLastBacktestAsset(ctx, opt.RefDB); err != nil {
				return nil, err
			}
		}
	}
	if err := loadBaseline(ctx, run); err != nil {
		return nil, err
	}
	var stages []string
	for _, st := range []struct {
		have      func() (bool, error)
		full, nil string
	}{
		{func() (bool, error) { return hasTable(ctx, db, "live_vs_backtest_winrate") }, "sql/02_slice_backtest.sql", "sql/02_slice_backtest_empty.sql"},
		{func() (bool, error) { return hasTable(ctx, db, "manage_exit") }, "sql/04_slice_decisions.sql", "sql/04_slice_decisions_empty.sql"},
		{func() (bool, error) { return hasTable(ctx, db, "manage_exit") }, "sql/04_slice_holding.sql", "sql/04_slice_holding_empty.sql"},
		{func() (bool, error) { return hasTable(ctx, db, "live_vs_backtest_trade") }, "sql/05_slice_model.sql", "sql/05_slice_model_empty.sql"},
	} {
		ok, err := st.have()
		if err != nil {
			return nil, err
		}
		if ok {
			stages = append(stages, st.full)
		} else {
			stages = append(stages, st.nil)
		}
	}
	// The baseline overlay goes right after the stored backtest slice it extends.
	stages = slices.Insert(stages, 1, "sql/02b_overlay_baseline.sql", "sql/03_slice_refs.sql")
	// Live results are always derived from the decisions slice, so they need no empty variant.
	stages = slices.Insert(stages, 5, "sql/04_slice_live.sql", "sql/04_slice_trades.sql")
	if ok, err := hasTable(ctx, db, "live_vs_backtest_deployed_trade"); err != nil {
		return nil, err
	} else if ok {
		stages = append(stages, "sql/11_slice_bt_trades_stored.sql")
	} else {
		stages = append(stages, "sql/11_slice_bt_trades_empty.sql")
	}
	stages = append(stages, "sql/12_overlay_bt_trades.sql", "sql/13_slice_bt_progress.sql", "sql/06_slice_join.sql", "sql/07_slice_interval.sql", "sql/08_compare.sql", "sql/09_slice_progress_count.sql", "sql/10_slice_progress.sql")
	for _, f := range stages {
		if err := runFile(ctx, run, f); err != nil {
			return nil, err
		}
		if f == "sql/03_slice_refs.sql" {
			var rows [][]any
			for _, r := range refs {
				if slices.Contains(opt.IDs, r.ID) {
					rows = append(rows, []any{r.ID, r.Trades, r.WinRate, r.CreatedAt})
				}
			}
			if err := run.InsertRows(ctx, "winrate_refs", []string{"strategy_id", "all_trades", "all_win_rate", "all_evaluated_at"}, rows); err != nil {
				return nil, err
			}
		}
	}
	// The marker goes last: a run without it is unfinished and is never reused.
	if err := run.Exec(ctx, `CREATE TABLE winrate_done (built_at TEXT)`); err != nil {
		return nil, err
	}
	return readCompare(ctx, run)
}

// compareKey sums up what Compare was asked, so a finished run can answer the same question again.
func compareKey(opt Options) string {
	ids := slices.Clone(opt.IDs)
	sort.Strings(ids)
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s", strings.Join(ids, ","), opt.RefDB)
	if st, err := os.Stat(opt.RefDB); opt.RefDB != "" && err == nil {
		fmt.Fprintf(h, "\x00%d\x00%d", st.Size(), st.ModTime().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}

// readCompare reads the finished comparison out of a run's tables.
func readCompare(ctx context.Context, run *runtables.Run) ([]Row, error) {
	rows, err := run.Query(ctx, `
SELECT strategy_id, verdict, greenlit, is_win_rate, COALESCE(is_trades, 0), oos_win_rate, COALESCE(oos_trades, 0),
	all_win_rate, COALESCE(all_trades, 0),
	live_win_rate, live_wins, live_n, live_fills, live_ci_low, live_ci_high, model_win_rate, model_n,
	COALESCE(base_kind, ''), live_minus_base, live_minus_model, live_avg_return, open_n, pending_n,
	COALESCE(first_close, ''), COALESCE(last_close, ''), COALESCE(evaluated_at, all_evaluated_at, ''), COALESCE(source, '')
FROM winrate_compare
WHERE greenlit OR live_n > 0 OR open_n > 0 OR pending_n > 0
ORDER BY CASE verdict WHEN 'BELOW' THEN 0 WHEN 'IN_LINE' THEN 1 WHEN 'ABOVE' THEN 2 WHEN 'TOO_EARLY' THEN 3 ELSE 4 END,
	live_n DESC, strategy_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var is, oos, all, lw, lo, hi, mw, d1, d2, avg sql.NullFloat64
		if err := rows.Scan(&r.Strategy, &r.Verdict, &r.Greenlit, &is, &r.ISTrades, &oos, &r.OOSTrades, &all, &r.AllTrades,
			&lw, &r.LiveWins, &r.LiveDecisions, &r.LiveFills, &lo, &hi, &mw, &r.ModelTrades,
			&r.BaseKind, &d1, &d2, &avg, &r.OpenPositions, &r.PendingFills,
			&r.FirstClose, &r.LastClose, &r.EvaluatedAt, &r.BacktestSource); err != nil {
			return nil, err
		}
		r.ISWinRate, r.OOSWinRate, r.AllWinRate, r.LiveWinRate = ptr(is), ptr(oos), ptr(all), ptr(lw)
		r.LiveCILow, r.LiveCIHigh, r.ModelWinRate = ptr(lo), ptr(hi), ptr(mw)
		r.LiveMinusBase, r.LiveMinusMod, r.LiveAvgReturn = ptr(d1), ptr(d2), ptr(avg)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	prog, err := run.Query(ctx, `SELECT strategy_id, n, COALESCE(exit_date, ''), COALESCE(symbol, ''), win, COALESCE(pnl_pct, 0), wins, rate, ci_low, ci_high
		FROM winrate_progress ORDER BY strategy_id, n`)
	if err != nil {
		return nil, err
	}
	defer prog.Close()
	at := map[string]int{}
	for i, r := range out {
		at[r.Strategy] = i
	}
	for prog.Next() {
		var id string
		var p Point
		var win int
		if err := prog.Scan(&id, &p.N, &p.Date, &p.Symbol, &win, &p.Return, &p.Wins, &p.Rate, &p.CILow, &p.CIHigh); err != nil {
			return nil, err
		}
		p.Win = win == 1
		if i, ok := at[id]; ok {
			out[i].Progress = append(out[i].Progress, p)
		}
	}
	if err := prog.Err(); err != nil {
		return nil, err
	}
	bt, err := run.Query(ctx, `SELECT strategy_id, pass, n, COALESCE(exit_date, ''), COALESCE(symbol, ''), win, COALESCE(return_pct, 0), rate
		FROM winrate_bt_progress ORDER BY strategy_id, pass, n`)
	if err != nil {
		return nil, err
	}
	defer bt.Close()
	for bt.Next() {
		var id, pass string
		var p BTPoint
		var win int
		if err := bt.Scan(&id, &pass, &p.N, &p.Date, &p.Symbol, &win, &p.Return, &p.Rate); err != nil {
			return nil, err
		}
		p.Win = win == 1
		if i, ok := at[id]; ok {
			if pass == "is" {
				out[i].ISProgress = append(out[i].ISProgress, p)
			} else {
				out[i].OOSProgress = append(out[i].OOSProgress, p)
			}
		}
	}
	return out, bt.Err()
}

// Format writes the comparison as a text table for the CLI.
func Format(w io.Writer, rows []Row) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "STRATEGY\tIN-SAMPLE\tOUT-OF-SAMPLE\tBACKTEST (ALL)\tLIVE\tLIVE 95%\tSAME TRADES IN BACKTEST\tVERDICT")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Strategy,
			cell(r.ISWinRate, r.ISTrades), cell(r.OOSWinRate, r.OOSTrades), cell(r.AllWinRate, r.AllTrades), cell(r.LiveWinRate, r.LiveDecisions),
			interval(r.LiveCILow, r.LiveCIHigh), cell(r.ModelWinRate, r.ModelTrades), r.Verdict)
	}
	tw.Flush()
}

func cell(rate *float64, n int) string {
	if rate == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f%% (n=%d)", *rate*100, n)
}

func interval(lo, hi *float64) string {
	if lo == nil || hi == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f-%.0f%%", *lo*100, *hi*100)
}

func ptr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	return &n.Float64
}

// Trade is one closed live trade.
type Trade struct {
	Strategy   string  `json:"strategy"`
	Account    string  `json:"account"`
	Symbol     string  `json:"symbol"`
	Status     string  `json:"status"`
	Reason     string  `json:"exit_reason"`
	ReasonCode string  `json:"exit_reason_code"`
	EntryTime  string  `json:"entry_time"`
	ExitTime   string  `json:"exit_time"`
	Quantity   float64 `json:"quantity"`
	EntryPrice float64 `json:"entry_price"` // the limit that was sent
	// EntryFill and ExitFill are what the broker really executed at; 0 when not yet recorded, in
	// which case P&L is measured from the limit and the order price. ExitPrice is the fill when known.
	EntryFill float64 `json:"entry_fill_price"`
	ExitFill  float64 `json:"exit_fill_price"`
	ExitPrice float64 `json:"exit_price"`
	PnL       float64 `json:"pnl"`
	ReturnPct float64 `json:"return_pct"`
	StopLoss  bool    `json:"stop_loss"`
}

// Recent returns the trades that closed in the last days UTC dates, newest first: 1 is today only,
// 2 is today and yesterday. A finished run of the same days within maxAge is read instead of building new tables (zero always builds). The slice table is a run-scoped table in Turso (see runtables); the tables it reads are never changed.
func Recent(ctx context.Context, db *sql.DB, days int, maxAge time.Duration) ([]Trade, error) {
	if days < 1 {
		days = 1
	}
	key := fmt.Sprintf("d%d", days)
	if run, ok, err := runtables.FindRecent(ctx, db, stageTables, "winrate_recent", key, maxAge); err != nil {
		return nil, err
	} else if ok {
		return readRecent(ctx, run)
	}
	run := runtables.BeginKeyed(db, stageTables, key)
	defer func() { _ = runtables.Prune(context.WithoutCancel(ctx), db, stageTables, keepRuns) }()
	if err := run.Exec(ctx, `DROP TABLE IF EXISTS winrate_window; CREATE TABLE winrate_window (days INTEGER)`); err != nil {
		return nil, err
	}
	if err := run.InsertRows(ctx, "winrate_window", []string{"days"}, [][]any{{days - 1}}); err != nil {
		return nil, err
	}
	stage := "sql/20_slice_recent.sql"
	if ok, err := hasTable(ctx, db, "manage_exit"); err != nil {
		return nil, err
	} else if !ok {
		stage = "sql/20_slice_recent_empty.sql"
	}
	if err := runFile(ctx, run, stage); err != nil {
		return nil, err
	}
	return readRecent(ctx, run)
}

// readRecent reads the recent closed trades out of a run's tables.
func readRecent(ctx context.Context, run *runtables.Run) ([]Trade, error) {
	rows, err := run.Query(ctx, `SELECT strategy_id, COALESCE(account_number, ''), symbol, status, exit_reason, exit_reason_code, entry_time, exit_time,
		COALESCE(quantity, 0), COALESCE(entry_price, 0), COALESCE(entry_fill_price, 0), COALESCE(exit_fill_price, 0), COALESCE(exit_price, 0), COALESCE(pnl, 0), COALESCE(pnl_pct, 0), stop_loss
		FROM winrate_recent ORDER BY exit_time DESC, strategy_id, account_number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Trade
	for rows.Next() {
		var t Trade
		var stop int
		if err := rows.Scan(&t.Strategy, &t.Account, &t.Symbol, &t.Status, &t.Reason, &t.ReasonCode, &t.EntryTime, &t.ExitTime,
			&t.Quantity, &t.EntryPrice, &t.EntryFill, &t.ExitFill, &t.ExitPrice, &t.PnL, &t.ReturnPct, &stop); err != nil {
			return nil, err
		}
		t.StopLoss = stop == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

// keepRuns is how many runs' slice tables are kept in Turso for audit.
const keepRuns = 3

// stageTables are the slice tables of the comparison, found in the embedded stage SQL plus the
// three that Go creates. Each run has its own copy of them in Turso.
var stageTables = sliceTableNames()

func sliceTableNames() []string {
	re := regexp.MustCompile(`CREATE TABLE (?:IF NOT EXISTS )?(winrate_\w+)`)
	seen := map[string]bool{"winrate_done": true, "winrate_ids": true, "winrate_baseline": true, "winrate_baseline_trade": true, "winrate_window": true}
	files, _ := sqlFiles.ReadDir("sql")
	for _, f := range files {
		b, _ := sqlFiles.ReadFile("sql/" + f.Name())
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			seen[m[1]] = true
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// hasTable reports whether db has a table called name.
func hasTable(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return n > 0, err
}

// runFile runs one embedded stage file in run's tables.
func runFile(ctx context.Context, run *runtables.Run, name string) error {
	b, err := sqlFiles.ReadFile(name)
	if err != nil {
		return err
	}
	if err := run.Exec(ctx, string(b)); err != nil {
		return fmt.Errorf("live_vs_backtest_winrate: %s: %w", strings.TrimPrefix(name, "sql/"), err)
	}
	return nil
}
