// Package sim is the portfolio simulator. It takes the signals a SQL pipeline
// wrote and plays them over hourly bars in time order: every fill and every
// exit is judged on the hour it can happen, so a target touched before a dip
// filled is never counted. The rules follow backtestgosqlite's pkg/simulator
// (exit order, sizing, shorts, cooldowns), applied hour by hour.
package sim

// NoHoldLimit is the holding window of a strategy that never exits on time.
const NoHoldLimit = 99999

// DefaultInitialCapital is the starting equity when the config gives none.
const DefaultInitialCapital = 100000

// DefaultAllocation is the share of equity per position when none is given.
const DefaultAllocation = 0.20

// Bar is one regular-hours hourly bar. T is "YYYY-MM-DD HH:MM", Eastern, the
// hour it starts; the session is T[:10].
type Bar struct {
	T                      string
	Open, High, Low, Close float64
	Volume                 float64 // shares traded in the hour (0 = unknown)
}

// Session is the bar's trading day.
func (b Bar) Session() string { return b.T[:10] }

// Signal is an order idea from the pipeline, dated on the session it was
// computed (known at that session's close).
type Signal struct {
	Symbol, Date string
	Entry        int // 1 open, -1 close
	Short        bool
	Close        float64 // the signal session's close
	BuyLimit     float64
	TakeProfit   float64 // absolute price, 0 = the config's percentage on the fill
	StopLoss     float64 // absolute price, 0 = the config's
	Alloc        float64 // override of the config allocation, 0 = none
	HoldDays     int     // override of the config holding window, 0 = none
}

// Config is the execution setup of one strategy.
type Config struct {
	InitialCapital float64
	Allocation     float64 // share of equity per position; unset -> DefaultAllocation
	FlatAlloc      bool    // every position is sized at Allocation: a signal's own Alloc is ignored
	DefaultAlloc   float64 // the share when neither the signal nor Allocation gives one; 0 -> DefaultAllocation
	CashYield      float64 // annual
	Slippage       float64
	Commission     float64 // per share, each side

	TakeProfitPct float64 // offset, 0.08 = +8%
	TargetPct     float64 // multiplier above 1, wins over TakeProfitPct
	StopLossPct   float64 // multiplier below 1 (0.93 = -7%); 0 = none
	HoldingWindow int     // sessions after the entry session; NoHoldLimit = none
	PositionCap   int
	TrailingStop  float64 // fraction below the running high; 0 = none

	ReentryCooldownDays int // sessions
	OneFillPerMonth     bool

	// How an entry signal becomes an order (at most one is set; none = buy the
	// signal session's close):
	NextDayOpenEntry  bool // buy the next session's first hour at its open
	NextDayLimitEntry bool // rest a buy limit at the signal's price through the next session
	// EntryLimitPct: > 0 rests a buy limit at this fraction of the previous close (the
	// close of the session before the entry session) through the entry session, 1.0 =
	// that close; < 0 buys at the entry session's open; 0 keeps the other entry rules.
	// The entry session is the one after the signal's, or the signal's own with EntrySameDay.
	EntryLimitPct float64
	EntrySameDay  bool

	// MaxBarVolumeShare caps an entry at this fraction of the shares traded in the hour it
	// fills (0.05 = 5%), so a small stock cannot be bought in sizes it never traded; the
	// position is cut to the cap, and no order is placed in an hour with no volume.
	// 0 = no cap.
	MaxBarVolumeShare float64

	// FixedShares, when > 0, buys that many shares whatever the equity (cut to what 95% of the
	// cash buys), so a one-share replay of a single signal does not depend on the account size.
	FixedShares int

	// FixedDollar, when > 0 (and FixedShares is not set), buys that many dollars of the stock per
	// position, cut to 95% of the cash when the cash is short.
	FixedDollar float64

	ShortBorrowAnnual float64
	CrisisStop        float64 // stop distance when a strategy has none (live pipeline: 0.20)
}

// Trade is one closed position. Entry and Exit are hourly timestamps.
type Trade struct {
	Strategy              string // the sleeve that traded it ("" for a single strategy)
	Symbol, Direction     string
	Entry, Exit           string
	EntryPrice, ExitPrice float64
	Shares                int
	HoldDays              int // sessions held after the entry session
	NetPnL, ReturnPct     float64
	Reason                string
	Target, Stop          float64 // the exit levels the position carried (0 = none)
}

// Point is the account at one session close.
type Point struct {
	Date          string
	Equity, Cash  float64
	OpenPositions int
}

// Result is a finished run.
type Result struct {
	Initial float64
	Trades  []Trade
	Curve   []Point
	// Park summarizes the parked leftover-cash book (zero value when no Park was set).
	Park ParkResult
	// Open holds positions entered in the final hour of the data. They are not trades (no later
	// bar to exit on), but a caller replaying one signal needs to tell them from an unfilled order.
	Open []OpenPosition
}

// OpenPosition is a position still held when the data ended.
type OpenPosition struct {
	Strategy, Symbol, Direction string
	Entry                       string
	EntryPrice                  float64
	Shares                      int
}

// Park holds a run's leftover cash in one symbol (the default asset). After each session's last
// hour the cash is swept into it at that session's close; an entry that needs cash sells just
// enough of it at the symbol's last price; a dividend on the shares is paid into cash. The lot is
// not a sleeve position, so a sleeve may hold the same symbol at the same time. Slippage and
// commission are the primary sleeve's. The lot is sold back to cash when the data ends, and that
// sale is not a trade.
type Park struct {
	Symbol string
	// Dividends maps a session (YYYY-MM-DD) to the cash dividend per share paid on it.
	Dividends map[string]float64
}

// ParkResult summarizes the parked book: its average share of equity, the dividends it paid and the
// sessions that ended with no park shares.
type ParkResult struct {
	Symbol       string
	AvgWeight    float64
	Dividends    float64
	DaysUnparked int
}
