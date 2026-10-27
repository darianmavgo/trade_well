package runner

import (
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// A park must reach the simulator as DefaultAsset, never as a sleeve.
func TestExecuteStackRejectsParkAsSleeve(t *testing.T) {
	primary, ok := strategy.Get("price-action-reclaim")
	if !ok {
		t.Fatal("price-action-reclaim not registered")
	}
	res := ExecuteStack(StackRequest{
		Primary:     primary,
		Secondaries: []strategy.Strategy{strategy.NewPark("GOOGL")},
		Capital:     100000,
	})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "DefaultAsset") {
		t.Fatalf("want DefaultAsset error, got %v", res.Err)
	}
}
