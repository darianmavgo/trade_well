package runner

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/hold_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
)

// A family name in -strategy stands for every row of that family.
func TestResolveStrategiesExpandsAFamilyName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "strategies.db")
	db, err := refdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS hold_strategy (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, symbol TEXT NOT NULL,
		total_return INTEGER NOT NULL DEFAULT 0, allocation_pct REAL NOT NULL,
		cash_yield REAL NOT NULL, slippage_pct REAL NOT NULL,
		trailing_stop_pct REAL NOT NULL DEFAULT 0, sma_reentry_period INTEGER NOT NULL DEFAULT 0);
		INSERT INTO hold_strategy (id, name, symbol, total_return, allocation_pct, cash_yield, slippage_pct) VALUES ('hold-zzres-a','A','VOO',0,1,0,0), ('hold-zzres-b','B','QQQ',0,1,0,0)`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	hold_strategy.RegisterFrom(path)

	got, err := ResolveStrategies("hold", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID() != "hold-zzres-a" || got[1].ID() != "hold-zzres-b" {
		t.Fatalf("got %d strategies: %v", len(got), got)
	}
	one, err := ResolveStrategies("hold-zzres-b,hold", "")
	if err != nil || len(one) != 3 {
		t.Fatalf("an id and a family together: %d strategies, err %v", len(one), err)
	}
}
