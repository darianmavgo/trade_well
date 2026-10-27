package strategy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// ParkIDPrefix starts the ID of a park member, e.g. "park-googl". Get resolves
// any such ID without registration, so every symbol is a valid park.
const ParkIDPrefix = "park-"

// ResidualProvider is implemented by a stack member that is not a sleeve. It
// holds the account's leftover cash in one symbol instead of emitting
// signals, so the runner takes its symbol and passes it to the simulator as
// the default asset.
type ResidualProvider interface {
	ParkSymbol() string
}

// ParkStrategy parks leftover cash in Symbol. It emits no signals; the shared
// account simulator does the buying and selling (see SetDefaultAsset).
type ParkStrategy struct {
	Symbol string
}

// NewPark returns the park member for symbol.
func NewPark(symbol string) *ParkStrategy {
	return &ParkStrategy{Symbol: strings.ToUpper(strings.TrimSpace(symbol))}
}

func (p *ParkStrategy) ID() string { return ParkIDPrefix + strings.ToLower(p.Symbol) }

func (p *ParkStrategy) Name() string { return "Park in " + p.Symbol }

func (p *ParkStrategy) Description() string {
	return fmt.Sprintf("Holds leftover stack cash in %s; sold down to fund any entry. Valid only as a non-primary stack member.", p.Symbol)
}

func (p *ParkStrategy) ParkSymbol() string { return p.Symbol }

func (p *ParkStrategy) RequiredSymbols() []string { return []string{p.Symbol} }

func (p *ParkStrategy) DefaultConfig() StrategyConfig {
	return StrategyConfig{
		ID:            p.ID(),
		Name:          p.Name(),
		Description:   p.Description(),
		Benchmark:     "SPY",
		TargetPct:     999.0,
		StopLossPct:   0.0001,
		HoldingWindow: 99999,
		PositionCap:   1,
		AllocationPct: 1,
	}
}

func (p *ParkStrategy) Validate() error {
	if p.Symbol == "" {
		return fmt.Errorf("park strategy needs a symbol")
	}
	return nil
}

func (p *ParkStrategy) SetDatabases(marketDBPath, calcDBPath string) {}

func (p *ParkStrategy) GenerateSignals(map[string][]models.Bar) []models.Signal { return nil }

// tickerShape accepts a ticker with an optional one-letter class (BRK-B), so
// "park-buy-hold" stays the buy-and-hold of the ticker PARK.
var tickerShape = regexp.MustCompile(`^[a-z]{1,5}(-[a-z])?$`)

// parkFromID builds the park for an ID like "park-googl".
func parkFromID(id string) (Strategy, bool) {
	lower := strings.ToLower(strings.TrimSpace(id))
	if !strings.HasPrefix(lower, ParkIDPrefix) {
		return nil, false
	}
	sym := lower[len(ParkIDPrefix):]
	if !tickerShape.MatchString(sym) {
		return nil, false
	}
	return NewPark(sym), true
}

// SplitResidual removes park members from members and returns the remaining
// sleeves plus the park symbol ("" when there is none). A park cannot be the
// first member (the primary needs signals) and a stack may hold only one.
func SplitResidual(members []Strategy) (sleeves []Strategy, parkSymbol string, err error) {
	for i, m := range members {
		rp, ok := m.(ResidualProvider)
		if !ok {
			sleeves = append(sleeves, m)
			continue
		}
		if i == 0 {
			return nil, "", fmt.Errorf("%s cannot be the primary: a park emits no signals", m.ID())
		}
		if parkSymbol != "" {
			return nil, "", fmt.Errorf("a stack holds one park, got %s and %s", parkSymbol, rp.ParkSymbol())
		}
		parkSymbol = rp.ParkSymbol()
	}
	return sleeves, parkSymbol, nil
}
