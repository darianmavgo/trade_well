package models

import "time"

// Bar represents a single OHLCV price bar for an asset.
type Bar struct {
	Idx        int       `db:"idx" json:"idx,omitempty"`
	Symbol     string    `db:"symbol" json:"symbol"`
	Date       string    `db:"Date" json:"date"`
	Timeframe  string    `db:"timeframe" json:"timeframe,omitempty"`     // e.g. "1d", "1h", "5m" (defaults to "1d")
	AssetClass string    `db:"asset_class" json:"asset_class,omitempty"` // e.g. "equity"
	Open       float64   `db:"open" json:"open"`
	High       float64   `db:"high" json:"high"`
	Low        float64   `db:"low" json:"low"`
	Close      float64   `db:"close" json:"close"`
	AdjClose   float64   `db:"Adj Close" json:"adj_close,omitempty"`
	Volume     int64     `db:"volume" json:"volume"`
	ParsedDt   time.Time `json:"-"`
}

// Signal represents a trade trigger detected by a strategy.
type Signal struct {
	Idx      int     `db:"idx" json:"idx"`
	Symbol   string  `db:"symbol" json:"symbol"`
	Date     string  `db:"date" json:"date"`
	Open     float64 `db:"open" json:"open"`
	High     float64 `db:"high" json:"high"`
	Low      float64 `db:"low" json:"low"`
	Close    float64 `db:"close" json:"close"`
	Volume   int64   `db:"volume" json:"volume"`
	BuyLimit float64 `db:"buylimit" json:"buylimit"`
	// Entry is 1 (or 0) to open. Negative closes an existing position in this
	// symbol at this bar and does not open a new one.
	Entry      int                `db:"entry" json:"entry"`
	OrderType  string             `db:"order_type" json:"order_type,omitempty"` // "limit", "market", "stop_limit" (default: "limit")
	StopLoss   float64            `db:"stop_loss" json:"stop_loss,omitempty"`
	TakeProfit float64            `db:"take_profit" json:"take_profit,omitempty"`
	Metadata   map[string]float64 `json:"metadata,omitempty"`
	AssetClass string             `db:"asset_class" json:"asset_class,omitempty"`

	// Direction indicates whether this is a LONG or SHORT entry signal.
	// Set by strategies that trade multiple directions (e.g. sig-voo-buy-spxu).
	Direction string `db:"direction" json:"direction,omitempty"` // "LONG" or "SHORT"

	// Regime records the market regime in effect when the signal fired.
	// For observability only — regime filtering is done inside GenerateSignals.
	Regime string `db:"regime" json:"regime,omitempty"` // e.g. "VOO<SMA200", "All Regimes"

	// HoldDaysOverride, when > 0, overrides the strategy-level HoldingWindow
	// for this specific signal. Used by multi-leg strategies with different
	// hold periods per leg (e.g. long=8 days, short=2 days).
	HoldDaysOverride int `db:"hold_days_override" json:"hold_days_override,omitempty"`

	// AllocationPctOverride, when > 0, overrides the strategy-level AllocationPct
	// for this specific signal based on probability or conviction.
	AllocationPctOverride float64 `db:"allocation_pct_override" json:"allocation_pct_override,omitempty"`

	// StrategyID identifies the strategy that generated this signal (e.g. "sig-voo-buy-tecl", "bb-capitulation").
	StrategyID string `db:"strategy_id" json:"strategy_id,omitempty"`

	// AfterFill, set by the simulator's limit-entry model when intraday bars are
	// available, holds the intraday bars of the entry session from the fill bar
	// on. The same-day exit is judged on these in order instead of on the daily
	// high and low. The fill bar's High is cut down to the fill price (the order
	// of touches inside one bar is unknown, so only its low can stop the trade).
	AfterFill []Bar `db:"-" json:"-"`

	// Priority denotes the execution priority tier (0 = primary, 1 = secondary, etc.).
	Priority int `db:"priority" json:"priority"`
}

