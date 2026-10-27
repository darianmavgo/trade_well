package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// SignalScanRow is one strategy's status as of the live as-of bar.
// Same shape livescan has always written to livescan_status.
type SignalScanRow struct {
	StrategyID   string `json:"strategy_id"`
	StrategyName string `json:"strategy_name"`
	Status       string `json:"status"` // "ENTER" or "NO_SIGNAL"
	Symbols      string `json:"symbols"`
	SignalDate   string `json:"signal_date"`
	// Trace says why the strategy did or did not enter on each of its latest sessions. It comes
	// from the pipeline's own signal_trace table, so it is empty for a pipeline without one.
	Trace []TraceRow `json:"trace,omitempty"`
}

// TraceRow is one session of a strategy's explanation: the verdict, the reason in a sentence and
// the numbers behind it (Facts is a JSON object whose keys depend on the strategy family).
type TraceRow struct {
	Symbol string          `json:"symbol"`
	Date   string          `json:"date"`
	Entry  bool            `json:"entry"`
	Reason string          `json:"reason"`
	Facts  json.RawMessage `json:"facts,omitempty"`
}

// SignalDetail is one ENTER-eligible signal on the as-of bar (priced).
// Persisted to livescan_signals and emitted in -json so trade_orchestrator
// can stage orders without re-running GenerateSignals.
type SignalDetail struct {
	StrategyID string  `json:"strategy_id"`
	Symbol     string  `json:"symbol"`
	Direction  string  `json:"direction"`
	Date       string  `json:"date"`
	Price      float64 `json:"price"`
	BuyLimit   float64 `json:"buy_limit,omitempty"`
	Close      float64 `json:"close,omitempty"`
	TakeProfit float64 `json:"take_profit,omitempty"`
	StopLoss   float64 `json:"stop_loss,omitempty"`
}

// SignalScanOptions configures the live/signal-only window over the same
// GenerateSignals path backtest uses before simulation.
type SignalScanOptions struct {
	MarketDB      string
	Table         string
	Strategies    []strategy.Strategy
	SymbolFilter  string
	BarsLimit     int // 0 = max MinHistoryBarsFor among strategies
	AutoDownload  bool
	DownloadYears int
	Concurrency   int
	OutDir        string    // livescan.db parent; empty skips persist
	Now           time.Time // tests; zero → time.Now()

	// Context cancels the refresh and scan; nil means context.Background().
	Context context.Context
	// Out receives progress text; nil discards it (the CLIs pass os.Stdout).
	Out io.Writer
}

// SignalScanResult is the full live-window scan output.
type SignalScanResult struct {
	AsOf          string          `json:"as_of"`
	NextSession   string          `json:"next_session"`
	TipDate       string          `json:"tip_date"`
	Rows          []SignalScanRow `json:"rows"`
	Signals       []SignalDetail  `json:"signals"`
	SymbolsLoaded int             `json:"symbols_loaded"`
	OutDBPath     string          `json:"out_db_path"`
}

