package analytics

import (
	"math"
	"sort"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// IdleStats summarizes how much of a backtest the account sat in cash rather
// than in positions. Used to measure a primary strategy's unused capital —
// the pool a stacked overlay can try to earn against — and to show whether
// an overlay actually reduced idle cash.
type IdleStats struct {
	TradingDays    int
	DaysFullyIdle  int     // OpenPositions == 0
	DaysDeployed   int     // OpenPositions > 0
	FullyIdlePct   float64 // DaysFullyIdle / TradingDays
	AvgCashPct     float64 // mean(Cash / TotalEquity)
	MedianCashPct  float64
	AvgDeployedPct float64 // mean(PositionsValue / TotalEquity)
	MinCashPct     float64
	MaxCashPct     float64
}

// CalculateIdleStats derives cash-utilization stats from a daily equity curve.
func CalculateIdleStats(curve []models.DailyEquityPoint) IdleStats {
	stats := IdleStats{
		TradingDays: len(curve),
		MinCashPct:  1.0,
	}
	if len(curve) == 0 {
		stats.MinCashPct = 0
		return stats
	}

	cashPcts := make([]float64, 0, len(curve))
	var sumCash, sumDeployed float64
	for _, pt := range curve {
		if pt.OpenPositions == 0 {
			stats.DaysFullyIdle++
		} else {
			stats.DaysDeployed++
		}
		cashPct := 0.0
		deployedPct := 0.0
		if pt.TotalEquity > 0 {
			cashPct = pt.Cash / pt.TotalEquity
			deployedPct = pt.PositionsValue / pt.TotalEquity
		}
		cashPcts = append(cashPcts, cashPct)
		sumCash += cashPct
		sumDeployed += deployedPct
		if cashPct < stats.MinCashPct {
			stats.MinCashPct = cashPct
		}
		if cashPct > stats.MaxCashPct {
			stats.MaxCashPct = cashPct
		}
	}

	n := float64(len(curve))
	stats.FullyIdlePct = float64(stats.DaysFullyIdle) / n
	stats.AvgCashPct = sumCash / n
	stats.AvgDeployedPct = sumDeployed / n

	sort.Float64s(cashPcts)
	mid := len(cashPcts) / 2
	if len(cashPcts)%2 == 0 {
		stats.MedianCashPct = (cashPcts[mid-1] + cashPcts[mid]) / 2.0
	} else {
		stats.MedianCashPct = cashPcts[mid]
	}
	if math.IsNaN(stats.AvgCashPct) {
		stats.AvgCashPct = 0
	}
	return stats
}
