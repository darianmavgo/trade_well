package submit_orders

import (
	"testing"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

func TestEntryDurationForIsDayUnlessTheStrategyAskedForGTC(t *testing.T) {
	for in, want := range map[string]string{
		"":                            trader.DurationDay, // rows staged before the duration was recorded
		stage_orders.EntryDurationDay: trader.DurationDay,
		stage_orders.EntryDurationGTC: trader.DurationGTC,
	} {
		if got := entryDurationFor(stage_orders.StagedOrder{EntryDuration: in}); got != want {
			t.Errorf("entryDurationFor(%q) = %q, want %q", in, got, want)
		}
	}
}
