package hold_strategy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// ParkIDPrefix starts the ID of a park member, e.g. "park-googl". The hold family
// resolves any such ID without a stored row, so every symbol is a valid park.
const ParkIDPrefix = "park-"

// Park is a hold row that parks a stack's leftover cash in its symbol. It emits no
// signals; the shared account simulator does the buying and selling (see
// strategy.ResidualProvider). Its row is built from the ID, not stored.
type Park struct {
	*Strategy
}

// NewPark returns the park member for symbol.
func NewPark(symbol string) *Park {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	return &Park{Strategy: &Strategy{Row: refdb.HoldStrategy{
		ID:            ParkIDPrefix + strings.ToLower(sym),
		Name:          "Park in " + sym,
		Symbol:        sym,
		AllocationPct: 1,
	}}}
}

func (p *Park) Description() string {
	return fmt.Sprintf("Holds leftover stack cash in %s; sold down to fund any entry. Valid only as a non-primary stack member.", p.Row.Symbol)
}

func (p *Park) ParkSymbol() string { return p.Row.Symbol }

// DefaultConfig is the hold config with the park's own description and benchmark.
func (p *Park) DefaultConfig() strategy.StrategyConfig {
	cfg := p.Strategy.DefaultConfig()
	cfg.Description = p.Description()
	cfg.Benchmark = "SPY"
	return cfg
}

func (p *Park) Validate() error {
	if p.Row.Symbol == "" {
		return fmt.Errorf("park strategy needs a symbol")
	}
	return p.Strategy.Validate()
}

func (p *Park) GenerateSignals(map[string][]models.Bar) []models.Signal { return nil }

// tickerShape accepts a ticker with an optional one-letter class (BRK-B), so
// "park-buy-hold" stays the buy-and-hold of the ticker PARK.
var tickerShape = regexp.MustCompile(`^[a-z]{1,5}(-[a-z])?$`)

// parkFromID builds the park for an ID like "park-googl".
func parkFromID(id string) (strategy.Strategy, bool) {
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

// parkFamily is the hold family that also answers park-<symbol> for any symbol.
type parkFamily struct {
	*strategy.RowFamily
}

func (f parkFamily) Lookup(id string) (strategy.Strategy, bool) {
	if s, ok := parkFromID(id); ok {
		return s, true
	}
	return f.RowFamily.Lookup(id)
}

func (f parkFamily) LookupExact(id string) (strategy.Strategy, bool) {
	if s, ok := parkFromID(id); ok {
		return s, true
	}
	return f.RowFamily.LookupExact(id)
}
