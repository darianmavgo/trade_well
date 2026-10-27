package park_sweep

import (
	"fmt"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/analytics"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/options"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/jmoiron/sqlx"
)

// SeedCounts is what seed wrote, by kind.
type SeedCounts struct {
	Kind    string
	Rows    int
	Pending int
	Skipped int
}

// Seed copies streak_strategy and markov_strategy into the
// sweep database, or only the rows named in ids when ids is not empty. Ids that
// are not streak or markov rows are printed and skipped. A symbol whose daily bars do not cover the config window is
// stored as skipped. Rows already done are left done.
func Seed(sweepPath, settingsPath, marketPath string, ids []string) ([]SeedCounts, error) {
	sweep, err := Open(sweepPath)
	if err != nil {
		return nil, err
	}
	defer sweep.Close()
	if err := ensureConfig(sweep); err != nil {
		return nil, err
	}
	cfg, err := loadConfig(sweep)
	if err != nil {
		return nil, err
	}

	settings, err := openReadOnly(settingsPath)
	if err != nil {
		return nil, err
	}
	defer settings.Close()
	market, err := storage.OpenSQLite(marketPath)
	if err != nil {
		return nil, err
	}
	defer market.Close()

	rows, err := loadSourceRows(settings)
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		rows = onlyIDs(rows, ids)
	}
	cov, err := coverage(market)
	if err != nil {
		return nil, err
	}
	if err := covers(cov, cfg.ParkSymbol, cfg.StartDate, cfg.EndDate); err != nil {
		return nil, fmt.Errorf("park symbol %s: %w", cfg.ParkSymbol, err)
	}
	if err := writeParkAsset(sweep, market, cfg); err != nil {
		return nil, err
	}
	if err := writeStrategies(sweep, rows, cov, cfg); err != nil {
		return nil, err
	}
	return countSeed(sweep)
}

type span struct {
	first string
	last  string
}

func coverage(market *sqlx.DB) (map[string]span, error) {
	type row struct {
		Symbol string `db:"symbol"`
		First  string `db:"first_d"`
		Last   string `db:"last_d"`
	}
	var rows []row
	err := market.Select(&rows, `
		SELECT symbol,
		       MIN(substr(Date, 1, 10)) AS first_d,
		       MAX(substr(Date, 1, 10)) AS last_d
		FROM backtest_start
		WHERE length(Date) = 10 AND timeframe = '1d'
		GROUP BY symbol`)
	if err != nil {
		return nil, fmt.Errorf("symbol coverage: %w", err)
	}
	out := make(map[string]span, len(rows))
	for _, r := range rows {
		out[strings.ToUpper(r.Symbol)] = span{first: r.First, last: r.Last}
	}
	return out, nil
}

func covers(cov map[string]span, symbol, start, end string) error {
	sp, ok := cov[strings.ToUpper(symbol)]
	if !ok {
		return fmt.Errorf("no daily bars")
	}
	if sp.first > start || sp.last < end {
		return fmt.Errorf("bars %s .. %s do not cover %s .. %s", sp.first, sp.last, start, end)
	}
	return nil
}