// ExitReason represents the trigger that closed a trade.
type ExitReason string

const (
	ExitReasonProfitTarget ExitReason = "PROFIT_TARGET"
	ExitReasonStopLoss     ExitReason = "STOP_LOSS"
	ExitReasonTrailingStop ExitReason = "TRAILING_STOP"
	ExitReasonATRStop      ExitReason = "ATR_STOP"
	ExitReasonTimeUp       ExitReason = "TIME_UP"
	ExitReasonEndBacktest  ExitReason = "END_OF_DATA"
	ExitReasonPreempted    ExitReason = "PREEMPTED_BY_PRIMARY"
	ExitReasonSignal       ExitReason = "SIGNAL"
)

// Trade represents an executed trade with full lifecycle metrics.
type Trade struct {
	ID                    int        `json:"id"`
	StrategyID            string     `json:"strategy_id,omitempty"`
	Symbol                string     `json:"symbol"`
	OrderType             string     `json:"order_type,omitempty"`
	EntryIdx              int        `json:"entry_idx"`
	EntryDate             string     `json:"entry_date"`
	EntryPrice            float64    `json:"entry_price"`
	TargetPrice           float64    `json:"target_price"`
	StopLossPrice         float64    `json:"stop_loss_price"`
	ExitDate              string     `json:"exit_date"`
	ExitPrice             float64    `json:"exit_price"`
	ExitReason            ExitReason `json:"exit_reason"`
	Shares                int        `json:"shares"`
	InvestedCapital       float64    `json:"invested_capital"`
	GrossPnL              float64    `json:"gross_pnl"`
	NetPnL                float64    `json:"net_pnl"`
	ReturnPct             float64    `json:"return_pct"`
	HoldDays              int        `json:"hold_days"`
	CommissionPaid        float64    `json:"commission_paid"`
	MaxAdverseExcursion   float64    `json:"mae_pct"` // Maximum adverse drawdown during holding period (%)
	MaxFavorableExcursion float64    `json:"mfe_pct"` // Maximum favorable unrealized gain during holding period (%)
}

// Position tracks currently held active assets in the portfolio.
type Position struct {
	StrategyID string `json:"strategy_id,omitempty"`
	Priority   int    `json:"priority"`
	Symbol     string `json:"symbol"`
	Shares     int    `json:"shares"`
	// Direction is "SHORT" for a cash-secured short. Empty means long.
	Direction         string  `json:"direction,omitempty"`
	OrderType         string  `json:"order_type,omitempty"`
	EntryPrice        float64 `json:"entry_price"`
	EntryDate         string  `json:"entry_date"`
	CurrentPrice      float64 `json:"current_price"`
	TargetPrice       float64 `json:"target_price"`
	StopLossPrice     float64 `json:"stop_loss_price"`
	TrailingStopPrice float64 `json:"trailing_stop_price,omitempty"`
	ATRStopPrice      float64 `json:"atr_stop_price,omitempty"`
	HoldDays          int     `json:"hold_days"`
	UnrealizedPnL     float64 `json:"unrealized_pnl"`
	MinLowSince       float64 `json:"min_low_since"`
	MaxHighSince      float64 `json:"max_high_since"`
	// HoldDaysOverride, when > 0, overrides the strategy-level HoldingWindow for this position.
	// Populated from Signal.HoldDaysOverride at entry time.
	HoldDaysOverride int `json:"hold_days_override,omitempty"`
}

// Account encapsulates the real-time financial ledger.
type Account struct {
	InitialCash     float64 `json:"initial_cash"`
	Cash            float64 `json:"cash"`
	PortfolioValue  float64 `json:"portfolio_value"`
	TotalEquity     float64 `json:"total_equity"`
	RealizedPnL     float64 `json:"realized_pnl"`
	UnrealizedPnL   float64 `json:"unrealized_pnl"`
	TotalReturnPct  float64 `json:"total_return_pct"`
	PeakEquity      float64 `json:"peak_equity"`
	CurrentDrawdown float64 `json:"current_drawdown"`
}

