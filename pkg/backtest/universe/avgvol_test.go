package universe

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func TestParseWindows(t *testing.T) {
	got, err := ParseWindows(" 20, 60,20 ")
	if err != nil || !reflect.DeepEqual(got, []int{20, 60}) {
		t.Fatalf("got %v %v", got, err)
	}
	for _, bad := range []string{"", "0", "-5", "abc"} {
		if _, err := ParseWindows(bad); err == nil {
			t.Errorf("ParseWindows(%q) should fail", bad)
		}
	}
}

// A real market database holding literal bars: AAA has five daily bars and one
// minute bar that must not count, BBB has two.
func marketWithVolumes(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "market.db")
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE backtest_start (idx INTEGER, symbol TEXT, Date TEXT, timeframe TEXT,
		open REAL, high REAL, low REAL, close REAL, volume INTEGER, "Adj Close" REAL)`); err != nil {
		t.Fatal(err)
	}
	ins := func(sym, date string, vol int) {
		if _, err := db.Exec(`INSERT INTO backtest_start (symbol, Date, timeframe, open, high, low, close, volume, "Adj Close")
			VALUES (?, ?, '1d', 1, 1, 1, 1, ?, 1)`, sym, date, vol); err != nil {
			t.Fatal(err)
		}
	}
	for i, v := range []int{100, 200, 300, 400, 500} {
		ins("AAA", "2026-01-0"+string(rune('1'+i)), v)
	}
	ins("AAA", "2026-01-05 09:31:00", 999999) // a minute bar
	ins("BBB", "2026-01-02", 10)
	ins("BBB", "2026-01-03", 30)
	return path
}

func TestAvgVolAveragesTheLastNDailyBars(t *testing.T) {
	market := marketWithVolumes(t)
	uni := filepath.Join(t.TempDir(), "universe.db")
	if _, err := AvgVol(AvgVolConfig{DBPath: uni, MarketDB: market, Windows: []int{2, 20}}); err != nil {
		t.Fatal(err)
	}
	db, err := Open(uni)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	type row struct {
		Symbol string  `db:"symbol"`
		Window int     `db:"window_days"`
		Avg    float64 `db:"avg_volume"`
		Used   int     `db:"bars_used"`
		First  string  `db:"first_date"`
		Last   string  `db:"last_date"`
	}
	var rows []row
	if err := db.Select(&rows, `SELECT symbol, window_days, avg_volume, bars_used, first_date, last_date FROM avg_volume ORDER BY symbol, window_days`); err != nil {
		t.Fatal(err)
	}
	want := []row{
		{"AAA", 2, 450, 2, "2026-01-04", "2026-01-05"},  // last two daily bars: 400 and 500
		{"AAA", 20, 300, 5, "2026-01-01", "2026-01-05"}, // fewer than 20 bars: all five, the minute bar left out
		{"BBB", 2, 20, 2, "2026-01-02", "2026-01-03"},
		{"BBB", 20, 20, 2, "2026-01-02", "2026-01-03"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("avg_volume\n got %+v\nwant %+v", rows, want)
	}

	// A rerun replaces the rows of its window and leaves the other windows alone.
	if _, err := AvgVol(AvgVolConfig{DBPath: uni, MarketDB: market, Windows: []int{2}}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM avg_volume`); err != nil || n != 4 {
		t.Fatalf("after a rerun there are %d rows, want 4 (%v)", n, err)
	}
}

func TestPlanVerificationSkipsWhatIsAlreadyKnown(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	discovered := []RawTicker{{Symbol: "NEW"}, {Symbol: "KNOWN"}, {Symbol: "FAILED_OLD"}, {Symbol: "FAILED_RECENT"}}
	have := map[string]known{
		"KNOWN":         {FirstTradeDate: "2010-01-04", UpdatedAt: "2026-09-01 00:00:00"},
		"FAILED_OLD":    {UpdatedAt: "2026-09-01 00:00:00"},
		"FAILED_RECENT": {UpdatedAt: "2026-10-02 00:00:00"},
	}
	verify, known, recent := planVerification(discovered, have, false, 7, now)
	got := []string{}
	for _, v := range verify {
		got = append(got, v.Symbol)
	}
	if !reflect.DeepEqual(got, []string{"NEW", "FAILED_OLD"}) || known != 1 || recent != 1 {
		t.Fatalf("verify %v, skipped known %d recent %d", got, known, recent)
	}
	verify, known, recent = planVerification(discovered, have, true, 7, now)
	if len(verify) != 4 || known != 0 || recent != 0 {
		t.Fatalf("-refresh should verify all four, got %d (skipped %d, %d)", len(verify), known, recent)
	}
}
