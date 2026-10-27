package runner

import (
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/hold_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// A park must reach the simulator as DefaultAsset, never as a sleeve.
func TestExecuteStackRejectsParkAsSleeve(t *testing.T) {
	primary := &hold_strategy.Strategy{Row: refdb.HoldStrategy{ID: "hold-voo", Name: "Hold VOO", Symbol: "VOO", AllocationPct: 1}}
	res := ExecuteStack(StackRequest{
		Primary:     primary,
		Secondaries: []strategy.Strategy{hold_strategy.NewPark("GOOGL")},
		Capital:     100000,
	})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "DefaultAsset") {
		t.Fatalf("want DefaultAsset error, got %v", res.Err)
	}
}
