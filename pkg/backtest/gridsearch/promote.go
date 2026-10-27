package gridsearch

import (
	"fmt"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/streak_strategy"
)

// PromoteConfig selects winning gridsearch_results rows and writes them to
// the streak_strategy table.
type PromoteConfig struct {
	Parents    []strategy.Strategy
	GridDB     string
	RefDB      string
	MinWinRate float64
	MinTrades  int
	Top        int
}

// PromoteReport is what promote wrote and what it refused.
type PromoteReport struct {
	Written []refdb.StreakStrategy
	Skipped []string
	Notes   []string
}

type promoteRow struct {
	Label        string  `db:"label"`
	Symbol       string  `db:"symbol"`
	SignalSymbol string  `db:"signal_symbol"`
	SignalDays   int     `db:"signal_days"`
	HoldDays     int     `db:"hold_days"`
	TakeProfit   float64 `db:"take_profit_pct"`
	StopLoss     float64 `db:"stop_loss_pct"`
	Regime       string  `db:"regime"`
	Allocation   float64 `db:"allocation_pct"`
	WinRate      float64 `db:"win_rate"`
	TotalTrades  int     `db:"total_trades"`
}

// Promote copies qualifying gridsearch rows into streak_strategy.
// The generated id is streak-<signal>-<up|down><days>-<trade>. Two rows that
// share that id keep the higher win rate (the query is ordered that way)
// and the later one is skipped.
func Promote(cfg PromoteConfig) (PromoteReport, error) {
	var rep PromoteReport
	if cfg.Top < 1 {
		return rep, fmt.Errorf("promote -top must be >= 1")
	}
	if len(cfg.Parents) == 0 {
		return rep, fmt.Errorf("promote requires -strategy")
	}
	gdb, err := storage.OpenSQLite(cfg.GridDB)
	if err != nil {
		return rep, fmt.Errorf("open gridsearch DB: %w", err)
	}
	defer gdb.Close()
	if err := ensureGridSearchSchema(gdb); err != nil {
		return rep, err
	}
	rdb, err := refdb.Open(cfg.RefDB)
	if err != nil {
		return rep, fmt.Errorf("open reference DB: %w", err)
	}
	defer rdb.Close()

	seen := map[string]bool{}
	var write []refdb.StreakStrategy
	for _, parent := range cfg.Parents {
		space := strategy.AssessParameterSpace(parent)
		if space.Direction != "drop" && space.Direction != "rally" {
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: direction %q is not a streak", parent.ID(), space.Direction))
			continue
		}
		var rows []promoteRow
		if err := gdb.Select(&rows, `
			SELECT COALESCE(label, '') AS label,
			       COALESCE(symbol, '') AS symbol,
			       COALESCE(signal_symbol, '') AS signal_symbol,
			       COALESCE(signal_days, 0) AS signal_days,
			       COALESCE(hold_days, 0) AS hold_days,
			       COALESCE(take_profit_pct, 0) AS take_profit_pct,
			       COALESCE(stop_loss_pct, 0) AS stop_loss_pct,
			       COALESCE(regime, '') AS regime,
			       COALESCE(allocation_pct, 0) AS allocation_pct,
			       COALESCE(win_rate, 0) AS win_rate,
			       COALESCE(total_trades, 0) AS total_trades
			FROM gridsearch_results
			WHERE strategy_id = ?
			  AND COALESCE(win_rate, 0) >= ?
			  AND COALESCE(total_trades, 0) >= ?
			  AND COALESCE(hold_days, 0) > 0
			ORDER BY win_rate DESC, total_trades DESC, id ASC
			LIMIT ?
		`, parent.ID(), cfg.MinWinRate, cfg.MinTrades, cfg.Top); err != nil {
			return rep, fmt.Errorf("%s: %w", parent.ID(), err)
		}
		if len(rows) == 0 {
			rep.Notes = append(rep.Notes, fmt.Sprintf("%s: no row with win_rate >= %.2f and trades >= %d", parent.ID(), cfg.MinWinRate, cfg.MinTrades))
			continue
		}
		for _, row := range rows {
			signal := strings.ToUpper(strings.TrimSpace(row.SignalSymbol))
			if signal == "" {
				signal = strings.ToUpper(strings.TrimSpace(space.SignalSymbol))
				rep.Notes = append(rep.Notes, fmt.Sprintf("%s %s: signal_symbol was not stored; using %s from the strategy (a past -signal override cannot be recovered)", parent.ID(), row.Label, signal))
			}
			trade := strings.ToUpper(strings.TrimSpace(row.Symbol))
			alloc := row.Allocation
			if alloc <= 0 {
				alloc = space.Baseline.Allocation
			}
			if alloc <= 0 {
				alloc = 0.65
			}
			id := streakID(signal, trade, space.Direction, row.SignalDays)
			if seen[id] {
				rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: duplicate %s (kept the higher win rate)", row.Label, id))
				continue
			}
			if existing, ok := strategy.Get(id); ok {
				if _, is := existing.(*streak_strategy.Strategy); !is {
					rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: id %s already belongs to %s", row.Label, id, existing.ID()))
					continue
				}
			}
			wr, trades := row.WinRate, row.TotalTrades
			built := refdb.StreakStrategy{
				ID:             id,
				Name:           streakName(signal, trade, space.Direction, row.SignalDays),
				SignalSymbol:   signal,
				TradeSymbol:    trade,
				Direction:      space.Direction,
				SignalDays:     row.SignalDays,
				HoldDays:       row.HoldDays,
				TakeProfitPct:  row.TakeProfit,
				StopLossPct:    row.StopLoss,
				Regime:         strings.TrimSpace(row.Regime),
				AllocationPct:  alloc,
				CashYield:      space.CashYield,
				SlippagePct:    0,
				NextDayLimit:   0,
				SourceStrategy: parent.ID(),
				SourceLabel:    row.Label,
				WinRate:        &wr,
				TotalTrades:    &trades,
			}
			if err := streak_strategy.ValidateRow(built); err != nil {
				rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: %v", row.Label, err))
				continue
			}
			seen[id] = true
			write = append(write, built)
		}
	}
	if len(write) > 0 {
		if err := refdb.UpsertStreakStrategies(rdb, write); err != nil {
			return rep, err
		}
	}
	rep.Written = write
	return rep, nil
}