func loadSourceRows(settings *sqlx.DB) ([]Strategy, error) {
	var streaks []struct {
		ID            string  `db:"id"`
		Name          string  `db:"name"`
		SignalSymbol  string  `db:"signal_symbol"`
		TradeSymbol   string  `db:"trade_symbol"`
		Direction     string  `db:"direction"`
		SignalDays    int     `db:"signal_days"`
		HoldDays      int     `db:"hold_days"`
		TakeProfitPct float64 `db:"take_profit_pct"`
		StopLossPct   float64 `db:"stop_loss_pct"`
		Regime        string  `db:"regime"`
		AllocationPct float64 `db:"allocation_pct"`
		CashYield     float64 `db:"cash_yield"`
		SlippagePct   float64 `db:"slippage_pct"`
		NextDayLimit  int     `db:"next_day_limit"`
	}
	if err := settings.Select(&streaks, `
		SELECT id, name, signal_symbol, trade_symbol, direction, signal_days, hold_days,
		       take_profit_pct, stop_loss_pct, regime, allocation_pct, cash_yield,
		       slippage_pct, next_day_limit
		FROM streak_strategy ORDER BY id`); err != nil {
		return nil, fmt.Errorf("streak_strategy: %w", err)
	}
	var markovs []struct {
		ID            string  `db:"id"`
		Name          string  `db:"name"`
		SignalSymbol  string  `db:"signal_symbol"`
		TradeSymbol   string  `db:"trade_symbol"`
		Direction     string  `db:"direction"`
		TargetState   string  `db:"target_state"`
		HoldDays      int     `db:"hold_days"`
		TakeProfitPct float64 `db:"take_profit_pct"`
		StopLossPct   float64 `db:"stop_loss_pct"`
		AllocationPct float64 `db:"allocation_pct"`
		CashYield     float64 `db:"cash_yield"`
		SlippagePct   float64 `db:"slippage_pct"`
		NextDayLimit  int     `db:"next_day_limit"`
	}
	if err := settings.Select(&markovs, `
		SELECT id, name, signal_symbol, trade_symbol, direction, target_state, hold_days,
		       take_profit_pct, stop_loss_pct, allocation_pct, cash_yield,
		       slippage_pct, next_day_limit
		FROM markov_strategy ORDER BY id`); err != nil {
		return nil, fmt.Errorf("markov_strategy: %w", err)
	}
	out := make([]Strategy, 0, len(streaks)+len(markovs))
	for _, r := range streaks {
		out = append(out, Strategy{
			StrategyID: r.ID, Kind: "streak", Name: r.Name,
			SignalSymbol: strings.ToUpper(r.SignalSymbol), TradeSymbol: strings.ToUpper(r.TradeSymbol),
			Direction: r.Direction, SignalDays: r.SignalDays, HoldDays: r.HoldDays,
			TakeProfitPct: r.TakeProfitPct, StopLossPct: r.StopLossPct, Regime: r.Regime,
			AllocationPct: r.AllocationPct, CashYield: r.CashYield, SlippagePct: r.SlippagePct,
			NextDayLimit: r.NextDayLimit,
		})
	}
	for _, r := range markovs {
		out = append(out, Strategy{
			StrategyID: r.ID, Kind: "markov", Name: r.Name,
			SignalSymbol: strings.ToUpper(r.SignalSymbol), TradeSymbol: strings.ToUpper(r.TradeSymbol),
			Direction: r.Direction, HoldDays: r.HoldDays,
			TakeProfitPct: r.TakeProfitPct, StopLossPct: r.StopLossPct,
			Regime: r.TargetState, TargetState: r.TargetState,
			AllocationPct: r.AllocationPct, CashYield: r.CashYield, SlippagePct: r.SlippagePct,
			NextDayLimit: r.NextDayLimit,
		})
	}
	return out, nil
}

func writeParkAsset(sweep, market *sqlx.DB, cfg Config) error {
	bars, _, err := storage.FetchBars(market, "backtest_start", []string{cfg.ParkSymbol}, cfg.StartDate, cfg.EndDate)
	if err != nil {
		return err
	}
	series := bars[cfg.ParkSymbol]
	if len(series) == 0 {
		return fmt.Errorf("park symbol %s has no bars in %s .. %s", cfg.ParkSymbol, cfg.StartDate, cfg.EndDate)
	}
	report, dividends := buyAndHold(series, cfg.Capital)
	_, err = sweep.Exec(`
		INSERT INTO park_asset (symbol, first_date, last_date, bar_count, final_equity, cagr, max_drawdown_pct, sharpe, dividends)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol) DO UPDATE SET
			first_date = excluded.first_date,
			last_date = excluded.last_date,
			bar_count = excluded.bar_count,
			final_equity = excluded.final_equity,
			cagr = excluded.cagr,
			max_drawdown_pct = excluded.max_drawdown_pct,
			sharpe = excluded.sharpe,
			dividends = excluded.dividends`,
		cfg.ParkSymbol, series[0].Date, series[len(series)-1].Date, len(series),
		report.FinalEquity, report.CAGR, report.MaxDrawdownPct, report.SharpeRatio, dividends)
	return err
}

// buyAndHold buys fractional shares at the first close and reinvests dividends
// from DividendsFromAdjClose. CAGR uses the 252-day year in analytics.
func buyAndHold(series []models.Bar, capital float64) (models.PerformanceReport, float64) {
	divs := options.DividendsFromAdjClose(series)
	if len(series) == 0 || series[0].Close <= 0 {
		return models.PerformanceReport{}, 0
	}
	shares := capital / series[0].Close
	var cash, dividendCash float64
	curve := make([]models.DailyEquityPoint, 0, len(series))
	for _, b := range series {
		if d, ok := divs[b.Date]; ok && d > 0 {
			got := shares * d
			dividendCash += got
			cash += got
			if b.Close > 0 {
				shares += cash / b.Close
				cash = 0
			}
		}
		eq := cash + shares*b.Close
		curve = append(curve, models.DailyEquityPoint{
			Date: b.Date, Cash: cash, PositionsValue: shares * b.Close,
			TotalEquity: eq, OpenPositions: 1,
		})
	}
	return analytics.CalculatePerformanceMetrics(capital, nil, curve), dividendCash
}

