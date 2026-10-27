# 🛠️ Writing Custom Strategies in Go

Creating a custom trading strategy in `backtestgosqlite` takes under 35 lines of Go code. 

All strategies implement the unified `Strategy` interface and self-register into the engine's central registry.

---

## 1. The Strategy Interface

```go
type Strategy interface {
    ID() string
    Name() string
    Description() string
    DefaultConfig() StrategyConfig
    Validate() error
    GenerateSignals(barsBySymbol map[string][]models.Bar) []models.Signal
}
```

---

## 2. Complete Strategy Example: SMA 20/50 Golden Cross

Create a new file `pkg/backtest/strategy/sma_crossover.go`:

```go
package strategy

import (
    "sort"
    "github.com/darianmavgo/pkg/backtest/models"
)

// SMACrossoverStrategy implements moving average crossover.
type SMACrossoverStrategy struct{}

func init() {
    // Automatically register strategy into the engine CLI
    Register(&SMACrossoverStrategy{})
}

func (s *SMACrossoverStrategy) ID() string {
    return "sma-cross"
}

func (s *SMACrossoverStrategy) Name() string {
    return "SMA 20/50 Golden Cross"
}

func (s *SMACrossoverStrategy) Description() string {
    return "Enters when fast 20-day SMA crosses above slow 50-day SMA."
}

func (s *SMACrossoverStrategy) DefaultConfig() StrategyConfig {
    return StrategyConfig{
        ID:                 "sma-cross",
        Name:               s.Name(),
        Description:        s.Description(),
        TargetPct:          1.15,   // +15% profit target
        StopLossPct:        0.95,   // -5% stop loss
        HoldingWindow:      15,     // 15-day maximum holding
        PositionCap:        5,      // Max 5 concurrent positions
        AllocationPct:      0.20,   // 20% equity per position
        SlippagePct:        0.0005, // 0.05% slippage
        CommissionPerShare: 0.0001,
    }
}

func (s *SMACrossoverStrategy) Validate() error {
    return ValidateConfig(s.DefaultConfig())
}

func (s *SMACrossoverStrategy) GenerateSignals(barsBySymbol map[string][]models.Bar) []models.Signal {
    var signals []models.Signal

    for sym, bars := range barsBySymbol {
        if len(bars) < 55 {
            continue
        }
        sma20 := CalcSMA(bars, 20)
        sma50 := CalcSMA(bars, 50)

        for i := 50; i < len(bars); i++ {
            // Check crossover condition: SMA20 crosses above SMA50
            if sma20[i-1] <= sma50[i-1] && sma20[i] > sma50[i] {
                signals = append(signals, models.Signal{
                    Idx:       bars[i].Idx,
                    Symbol:    sym,
                    Date:      bars[i].Date,
                    Open:      bars[i].Open,
                    High:      bars[i].High,
                    Low:       bars[i].Low,
                    Close:     bars[i].Close,
                    Volume:    bars[i].Volume,
                    BuyLimit:  bars[i].Close,
                    OrderType: "market",
                    Entry:     1,
                })
            }
        }
    }

    // Sort signals chronologically
    sort.Slice(signals, func(i, j int) bool {
        if signals[i].Date == signals[j].Date {
            return signals[i].Symbol < signals[j].Symbol
        }
        return signals[i].Date < signals[j].Date
    })

    return signals
}
```

---

## 3. Running Your Strategy

Once saved, rebuild and execute:

```bash
# Verify it appears in the strategy list
./bin/backtest -strategylist

# Run backtest with tear sheet & HTML report
./bin/backtest -strategy sma-cross -capital 100000

# Rank every strategy that already has a result database
./bin/scoreboard compile
```

---

## 4. Built-in Technical Indicators Available

The engine provides zero-dependency vectorized mathematical indicators in `pkg/backtest/strategy/indicators.go`:
- `CalcSMA(bars, period)` — Simple Moving Average
- `CalcEMA(bars, period)` — Exponential Moving Average
- `CalcRSI(bars, period)` — Wilder's RSI (14, 5, 2, etc.)
- `CalcBollinger(bars, period, stdDevMult)` — Bollinger Bands (Upper, Middle, Lower)
- `CalcDonchian(bars, period)` — Donchian Channels (Upper, Middle, Lower)
- `CalcMACD(bars, fast, slow, signal)` — MACD Line, Signal Line, Histogram
- `CalcATR(bars, period)` — Average True Range