// RunSignalScan is the shared backtest signal phase with the window fixed to
// the live tip bar (buy signal for today / next session). livescan is a thin
// CLI wrapper around this; backtest -signals-only can call it too.
func RunSignalScan(opts SignalScanOptions) (*SignalScanResult, error) {
	if len(opts.Strategies) == 0 {
		return nil, fmt.Errorf("no strategies")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}

	outDir := opts.OutDir
	if outDir == "" {
		outDir = "."
	}
	livescanDBPath := filepath.Join(outDir, "livescan.db")
	// Each strategy computes in its own scratch database, recreated for this scan. The pipelines
	// build their slice tables with CREATE TABLE IF NOT EXISTS, so a database shared by several
	// strategies (or kept from an earlier scan) would hand one strategy another's rows.
	calcByID := make(map[string]string, len(opts.Strategies))
	for _, s := range opts.Strategies {
		calc, err := freshCalcDB(outDir, s.ID())
		if err != nil {
			return nil, err
		}
		calcByID[s.ID()] = calc
		s.SetDatabases(opts.MarketDB, calc)
	}

	var knownSymbols []string
	if opts.SymbolFilter != "" {
		for _, p := range strings.Split(opts.SymbolFilter, ",") {
			if t := strings.ToUpper(strings.TrimSpace(p)); t != "" {
				knownSymbols = append(knownSymbols, t)
			}
		}
	} else if allStrategiesDeclareSymbols(opts.Strategies) {
		knownSymbols = RequiredSymbolsFor(opts.Strategies, "")
	}

	minBars := opts.BarsLimit
	if minBars <= 0 {
		for _, s := range opts.Strategies {
			if n := strategy.MinHistoryBarsFor(s); n > minBars {
				minBars = n
			}
		}
	}
	scanYears := minBars/240 + 1
	if opts.DownloadYears > 0 && scanYears > opts.DownloadYears {
		scanYears = opts.DownloadYears
	}
	if scanYears < 1 {
		scanYears = 1
	}

	// Default: fill market history for the live window before scanning.
	// Never soft-continue on cache — download failure and stale tip both abort.
	symbolsForRefresh := knownSymbols
	if len(symbolsForRefresh) == 0 {
		symbolsForRefresh = RequiredSymbolsFor(opts.Strategies, opts.SymbolFilter)
	}
	if opts.AutoDownload {
		fmt.Fprintf(out, "\n📥 Refreshing market history for live window (%d symbol(s), %d yr)...\n", len(symbolsForRefresh), scanYears)
		if len(symbolsForRefresh) > 0 {
			if err := RunDownloadContext(ctx, opts.MarketDB, opts.Table, symbolsForRefresh, scanYears); err != nil {
				return nil, fmt.Errorf("MARKET_DATA_REFRESH_FAILED: download for %v into %s (%s) failed: %w", symbolsForRefresh, opts.MarketDB, opts.Table, err)
			}
		} else {
			if err := DetectAndDownloadMissingData(opts.MarketDB, opts.Table, opts.Strategies, opts.SymbolFilter, true, scanYears); err != nil {
				return nil, fmt.Errorf("MARKET_DATA_REFRESH_FAILED: %w", err)
			}
		}
	} else {
		fmt.Fprintln(out, "\n⚠️  -auto-download=false: skipping refresh; will still refuse a stale tip")
	}

	db, err := storage.OpenSQLite(opts.MarketDB)
	if err != nil {
		return nil, fmt.Errorf("open market DB %s: %w", opts.MarketDB, err)
	}
	defer db.Close()

	barsBySymbol, sortedDates, err := storage.FetchRecentBars(db, opts.Table, knownSymbols, minBars)
	if err != nil {
		return nil, fmt.Errorf("load recent bars from %s (%s): %w", opts.MarketDB, opts.Table, err)
	}
	barsBySymbol, sortedDates = DropUnfinishedSessions(barsBySymbol, now)
	if len(barsBySymbol) == 0 || len(sortedDates) == 0 {
		return nil, fmt.Errorf("no historical bars in %s (%s) after refresh", opts.MarketDB, opts.Table)
	}
	tip := sortedDates[len(sortedDates)-1]
	asOf, nextSession, err := ResolveLiveAsOf(tip, now)
	if err != nil {
		return nil, err
	}

	for _, s := range opts.Strategies {
		if p, ok := s.(strategy.Preparer); ok {
			if err := p.Prepare(ctx, opts.MarketDB); err != nil {
				return nil, fmt.Errorf("PREPARE_FAILED: %s: %w", s.ID(), err)
			}
		}
	}

	type scanOut struct {
		row  SignalScanRow
		sigs []SignalDetail
	}
	workers := opts.Concurrency
	if workers < 1 {
		workers = 1
	}
	if workers > len(opts.Strategies) {
		workers = len(opts.Strategies)
	}
	jobs := make(chan strategy.Strategy, len(opts.Strategies))
	for _, s := range opts.Strategies {
		jobs <- s
	}
	close(jobs)
	resultsChan := make(chan scanOut, len(opts.Strategies))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for strat := range jobs {
				if ctx.Err() != nil {
					return
				}
				sigs := strat.GenerateSignals(barsBySymbol)
				row, details := buildSignalScanRow(strat, sigs, asOf)
				row.Trace = ReadSignalTrace(calcByID[strat.ID()])
				resultsChan <- scanOut{row: row, sigs: details}
			}
		}()
	}
	wg.Wait()
	close(resultsChan)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("signal scan cancelled: %w", err)
	}

	var rows []SignalScanRow
	var details []SignalDetail
	for r := range resultsChan {
		rows = append(rows, r.row)
		details = append(details, r.sigs...)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].StrategyID < rows[j].StrategyID })
	sort.Slice(details, func(i, j int) bool {
		if details[i].StrategyID != details[j].StrategyID {
			return details[i].StrategyID < details[j].StrategyID
		}
		return details[i].Symbol < details[j].Symbol
	})

	res := &SignalScanResult{
		AsOf:          asOf,
		NextSession:   nextSession,
		TipDate:       tip,
		Rows:          rows,
		Signals:       details,
		SymbolsLoaded: len(barsBySymbol),
		OutDBPath:     livescanDBPath,
	}
	if opts.OutDir != "" {
		if err := SaveSignalScanRows(livescanDBPath, rows); err != nil {
			return res, fmt.Errorf("save livescan_status: %w", err)
		}
		if err := SaveSignalDetails(livescanDBPath, details); err != nil {
			return res, fmt.Errorf("save livescan_signals: %w", err)
		}
	}
	return res, nil
}

