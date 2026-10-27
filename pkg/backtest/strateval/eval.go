package strateval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/jmoiron/sqlx"
)

// EvalOptions controls one strategy evaluation.
type EvalOptions struct {
	Gates     Gates // zero value → DefaultGates()
	MarketDB  string
	Table     string
	OutDir    string
	Capital   float64
	StartDate string
	OOSMonths int
	Optimize  bool
	MaxTrials int
	Allowlist map[string]bool
	RunID     string
}

// EvalResult is the in-memory outcome for one strategy.
type EvalResult struct {
	Row       EvalRow
	ISReport  models.PerformanceReport
	OOSReport models.PerformanceReport
}

func metricsFrom(rep models.PerformanceReport) Metrics {
	return Metrics{
		StartDate:      rep.StartDate,
		EndDate:        rep.EndDate,
		CAGR:           rep.CAGR,
		Sharpe:         rep.SharpeRatio,
		MaxDD:          rep.MaxDrawdownPct,
		Trades:         rep.TotalTrades,
		WinRate:        rep.WinRate,
		AvgTradePct:    rep.AvgTradeReturnPct,
		TotalReturnPct: rep.TotalReturnPct,
		IdleDays:       rep.IdleDays,
		Days:           rep.TotalTradingDays,
	}
}

// EvaluateStrategy runs IS (+ optional coarse optimize on IS only) then a locked OOS pass.
func EvaluateStrategy(db *sqlx.DB, strat strategy.Strategy, opt EvalOptions) (EvalResult, error) {
	if opt.OOSMonths <= 0 {
		opt.OOSMonths = 12
	}
	if opt.Capital <= 0 {
		opt.Capital = 100000
	}
	if opt.Table == "" {
		opt.Table = "backtest_start"
	}
	if opt.OutDir == "" {
		opt.OutDir = filepath.Join(os.TempDir(), "strateval_runs")
	}
	if opt.MaxTrials <= 0 {
		opt.MaxTrials = 50
	}
	if opt.RunID == "" {
		opt.RunID = time.Now().UTC().Format("20060102T150405Z")
	}
	if err := os.MkdirAll(opt.OutDir, 0o755); err != nil {
		return EvalResult{}, err
	}

	req := runner.RequiredSymbolsFor([]strategy.Strategy{strat}, "")
	bars, dates, err := storage.FetchBars(db, opt.Table, req, opt.StartDate, "")
	if err != nil {
		return EvalResult{}, fmt.Errorf("fetch bars: %w", err)
	}
	if len(dates) == 0 {
		return EvalResult{}, fmt.Errorf("no bars for %s", strat.ID())
	}
	split, err := SplitDates(dates, opt.OOSMonths)
	if err != nil {
		return EvalResult{}, err
	}
	isDates := FilterDatesInclusive(dates, split.ISStart, split.ISEnd)
	oosDates := FilterDatesInclusive(dates, split.OOSStart, split.OOSEnd)

	cfg := runner.BuildConfig(strat, 0, 0, 0, 0)
	params := map[string]any{
		"hold": cfg.HoldingWindow, "stop_loss_pct": cfg.StopLossPct,
		"target_pct": cfg.TargetPct, "position_cap": cfg.PositionCap, "optimized": false,
	}

	if opt.Optimize {
		bestCfg, bestSharpe, trials := optimizeOnIS(strat, bars, isDates, opt)
		cfg = bestCfg
		params = map[string]any{
			"hold": cfg.HoldingWindow, "stop_loss_pct": cfg.StopLossPct,
			"target_pct": cfg.TargetPct, "position_cap": cfg.PositionCap,
			"optimized": true, "is_best_sharpe": bestSharpe, "trials": trials,
		}
	}

	isRes := runner.ExecuteStrategy(strat, cfg, bars, isDates, opt.Capital, "", opt.OutDir, opt.MarketDB)
	if isRes.Err != nil {
		return EvalResult{}, fmt.Errorf("IS run: %w", isRes.Err)
	}
	oosRes := runner.ExecuteStrategy(strat, cfg, bars, oosDates, opt.Capital, "", opt.OutDir, opt.MarketDB)
	if oosRes.Err != nil {
		return EvalResult{}, fmt.Errorf("OOS run: %w", oosRes.Err)
	}

	isM := metricsFrom(isRes.Report)
	oosM := metricsFrom(oosRes.Report)
	inAL := false
	if opt.Allowlist != nil {
		inAL = opt.Allowlist[strat.ID()] || opt.Allowlist[strings.ToLower(strat.ID())]
	}
	gates := DefaultGates()
	if opt.Gates.MinOOSTrades > 0 {
		gates.MinOOSTrades = opt.Gates.MinOOSTrades
	}
	if opt.Gates.MinOOSWinRate > 0 {
		gates.MinOOSWinRate = opt.Gates.MinOOSWinRate
	}
	if opt.Gates.MaxOOSDDAbs > 0 {
		gates.MaxOOSDDAbs = opt.Gates.MaxOOSDDAbs
	}
	if opt.Gates.MaxOOSDDMultiple > 0 {
		gates.MaxOOSDDMultiple = opt.Gates.MaxOOSDDMultiple
	}
	tier, reasons := AssignTier(isM, oosM, gates, inAL)
	pj, _ := json.Marshal(params)

	row := EvalRow{
		RunID:      opt.RunID,
		StrategyID: strat.ID(),
		ParamsJSON: string(pj),
		Optimized:  opt.Optimize,
		Split:      split,
		IS:         isM,
		OOS:        oosM,
		Tier:       tier,
		Reasons:    reasons,
		CreatedAt:  time.Now().UTC(),
	}
	return EvalResult{Row: row, ISReport: isRes.Report, OOSReport: oosRes.Report}, nil
}

