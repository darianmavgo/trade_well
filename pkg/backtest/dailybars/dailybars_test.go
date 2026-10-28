package dailybars

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/calendar"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

func TestCompletedDropsTodayDuplicatesAndBadBars(t *testing.T) {
	bars := []models.Bar{
		{Date: "2026-10-20", Close: 100, AdjClose: 99},
		{Date: "2026-10-19", Close: 101, AdjClose: 100},
		{Date: "2026-10-19", Close: 999, AdjClose: 999}, // duplicate day
		{Date: "2026-10-21", Close: 0, AdjClose: 0},     // no close
		{Date: "2026-10-22", Close: 102, AdjClose: 101}, // after the last completed session
	}
	got := completed(bars, "2026-10-21")
	if len(got) != 2 || got[0].Date != "2026-10-19" || got[1].Date != "2026-10-20" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Close != 101 {
		t.Fatalf("kept the duplicate instead of the first bar: %+v", got[0])
	}
}

func TestReplaceSwapsOneSymbolOnly(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "market_history_daily.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := Replace(db, "QQQ", []models.Bar{{Date: "2026-10-19", Close: 1, AdjClose: 1, Volume: 5}, {Date: "2026-10-20", Close: 2, AdjClose: 2, Volume: 6}}); err != nil {
		t.Fatal(err)
	}
	if err := Replace(db, "SPY", []models.Bar{{Date: "2026-10-20", Close: 9, AdjClose: 9, Volume: 1}}); err != nil {
		t.Fatal(err)
	}
	// A fresh download rescales history: the old QQQ rows must be replaced, not merged.
	if err := Replace(db, "QQQ", []models.Bar{{Date: "2026-10-20", Close: 3, AdjClose: 3, Volume: 7}}); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM daily_bar WHERE symbol = 'QQQ'`); err != nil || n != 1 {
		t.Fatalf("QQQ rows = %d, err %v; want 1", n, err)
	}
	if err := db.Get(&n, `SELECT COUNT(*) FROM daily_bar WHERE symbol = 'SPY'`); err != nil || n != 1 {
		t.Fatalf("SPY rows = %d, err %v; want 1 (untouched)", n, err)
	}
	var src string
	if err := db.Get(&src, `SELECT source FROM daily_bar WHERE symbol = 'QQQ'`); err != nil || src != Source {
		t.Fatalf("source = %q, err %v", src, err)
	}
}

func TestRunRejectsBadStart(t *testing.T) {
	if _, err := Run(t.Context(), Config{DB: filepath.Join(t.TempDir(), "x.db"), Start: "2010/01/01"}, nil); err == nil {
		t.Fatal("a malformed start date should be an error")
	}
}

func bar(date string, adj float64) models.Bar {
	return models.Bar{Date: date, Close: adj, AdjClose: adj, Volume: 1}
}

func TestReconcileAppendsOnlyNewDays(t *testing.T) {
	held := []Stored{{"2026-10-20", 100}, {"2026-10-21", 101}}
	add, restated := Reconcile(held, []models.Bar{bar("2026-10-20", 100), bar("2026-10-21", 101), bar("2026-10-22", 102), bar("2026-10-23", 103)})
	if restated || len(add) != 2 || add[0].Date != "2026-10-22" || add[1].Date != "2026-10-23" {
		t.Fatalf("restated=%v add=%+v", restated, add)
	}
	if add, restated := Reconcile(held, []models.Bar{bar("2026-10-20", 100), bar("2026-10-21", 101)}); restated || len(add) != 0 {
		t.Fatalf("a download with nothing new: restated=%v add=%+v", restated, add)
	}
}

// A dividend rescales every earlier adjusted close, so a held day that now differs means the
// stored history is out of date and all of it must be downloaded again.
func TestReconcileDetectsARestatedHistory(t *testing.T) {
	held := []Stored{{"2026-10-20", 100}, {"2026-10-21", 101}}
	if _, restated := Reconcile(held, []models.Bar{bar("2026-10-20", 99.5), bar("2026-10-21", 100.5), bar("2026-10-22", 102)}); !restated {
		t.Fatal("a rescaled held day was not detected")
	}
	// Nothing in common cannot be checked, so it is treated the same way.
	if _, restated := Reconcile(held, []models.Bar{bar("2026-10-22", 102)}); !restated {
		t.Fatal("a download sharing no day with the held bars must not be trusted")
	}
	// Nothing held: everything is new.
	if add, restated := Reconcile(nil, []models.Bar{bar("2026-10-22", 102)}); restated || len(add) != 1 {
		t.Fatalf("empty store: restated=%v add=%+v", restated, add)
	}
}

func TestAppendKeepsOlderDaysAndReplacesARepeatedOne(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "market_history_daily.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Replace(db, "QQQ", []models.Bar{bar("2026-10-20", 1), bar("2026-10-21", 2)}); err != nil {
		t.Fatal(err)
	}
	if err := Append(db, "QQQ", []models.Bar{bar("2026-10-21", 5), bar("2026-10-22", 6)}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM daily_bar WHERE symbol = 'QQQ'`); err != nil || n != 3 {
		t.Fatalf("rows = %d, err %v; want 3", n, err)
	}
	var adj float64
	if err := db.Get(&adj, `SELECT adj_close FROM daily_bar WHERE symbol = 'QQQ' AND date = '2026-10-21'`); err != nil || adj != 5 {
		t.Fatalf("the repeated day = %v, err %v; want 5", adj, err)
	}
}

// A symbol already current through the last completed session is not downloaded: Run does no
// network call for it (there is none in this test to make).
func TestRunDownloadsNothingForACurrentSymbol(t *testing.T) {
	path := filepath.Join(t.TempDir(), "market_history_daily.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 23, 22, 0, 0, 0, time.UTC) // Friday after the close
	last := calendar.LastCompletedSession(now).Format("2006-01-02")
	if err := Replace(db, "QQQ", []models.Bar{bar(last, 1)}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	var out bytes.Buffer
	res, err := Run(context.Background(), Config{DB: path, Symbols: []string{"QQQ"}, Now: now}, &out)
	if err != nil || len(res) != 1 || res[0].Bars != 0 || res[0].Last != last {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !strings.Contains(out.String(), "nothing to download") {
		t.Fatalf("output %q", out.String())
	}
	if d, ok := LastDate(path, "QQQ"); !ok || d != last {
		t.Fatalf("LastDate = %q %v", d, ok)
	}
}