func allStrategiesDeclareSymbols(strategies []strategy.Strategy) bool {
	for _, s := range strategies {
		declares := false
		if rp, ok := s.(strategy.RequiredSymbolsProvider); ok && len(rp.RequiredSymbols()) > 0 {
			declares = true
		}
		if !declares && strings.TrimSpace(s.DefaultConfig().Benchmark) != "" {
			declares = true
		}
		if !declares {
			return false
		}
	}
	return true
}

// liveLimitEntry prices the order a strategy with EntryLimitPct sends for the next
// session. The backtest enters on a session with a buy limit at that fraction of the
// previous session's close, so the live order, placed after the as-of close for the
// next session, is a limit at that fraction of the as-of close. Take-profit and stop
// are absolute prices anchored to the limit, as the staged bracket order is (the
// simulator measures them from the fill, which is the limit or, on a gap down, lower).
// A target or stop the signal already carries is kept.
func liveLimitEntry(sig models.Signal, cfg strategy.StrategyConfig) (buyLimit, takeProfit, stopLoss float64) {
	buyLimit = sig.Close * cfg.EntryLimitPct
	takeProfit, stopLoss = sig.TakeProfit, sig.StopLoss
	if takeProfit <= 0 {
		switch {
		case cfg.TakeProfitPct > 0:
			takeProfit = buyLimit * (1 + cfg.TakeProfitPct)
		case cfg.TargetPct > 1:
			takeProfit = buyLimit * cfg.TargetPct
		}
	}
	if stopLoss <= 0 && cfg.StopLossPct > 0 && cfg.StopLossPct < 1 {
		stopLoss = buyLimit * cfg.StopLossPct
	}
	return buyLimit, takeProfit, stopLoss
}

func buildSignalScanRow(strat strategy.Strategy, signals []models.Signal, asOf string) (SignalScanRow, []SignalDetail) {
	cfg := strat.DefaultConfig()
	seen := map[string]bool{}
	var parts []string
	var details []SignalDetail
	for _, sig := range signals {
		if sig.Date != asOf {
			continue
		}
		dir := sig.Direction
		if dir == "" {
			dir = "LONG"
		}
		if dir != "LONG" && dir != "BUY" {
			continue
		}
		sym := strings.ToUpper(strings.TrimSpace(sig.Symbol))
		if sym == "" {
			continue
		}
		key := sym + ":" + dir
		if seen[key] {
			continue
		}
		seen[key] = true
		parts = append(parts, key)
		price := sig.BuyLimit
		if price <= 0 {
			price = sig.Close
		}
		buyLimit, takeProfit, stopLoss := sig.BuyLimit, sig.TakeProfit, sig.StopLoss
		if cfg.EntryLimitPct > 0 && sig.Close > 0 {
			buyLimit, takeProfit, stopLoss = liveLimitEntry(sig, cfg)
			price = buyLimit
		}
		details = append(details, SignalDetail{
			StrategyID: strat.ID(),
			Symbol:     sym,
			Direction:  dir,
			Date:       asOf,
			Price:      price,
			BuyLimit:   buyLimit,
			Close:      sig.Close,
			TakeProfit: takeProfit,
			StopLoss:   stopLoss,
		})
	}
	status := "NO_SIGNAL"
	if len(parts) > 0 {
		status = "ENTER"
	}
	return SignalScanRow{
		StrategyID:   strat.ID(),
		StrategyName: strat.Name(),
		Status:       status,
		Symbols:      strings.Join(parts, ", "),
		SignalDate:   asOf,
	}, details
}