func optimizeOnIS(
	strat strategy.Strategy,
	bars map[string][]models.Bar,
	isDates []string,
	opt EvalOptions,
) (strategy.StrategyConfig, float64, int) {
	space := strategy.AssessParameterSpace(strat)
	base := runner.BuildConfig(strat, 0, 0, 0, 0)

	holds := space.HoldDays
	if len(holds) == 0 {
		holds = []int{base.HoldingWindow}
	}
	tps := space.TakeProfits
	if len(tps) == 0 {
		tps = []float64{0}
	}
	sls := space.StopLosses
	if len(sls) == 0 {
		sls = []float64{0}
	}

	bestCfg := base
	bestSharpe := -1e99
	trials := 0
	tmp := filepath.Join(opt.OutDir, "optimize_scratch")
	_ = os.MkdirAll(tmp, 0o755)

	for _, h := range holds {
		for _, tpFrac := range tps {
			for _, slFrac := range sls {
				if trials >= opt.MaxTrials {
					if bestSharpe == -1e99 {
						return base, 0, trials
					}
					return bestCfg, bestSharpe, trials
				}
				stopMult := 0.0
				if slFrac > 0 && slFrac < 1 {
					stopMult = 1.0 - slFrac
				}
				targetMult := 0.0
				if tpFrac > 0 {
					targetMult = 1.0 + tpFrac
				}
				cfg := runner.BuildConfig(strat, stopMult, targetMult, h, 0)
				trials++
				res := runner.ExecuteStrategy(strat, cfg, bars, isDates, opt.Capital, "", tmp, opt.MarketDB)
				if res.Err != nil || res.Report.TotalTrades < 5 {
					continue
				}
				if res.Report.SharpeRatio > bestSharpe {
					bestSharpe = res.Report.SharpeRatio
					bestCfg = cfg
				}
			}
		}
	}
	if bestSharpe == -1e99 {
		return base, 0, trials
	}
	return bestCfg, bestSharpe, trials
}

// ParseAllowlist splits a comma-separated STRATEGY_ALLOWLIST into a set.
func ParseAllowlist(raw string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out[p] = true
		out[strings.ToLower(p)] = true
		if i := strings.IndexByte(p, '+'); i > 0 {
			prim := p[:i]
			out[prim] = true
			out[strings.ToLower(prim)] = true
		}
	}
	return out
}
