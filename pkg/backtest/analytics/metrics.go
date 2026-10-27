package analytics

import (
	"math"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// CalculatePerformanceMetrics computes standard quantitative risk and return metrics
// from trade history and daily equity time series.
func CalculatePerformanceMetrics(
	initialCapital float64,
	trades []models.Trade,
	equityPoints []models.DailyEquityPoint,
) models.PerformanceReport {
	return CalculatePerformanceMetricsWithBenchmark(initialCapital, trades, equityPoints, nil)
}

// CalculatePerformanceMetricsWithBenchmark computes institutional risk and return metrics
// including relative benchmark statistics (Alpha, Beta, Benchmark Return).
func CalculatePerformanceMetricsWithBenchmark(
	initialCapital float64,
	trades []models.Trade,
	equityPoints []models.DailyEquityPoint,
	benchmarkBars map[string]models.Bar,
) models.PerformanceReport {
	report := models.PerformanceReport{
		InitialCapital: initialCapital,
		TotalTrades:    len(trades),
	}

	if len(equityPoints) == 0 {
		return report
	}

	report.StartDate = equityPoints[0].Date
	report.EndDate = equityPoints[len(equityPoints)-1].Date
	report.TotalTradingDays = len(equityPoints)
	report.TotalCalendarYears = float64(report.TotalTradingDays) / 252.0
	applyIdleDays(&report, equityPoints)

	finalEquity := equityPoints[len(equityPoints)-1].TotalEquity
	report.FinalEquity = finalEquity
	report.NetProfit = finalEquity - initialCapital
	if initialCapital > 0 {
		report.TotalReturnPct = (finalEquity - initialCapital) / initialCapital
	}

	// 1. Trade-level statistics
	var totalProfit, totalLoss float64
	var totalWinPct, totalLossPct float64
	var totalHoldDays int
	var totalCommission float64
	var sumMAE, sumMFE float64

	for _, t := range trades {
		totalCommission += t.CommissionPaid
		totalHoldDays += t.HoldDays
		sumMAE += t.MaxAdverseExcursion
		sumMFE += t.MaxFavorableExcursion

		if t.NetPnL > 0 {
			report.WinningTrades++
			totalProfit += t.NetPnL
			totalWinPct += t.ReturnPct
		} else {
			report.LosingTrades++
			totalLoss += math.Abs(t.NetPnL)
			totalLossPct += math.Abs(t.ReturnPct)
		}
	}

	report.TotalCommissionPaid = totalCommission

	if report.TotalTrades > 0 {
		report.WinRate = float64(report.WinningTrades) / float64(report.TotalTrades)
		report.AvgHoldingDays = float64(totalHoldDays) / float64(report.TotalTrades)
		report.AvgMAE = sumMAE / float64(report.TotalTrades)
		report.AvgMFE = sumMFE / float64(report.TotalTrades)
	}

	if report.WinningTrades > 0 {
		report.AvgWinAmount = totalProfit / float64(report.WinningTrades)
	}
	if report.LosingTrades > 0 {
		report.AvgLossAmount = totalLoss / float64(report.LosingTrades)
	}

	if totalLoss > 0 {
		report.ProfitFactor = totalProfit / totalLoss
	} else if totalProfit > 0 {
		report.ProfitFactor = 999.0 // Infinite profit factor
	}

	if report.AvgLossAmount > 0 {
		report.PayoffRatio = report.AvgWinAmount / report.AvgLossAmount
	}

	// 2. Daily returns & Peak-to-Trough Drawdown calculation
	var dailyReturns []float64
	var drawdownSqSum float64
	var sumPositiveReturns, sumNegativeReturns float64

	peak := initialCapital
	peakDate := equityPoints[0].Date
	maxDrawdown := 0.0
	currentDrawdownDays := 0
	maxDrawdownDays := 0

	for i := 1; i < len(equityPoints); i++ {
		prev := equityPoints[i-1].TotalEquity
		curr := equityPoints[i].TotalEquity
		currDate := equityPoints[i].Date

		ret := 0.0
		if prev > 0 {
			ret = (curr - prev) / prev
		}
		dailyReturns = append(dailyReturns, ret)

		if ret > 0 {
			sumPositiveReturns += ret
		} else if ret < 0 {
			sumNegativeReturns += math.Abs(ret)
		}

		// Track running peak
		if curr >= peak {
			peak = curr
			peakDate = currDate
			currentDrawdownDays = 0
		} else if peak > 0 {
			dd := (peak - curr) / peak
			currentDrawdownDays++
			drawdownSqSum += (dd * 100.0) * (dd * 100.0)

			if dd > maxDrawdown {
				maxDrawdown = dd
				report.MaxDrawdownDollars = peak - curr
				report.MaxDrawdownPeakEquity = peak
				report.MaxDrawdownTroughEquity = curr
				report.MaxDrawdownPeakDate = peakDate
				report.MaxDrawdownTroughDate = currDate
			}

			if currentDrawdownDays > maxDrawdownDays {
				maxDrawdownDays = currentDrawdownDays
			}
		}
	}

	report.MaxDrawdownPct = maxDrawdown
	report.MaxDrawdownDuration = maxDrawdownDays

	// Ulcer Index: Sqrt(Mean(DrawdownPct^2))
	if len(equityPoints) > 1 {
		report.UlcerIndex = math.Sqrt(drawdownSqSum / float64(len(equityPoints)-1))
	}

	// Omega Ratio (threshold = 0)
	if sumNegativeReturns > 0 {
		report.OmegaRatio = sumPositiveReturns / sumNegativeReturns
	} else if sumPositiveReturns > 0 {
		report.OmegaRatio = 99.0
	}

	// 3. Annualized metrics (Sharpe, Sortino, CAGR, Calmar)
	years := report.TotalCalendarYears
	if years >= 0.5 && report.FinalEquity > 0 && report.InitialCapital > 0 {
		report.CAGR = math.Pow(report.FinalEquity/report.InitialCapital, 1.0/years) - 1.0
	} else if years > 0 {
		report.CAGR = report.TotalReturnPct / years
	}

	// Calmar Ratio
	if report.MaxDrawdownPct > 0 {
		report.CalmarRatio = report.CAGR / report.MaxDrawdownPct
	}

	if len(dailyReturns) > 1 {
		var sumRet float64
		for _, r := range dailyReturns {
			sumRet += r
		}
		meanDailyRet := sumRet / float64(len(dailyReturns))

		var sumSqDiff float64
		var sumDownsideSqDiff float64
		for _, r := range dailyReturns {
			diff := r - meanDailyRet
			sumSqDiff += diff * diff

			if r < 0 {
				sumDownsideSqDiff += r * r
			}
		}

		stdDevDaily := math.Sqrt(sumSqDiff / float64(len(dailyReturns)-1))
		downsideStdDev := math.Sqrt(sumDownsideSqDiff / float64(len(dailyReturns)))

		// Annualized Sharpe Ratio (assuming 0% risk-free rate)
		if stdDevDaily > 1e-12 {
			report.SharpeRatio = (meanDailyRet / stdDevDaily) * math.Sqrt(252.0)
		}

		// Annualized Sortino Ratio
		if downsideStdDev > 1e-12 {
			report.SortinoRatio = (meanDailyRet / downsideStdDev) * math.Sqrt(252.0)
		}
	}

	// 4. Benchmark analysis (Alpha & Beta)
	if len(benchmarkBars) > 0 && len(equityPoints) > 1 {
		var benchDailyReturns []float64
		var stratMatchedReturns []float64

		for i := 1; i < len(equityPoints); i++ {
			pDate := equityPoints[i-1].Date
			cDate := equityPoints[i].Date
			pBar, okP := benchmarkBars[pDate]
			cBar, okC := benchmarkBars[cDate]

			if okP && okC && pBar.Close > 0 {
				bRet := (cBar.Close - pBar.Close) / pBar.Close
				sRet := dailyReturns[i-1]
				benchDailyReturns = append(benchDailyReturns, bRet)
				stratMatchedReturns = append(stratMatchedReturns, sRet)
			}
		}

		if len(benchDailyReturns) > 5 {
			var bSum, sSum float64
			for i := 0; i < len(benchDailyReturns); i++ {
				bSum += benchDailyReturns[i]
				sSum += stratMatchedReturns[i]
			}
			bMean := bSum / float64(len(benchDailyReturns))
			sMean := sSum / float64(len(stratMatchedReturns))

			var covariance, bVariance float64
			for i := 0; i < len(benchDailyReturns); i++ {
				bDiff := benchDailyReturns[i] - bMean
				sDiff := stratMatchedReturns[i] - sMean
				covariance += bDiff * sDiff
				bVariance += bDiff * bDiff
			}

			if bVariance > 0 {
				beta := covariance / bVariance
				report.Beta = beta
				// Annualized Jensen's Alpha (assuming 0% risk-free rate)
				annualizedStratReturn := sMean * 252.0
				annualizedBenchReturn := bMean * 252.0
				report.Alpha = annualizedStratReturn - (beta * annualizedBenchReturn)
				report.BenchmarkReturnPct = (annualizedBenchReturn * report.TotalCalendarYears)
			}
		}
	}

	return report
}

// applyIdleDays counts sessions with no open position. A curve that never
// records cash, invested value, or an open position (sleeve PnL curves) leaves
// IdleKnown false so callers do not print a zero.
func applyIdleDays(report *models.PerformanceReport, curve []models.DailyEquityPoint) {
	hasOpen := false
	hasValue := false
	for _, pt := range curve {
		if pt.OpenPositions > 0 {
			hasOpen = true
		}
		if pt.Cash != 0 || pt.PositionsValue != 0 {
			hasValue = true
		}
	}
	if !hasOpen && !hasValue {
		return
	}
	idle := 0
	for _, pt := range curve {
		flat := pt.OpenPositions == 0
		if !hasOpen {
			flat = pt.PositionsValue == 0
		}
		if flat {
			idle++
		}
	}
	report.IdleDays = idle
	report.IdleKnown = true
}
