package runner

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func TestLoadIntradayReturnsRegularSessionHoursInEasternTime(t *testing.T) {
	market := filepath.Join(t.TempDir(), "market.db")
	path := appenv.BarDB(market, "1h") // hourly bars live beside the daily database
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	// The real table declares Date as DATETIME, which the driver hands back as a time.
	if _, err := db.Exec(`CREATE TABLE backtest_start (idx INTEGER, Date DATETIME, timeframe TEXT, open FLOAT, high FLOAT, low FLOAT, close FLOAT, volume BIGINT, symbol TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, r := range [][2]string{
		{"2026-01-05 12:00:00", "premarket"}, // 07:00 ET winter, dropped
		{"2026-01-05 14:00:00", "9:00 ET"},   // EST is UTC-5
		{"2026-01-05 20:00:00", "15:00 ET"},
		{"2026-01-05 21:00:00", "after close"},
		{"2026-07-06 13:00:00", "9:00 ET"}, // EDT is UTC-4
	} {
		if _, err := db.Exec(`INSERT INTO backtest_start VALUES (1, ?, '1h', 1, 2, 0.5, 1.5, 10, 'AAA')`, r[0]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO backtest_start VALUES (1, '2026-01-05', '1d', 1, 2, 0.5, 1.5, 10, 'AAA')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	got, err := LoadIntraday(market, []string{"AAA"})
	if err != nil {
		t.Fatal(err)
	}
	var dates []string
	for _, b := range got["AAA"] {
		dates = append(dates, b.Date)
	}
	want := []string{"2026-01-05 09:00", "2026-01-05 15:00", "2026-07-06 09:00"}
	if len(dates) != len(want) {
		t.Fatalf("got %v want %v", dates, want)
	}
	for i := range want {
		if dates[i] != want[i] {
			t.Fatalf("got %v want %v", dates, want)
		}
	}
}
