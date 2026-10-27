package stackopt

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
)

func TestTagDoesNotMutateSource(t *testing.T) {
	src := []models.Signal{{Symbol: "A"}, {Symbol: "B"}}
	out := tag(src, "s1", 3)
	if out[0].StrategyID != "s1" || out[1].Priority != 3 {
		t.Fatalf("tag = %+v", out)
	}
	if src[0].StrategyID != "" || src[0].Priority != 0 {
		t.Fatal("tag changed the cached signals")
	}
}

func TestNextDay(t *testing.T) {
	if got := nextDay("2025-12-31"); got != "2026-01-01" {
		t.Fatalf("nextDay = %s", got)
	}
}

func TestUpsertStackWritesNameThenID(t *testing.T) {
	db, err := refdb.Open(filepath.Join(t.TempDir(), "strategies.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res := &Result{StackID: "a+b", RunDir: "/x/12"}
	res.InSample.CAGR = 0.5
	if err := upsertStack(db, "My Stack", res); err != nil {
		t.Fatal(err)
	}
	st, ok, err := refdb.StackByName(db, "my-stack")
	if err != nil || !ok || st.ID != "a+b" || st.Name != "My Stack" {
		t.Fatalf("StackByName = %+v ok=%v err=%v", st, ok, err)
	}
}