func writeStrategies(sweep *sqlx.DB, rows []Strategy, cov map[string]span, cfg Config) error {
	tx, err := sweep.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rows {
		if _, err := tx.NamedExec(`
			INSERT INTO sweep_strategy (
				strategy_id, kind, name, signal_symbol, trade_symbol, direction, signal_days,
				hold_days, take_profit_pct, stop_loss_pct, regime, target_state,
				allocation_pct, cash_yield, slippage_pct, commission_per_share, next_day_limit
			) VALUES (
				:strategy_id, :kind, :name, :signal_symbol, :trade_symbol, :direction, :signal_days,
				:hold_days, :take_profit_pct, :stop_loss_pct, :regime, :target_state,
				:allocation_pct, :cash_yield, :slippage_pct, :commission_per_share, :next_day_limit
			)
			ON CONFLICT(strategy_id) DO UPDATE SET
				kind = excluded.kind,
				name = excluded.name,
				signal_symbol = excluded.signal_symbol,
				trade_symbol = excluded.trade_symbol,
				direction = excluded.direction,
				signal_days = excluded.signal_days,
				hold_days = excluded.hold_days,
				take_profit_pct = excluded.take_profit_pct,
				stop_loss_pct = excluded.stop_loss_pct,
				regime = excluded.regime,
				target_state = excluded.target_state,
				allocation_pct = excluded.allocation_pct,
				cash_yield = excluded.cash_yield,
				slippage_pct = excluded.slippage_pct,
				commission_per_share = excluded.commission_per_share,
				next_day_limit = excluded.next_day_limit`, r); err != nil {
			return err
		}
		status := "pending"
		reason := ""
		if err := covers(cov, r.SignalSymbol, cfg.StartDate, cfg.EndDate); err != nil {
			status, reason = "skipped", r.SignalSymbol+": "+err.Error()
		} else if err := covers(cov, r.TradeSymbol, cfg.StartDate, cfg.EndDate); err != nil {
			status, reason = "skipped", r.TradeSymbol+": "+err.Error()
		}
		if _, err := tx.Exec(`
			INSERT INTO strategy_run (strategy_id, status, error)
			VALUES (?, ?, ?)
			ON CONFLICT(strategy_id) DO UPDATE SET
				status = excluded.status,
				error = excluded.error
			WHERE strategy_run.status IN ('pending', 'skipped')`,
			r.StrategyID, status, reason); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func countSeed(sweep *sqlx.DB) ([]SeedCounts, error) {
	type row struct {
		Kind   string `db:"kind"`
		Status string `db:"status"`
		N      int    `db:"n"`
	}
	var rows []row
	if err := sweep.Select(&rows, `
		SELECT s.kind, r.status, COUNT(*) AS n
		FROM strategy_run r
		JOIN sweep_strategy s ON s.strategy_id = r.strategy_id
		GROUP BY s.kind, r.status
		ORDER BY s.kind, r.status`); err != nil {
		return nil, err
	}
	byKind := map[string]*SeedCounts{}
	var order []string
	for _, kind := range []string{"streak", "markov"} {
		byKind[kind] = &SeedCounts{Kind: kind}
		order = append(order, kind)
	}
	for _, r := range rows {
		c, ok := byKind[r.Kind]
		if !ok {
			c = &SeedCounts{Kind: r.Kind}
			byKind[r.Kind] = c
			order = append(order, r.Kind)
		}
		c.Rows += r.N
		switch r.Status {
		case "pending":
			c.Pending += r.N
		case "skipped":
			c.Skipped += r.N
		}
	}
	out := make([]SeedCounts, 0, len(order))
	for _, kind := range order {
		out = append(out, *byKind[kind])
	}
	return out, nil
}

// onlyIDs keeps the rows named in ids (matched case-insensitively) and prints the
// ids that are not a streak_strategy or markov_strategy row.
func onlyIDs(rows []Strategy, ids []string) []Strategy {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[strings.ToLower(id)] = true
	}
	var out []Strategy
	for _, r := range rows {
		if want[strings.ToLower(r.StrategyID)] {
			out = append(out, r)
			delete(want, strings.ToLower(r.StrategyID))
		}
	}
	for _, id := range ids {
		if want[strings.ToLower(id)] {
			fmt.Printf("  skipped %s: park_sweep only runs streak_strategy and markov_strategy rows\n", id)
		}
	}
	return out
}
