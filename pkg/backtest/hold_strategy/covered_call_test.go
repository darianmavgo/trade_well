package hold_strategy

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// A hold row with covered_call_otm_pct is simulated on option history; a plain hold row is not.
func TestCoveredCallRowsAreOverlaysAndPlainRowsAreNot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "strategies.db")
	db, err := refdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS hold_strategy (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, symbol TEXT NOT NULL,
		total_return INTEGER NOT NULL DEFAULT 0, allocation_pct REAL NOT NULL,
		cash_yield REAL NOT NULL, slippage_pct REAL NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if db, err = refdb.Open(path); err != nil { // an older table gains covered_call_otm_pct on open
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO hold_strategy (id, name, symbol, allocation_pct, cash_yield, slippage_pct, covered_call_otm_pct)
		VALUES ('zz-cc', 'ZZ covered call', 'DVY', 1, 0, 0.0005, 5), ('zz-plain', 'ZZ hold', 'DVY', 1, 0, 0.0005, 0), ('zz-bad', 'ZZ bad', 'DVY', 1, 0, 0.0005, 80)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	fam := NewFamily(func() string { return path })
	cc, ok := fam.Lookup("zz-cc")
	if !ok {
		t.Fatal("covered call row not found")
	}
	ov, isOverlay := cc.(strategy.OptionOverlayProvider)
	if !isOverlay {
		t.Fatalf("%T is not an option overlay", cc)
	}
	if spec := ov.OverlaySpec(); spec.Underlying != "DVY" || spec.OTMPct != 5 || spec.WindowYears != 2 {
		t.Fatalf("spec = %+v", spec)
	}
	if got := cc.GenerateSignals(nil); len(got) != 0 {
		t.Fatalf("a covered call emits no entry signals, got %d", len(got))
	}
	if syms := cc.(strategy.RequiredSymbolsProvider).RequiredSymbols(); len(syms) != 1 || syms[0] != "DVY" {
		t.Fatalf("required symbols %v", syms)
	}

	plain, ok := fam.Lookup("zz-plain")
	if !ok {
		t.Fatal("plain hold row not found")
	}
	if _, isOverlay := plain.(strategy.OptionOverlayProvider); isOverlay {
		t.Fatal("a plain hold row must not be an option overlay")
	}
	if _, ok := fam.Lookup("zz-bad"); ok {
		t.Fatal("a strike 80% out of the money must not build")
	}
}