// DailyEquityPoint logs daily portfolio valuation for equity curve and drawdown charts.
type DailyEquityPoint struct {
	Date           string  `json:"date"`
	Cash           float64 `json:"cash"`
	PositionsValue float64 `json:"positions_value"`
	TotalEquity    float64 `json:"total_equity"`
	DailyReturn    float64 `json:"daily_return"`
	DrawdownPct    float64 `json:"drawdown_pct"`
	OpenPositions  int     `json:"open_positions"`
	BuyingPower    float64 `json:"buying_power,omitempty"`
	MarginDebt     float64 `json:"margin_debt,omitempty"`
	MarginInterest float64 `json:"margin_interest,omitempty"`
	DividendIncome float64 `json:"dividend_income,omitempty"`
}

// PerformanceReport aggregates institutional quantitative performance metrics.
type PerformanceReport struct {
	StartDate               string  `json:"start_date"`
	EndDate                 string  `json:"end_date"`
	TotalTradingDays        int     `json:"total_trading_days"`
	TotalCalendarYears      float64 `json:"total_calendar_years"`
	InitialCapital          float64 `json:"initial_capital"`
	FinalEquity             float64 `json:"final_equity"`
	NetProfit               float64 `json:"net_profit"`
	TotalReturnPct          float64 `json:"total_return_pct"`
	CAGR                    float64 `json:"cagr"`
	SharpeRatio             float64 `json:"sharpe_ratio"`
	SortinoRatio            float64 `json:"sortino_ratio"`
	CalmarRatio             float64 `json:"calmar_ratio"`
	OmegaRatio              float64 `json:"omega_ratio"`
	UlcerIndex              float64 `json:"ulcer_index"`
	Alpha                   float64 `json:"alpha"`
	Beta                    float64 `json:"beta"`
	BenchmarkReturnPct      float64 `json:"benchmark_return_pct"`
	MaxDrawdownPct          float64 `json:"max_drawdown_pct"`
	MaxDrawdownDollars      float64 `json:"max_drawdown_dollars"`
	MaxDrawdownPeakEquity   float64 `json:"max_drawdown_peak_equity"`
	MaxDrawdownTroughEquity float64 `json:"max_drawdown_trough_equity"`
	MaxDrawdownPeakDate     string  `json:"max_drawdown_peak_date"`
	MaxDrawdownTroughDate   string  `json:"max_drawdown_trough_date"`
	MaxDrawdownDuration     int     `json:"max_drawdown_days"`
	TotalTrades             int     `json:"total_trades"`
	WinningTrades           int     `json:"winning_trades"`
	LosingTrades            int     `json:"losing_trades"`
	WinRate                 float64 `json:"win_rate"`
	ProfitFactor            float64 `json:"profit_factor"`
	AvgTradeReturnPct       float64 `json:"avg_trade_return_pct"`
	AvgWinAmount            float64 `json:"avg_win_amount"`
	AvgLossAmount           float64 `json:"avg_loss_amount"`
	PayoffRatio             float64 `json:"payoff_ratio"`
	AvgHoldingDays          float64 `json:"avg_holding_days"`
	AvgMAE                  float64 `json:"avg_mae"`
	AvgMFE                  float64 `json:"avg_mfe"`
	TotalCommissionPaid     float64 `json:"total_commission_paid"`
	// IdleDays is the number of sessions with no open position. IdleKnown is
	// false when the equity curve did not record cash or positions (a sleeve
	// curve built from trade PnL only), so a zero is not reported as fact.
	IdleDays  int  `json:"idle_days,omitempty"`
	IdleKnown bool `json:"idle_known,omitempty"`
}

// DailySnapshot records a single day's mark-to-market portfolio state for equity curves and drawdown charts.
type DailySnapshot struct {
	Date      string  `json:"date"`
	Equity    float64 `json:"equity"`
	Drawdown  float64 `json:"drawdown"`
	ActivePos string  `json:"active_pos,omitempty"`
}