func streakID(signal, trade, direction string, days int) string {
	dir := "down"
	if direction == "rally" {
		dir = "up"
	}
	return fmt.Sprintf("streak-%s-%s%d-%s", strings.ToLower(signal), dir, days, strings.ToLower(trade))
}

func streakName(signal, trade, direction string, days int) string {
	dir := "down"
	if direction == "rally" {
		dir = "up"
	}
	return fmt.Sprintf("%s %s%d → %s", strings.ToUpper(signal), dir, days, strings.ToUpper(trade))
}

func printPromoteReport(rep PromoteReport) {
	fmt.Printf("\nWrote %d streak_strategy row(s):\n", len(rep.Written))
	for _, row := range rep.Written {
		wr := 0.0
		if row.WinRate != nil {
			wr = *row.WinRate
		}
		tr := 0
		if row.TotalTrades != nil {
			tr = *row.TotalTrades
		}
		fmt.Printf("  %-28s  watch %s  buy %s  %s %dd  hold %dd  TP +%.1f%%  SL -%.1f%%  %s  win %.1f%%  trades %d\n",
			row.ID, row.SignalSymbol, row.TradeSymbol, row.Direction, row.SignalDays, row.HoldDays,
			row.TakeProfitPct*100, row.StopLossPct*100, row.Regime, wr*100, tr)
	}
	if len(rep.Skipped) > 0 {
		fmt.Printf("\nSkipped %d:\n", len(rep.Skipped))
		for _, s := range rep.Skipped {
			fmt.Printf("  - %s\n", s)
		}
	}
	if len(rep.Notes) > 0 {
		fmt.Println()
		for _, n := range rep.Notes {
			fmt.Printf("  note: %s\n", n)
		}
	}
	fmt.Println()
}