// SaveSignalScanRows upserts livescan_status (same schema livescan always used).
func SaveSignalScanRows(dbPath string, rows []SignalScanRow) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return err
	}
	db, err := storage.OpenSQLite(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS livescan_status (
			strategy_id   TEXT PRIMARY KEY,
			strategy_name TEXT,
			status        TEXT NOT NULL,
			symbols       TEXT,
			signal_date   TEXT,
			scanned_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := db.Exec(`
			INSERT INTO livescan_status (strategy_id, strategy_name, status, symbols, signal_date, scanned_at)
			VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(strategy_id) DO UPDATE SET
				strategy_name = excluded.strategy_name,
				status        = excluded.status,
				symbols       = excluded.symbols,
				signal_date   = excluded.signal_date,
				scanned_at    = excluded.scanned_at
		`, r.StrategyID, r.StrategyName, r.Status, r.Symbols, r.SignalDate); err != nil {
			return err
		}
	}
	return nil
}

// SaveSignalDetails replaces livescan_signals with this scan's priced ENTER rows.
func SaveSignalDetails(dbPath string, details []SignalDetail) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return err
	}
	db, err := storage.OpenSQLite(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS livescan_signals (
			strategy_id TEXT NOT NULL,
			symbol      TEXT NOT NULL,
			direction   TEXT,
			date        TEXT NOT NULL,
			price       REAL NOT NULL,
			buy_limit   REAL,
			close       REAL,
			take_profit REAL,
			stop_loss   REAL,
			PRIMARY KEY (strategy_id, symbol, date)
		);
	`); err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM livescan_signals`); err != nil {
		return err
	}
	for _, d := range details {
		if _, err := db.Exec(`
			INSERT INTO livescan_signals (strategy_id, symbol, direction, date, price, buy_limit, close, take_profit, stop_loss)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, d.StrategyID, d.Symbol, d.Direction, d.Date, d.Price, d.BuyLimit, d.Close, d.TakeProfit, d.StopLoss); err != nil {
			return err
		}
	}
	return nil
}

// MarshalSignalScanJSON is the machine-readable livescan contract for orchestrator.
func MarshalSignalScanJSON(res *SignalScanResult) ([]byte, error) {
	return json.MarshalIndent(res, "", "  ")
}

var calcNameUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// freshCalcDB returns the path of strategy id's scratch database under outDir/calc, with any file
// left by an earlier scan removed.
func freshCalcDB(outDir, id string) (string, error) {
	dir := filepath.Join(outDir, "calc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create scratch dir %s: %w", dir, err)
	}
	path := filepath.Join(dir, calcNameUnsafe.ReplaceAllString(id, "_")+".db")
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("remove old scratch database %s: %w", path+suffix, err)
		}
	}
	return path, nil
}

// ReadSignalTrace returns the signal_trace rows a pipeline left in its scratch database, oldest
// session first. A database without the table, or one that cannot be read, gives none: the trace
// explains a decision and must never stop a scan.
func ReadSignalTrace(calcDB string) []TraceRow {
	if calcDB == "" {
		return nil
	}
	if _, err := os.Stat(calcDB); err != nil {
		return nil
	}
	db, err := storage.OpenSQLiteReadOnly(calcDB)
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := db.Queryx(`SELECT symbol, date, entry, coalesce(reason, ''), coalesce(facts, '') FROM signal_trace ORDER BY date, symbol`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []TraceRow
	for rows.Next() {
		var t TraceRow
		var entry int
		var facts string
		if err := rows.Scan(&t.Symbol, &t.Date, &entry, &t.Reason, &facts); err != nil {
			return out
		}
		t.Entry = entry != 0
		if json.Valid([]byte(facts)) {
			t.Facts = json.RawMessage(facts)
		}
		out = append(out, t)
	}
	return out
}
