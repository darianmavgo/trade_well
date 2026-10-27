package rotation_strategy

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/realbars"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	sqlfiles "github.com/darianmavgo/trade_well/sql"
)

// periodRow is a biggest-winner style row: top 1, the whole market, one side.
func periodRow(period, side string) refdb.RotationStrategy {
	return refdb.RotationStrategy{
		ID: "rot-period-" + period + "-" + side, Name: "period " + period, TopK: 1, UniverseSize: 1,
		MaxWeightPct: 1, RegimeSymbol: "QQQ", AllocationPct: 1, SlippagePct: 0.0005,
		Period: period, Side: side, Pick: "winner",
	}
}

// periodKey is the calendar period of a date, worked out here in Go and not by
// the SQL under test.
func periodKey(period, date string) string {
	d, _ := time.Parse("2006-01-02", date)
	switch period {
	case "1d":
		return date
	case "1w":
		return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7)).Format("2006-01-02")
	case "1m":
		return date[:7]
	case "1q":
		return fmt.Sprintf("%sQ%d", date[:4], (int(d.Month())+2)/3)
	}
	return date[:4]
}

// gappedMarketDB is marketDB with each session opening at the previous close, so
// a one day period has a return (open against close) and the same ranking as the
// close to close moves in closeAt.
func gappedMarketDB(t *testing.T) (string, []string, map[string][]models.Bar) {
	t.Helper()
	path, dates, _ := marketDB(t)
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bars := map[string][]models.Bar{}
	for _, sym := range testSymbols {
		for i, date := range dates {
			c, o := closeAt(sym, i), closeAt(sym, i-1)
			if _, err := db.Exec(`UPDATE backtest_start SET open = ?, high = ?, low = ? WHERE symbol = ? AND Date = ?`,
				o, max(o, c), min(o, c), sym, date); err != nil {
				t.Fatal(err)
			}
			bars[sym] = append(bars[sym], models.Bar{Symbol: sym, Date: date, Open: o, High: max(o, c), Low: min(o, c), Close: c, Volume: 1000000})
		}
	}
	return path, dates, bars
}

// UP1 is the strongest name in every period of the literal bars, so a top 1
// period row buys it at the start of every period after the first and sells it
// on the period's last session (the next session for a one session period).
func TestPeriodRowsOnLiteralBars(t *testing.T) {
	for _, period := range PeriodIDs {
		t.Run(period, func(t *testing.T) {
			market, dates, bars := gappedMarketDB(t)
			s := &Strategy{Row: periodRow(period, "long")}
			if err := s.Validate(); err != nil {
				t.Fatal(err)
			}
			s.SetDatabases(market, filepath.Join(t.TempDir(), "calc.db"))
			got := map[string][]string{}
			for _, sig := range s.GenerateSignals(bars) {
				if sig.Symbol != "UP1" {
					t.Fatalf("%s: bought %s, want only UP1", period, sig.Symbol)
				}
				got[sig.Date] = append(got[sig.Date], fmt.Sprint(sig.Entry))
			}

			first := map[string]string{}
			last := map[string]string{}
			for _, d := range dates {
				k := periodKey(period, d)
				if _, ok := first[k]; !ok {
					first[k] = d
				}
				last[k] = d
			}
			want := map[string][]string{}
			for k, d := range first {
				if d == dates[0] {
					continue // no previous period to rank on
				}
				want[d] = append(want[d], "1")
				exit := last[k]
				if exit == d {
					i := sort.SearchStrings(dates, d)
					if i+1 == len(dates) {
						continue
					}
					exit = dates[i+1]
				}
				want[exit] = append(want[exit], "-1")
			}
			for d := range want {
				sort.Strings(want[d]) // "-1" sorts before "1": exits first
			}
			for d := range got {
				if _, ok := want[d]; !ok {
					t.Errorf("%s: unexpected signal on %s: %v", period, d, got[d])
				}
			}
			for d, w := range want {
				if strings.Join(got[d], ",") != strings.Join(w, ",") {
					t.Errorf("%s: %s got %v want %v", period, d, got[d], w)
				}
			}
		})
	}
}

func TestPeriodValidate(t *testing.T) {
	for _, c := range []struct {
		name string
		row  refdb.RotationStrategy
		ok   bool
	}{
		{"year", periodRow("1y", "long"), true},
		{"bad period", periodRow("2y", "long"), false},
		{"bad side", periodRow("1y", "sideways"), false},
		{"short without period", func() refdb.RotationStrategy { r := row(); r.Side = "short"; return r }(), false},
		{"momentum row", row(), true},
	} {
		err := (&Strategy{Row: c.row}).Validate()
		if (err == nil) != c.ok {
			t.Errorf("%s: err %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

// A one-year period row reproduces the retired biggest-winner strategies: the
// expected winner each year is worked out here from the real first open and last
// close, independently of the SQL pipeline.
var annualSymbols = []string{"TQQQ", "SQQQ", "SOXL", "SOXS", "SPY", "SH", "QQQ", "PSQ", "NVDL", "TSLL", "GOOGL"}

var annualPairs = map[string]string{
	"TQQQ": "SQQQ", "SQQQ": "TQQQ", "SOXL": "SOXS", "SOXS": "SOXL",
	"SPY": "SH", "SH": "SPY", "QQQ": "PSQ", "PSQ": "QQQ",
}

type yearPick struct{ symbol, entryDate, exitDate string }

func expectedPicks(t *testing.T, market string) map[string]yearPick {
	t.Helper()
	db, err := storage.OpenSQLiteReadOnly(market)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var bars []struct {
		Symbol string  `db:"symbol"`
		Date   string  `db:"date"`
		Open   float64 `db:"open"`
		Close  float64 `db:"close"`
	}
	if err := db.Select(&bars, `SELECT symbol, substr(Date,1,10) AS date, open, close FROM backtest_start
		WHERE length(Date) = 10 AND substr(Date,1,10) >= '2021-01-01' ORDER BY symbol, Date`); err != nil {
		t.Fatal(err)
	}
	type span struct {
		firstDate, lastDate  string
		firstOpen, lastClose float64
	}
	years := map[string]map[string]*span{}
	for _, b := range bars {
		y := b.Date[:4]
		if years[y] == nil {
			years[y] = map[string]*span{}
		}
		sp := years[y][b.Symbol]
		if sp == nil {
			years[y][b.Symbol] = &span{b.Date, b.Date, b.Open, b.Close}
			continue
		}
		sp.lastDate, sp.lastClose = b.Date, b.Close
	}
	out := map[string]yearPick{}
	for y := 2022; y <= 2025; y++ {
		prev, cur := years[fmt.Sprint(y-1)], years[fmt.Sprint(y)]
		best, bestRet := "", -1e18
		syms := make([]string, 0, len(prev))
		for s := range prev {
			syms = append(syms, s)
		}
		sort.Strings(syms)
		for _, s := range syms {
			if _, ok := cur[s]; !ok || prev[s].firstOpen == 0 {
				continue
			}
			if r := (prev[s].lastClose - prev[s].firstOpen) / prev[s].firstOpen; r > bestRet {
				best, bestRet = s, r
			}
		}
		if best != "" {
			out[fmt.Sprint(y)] = yearPick{best, cur[best].firstDate, cur[best].lastDate}
		}
	}
	return out
}

func TestYearRowsLongShortAndInverseOnRealBars(t *testing.T) {
	market := realbars.Copy(t, annualSymbols...)
	picks := expectedPicks(t, market)
	if len(picks) < 3 {
		t.Skipf("only %d years had a winner (the hourly database holds too few years of these symbols): %v", len(picks), picks)
	}
	db, err := storage.OpenSQLite(market)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bars, _, err := storage.FetchBars(db, "backtest_start", annualSymbols, "2021-01-01", "")
	if err != nil {
		t.Fatal(err)
	}

	for _, side := range []string{"long", "short", "inverse"} {
		s := &Strategy{Row: periodRow("1y", side)}
		if err := s.Validate(); err != nil {
			t.Fatal(err)
		}
		s.SetDatabases(market, filepath.Join(t.TempDir(), "calc.db"))
		got := map[string][]string{}
		for _, sig := range s.GenerateSignals(bars) {
			got[sig.Date[:4]] = append(got[sig.Date[:4]], fmt.Sprintf("%s|%s|%d|%s", sig.Date, sig.Symbol, sig.Entry, sig.Direction))
		}
		dir := "LONG"
		if side == "short" {
			dir = "SHORT"
		}
		for y, p := range picks {
			trade := p.symbol
			if side == "inverse" {
				inv, ok := annualPairs[p.symbol]
				if !ok {
					if len(got[y]) != 0 {
						t.Errorf("%s %s: %s has no pair, want a cash year, got %v", side, y, p.symbol, got[y])
					}
					continue
				}
				trade = inv
				if len(got[y]) == 0 {
					continue // the pair did not trade on the entry date
				}
			}
			want := []string{fmt.Sprintf("%s|%s|1|%s", p.entryDate, trade, dir)}
			if p.exitDate != p.entryDate {
				want = append(want, fmt.Sprintf("%s|%s|-1|%s", p.exitDate, trade, dir))
			}
			if strings.Join(got[y], ",") != strings.Join(want, ",") {
				t.Errorf("%s %s: got %v want %v", side, y, got[y], want)
			}
		}
	}
}

var _ = models.Signal{}

// The matched-leverage pairs are data in the pipeline: every pair is listed both
// ways and single-name leveraged ETFs are not paired.
func TestInversePairsRoundTrip(t *testing.T) {
	text, err := fs.ReadFile(sqlfiles.FS, "strategies/rotation_period/02_inverse_pairs.sql")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlx.Open("sqlite", filepath.Join(t.TempDir(), "pairs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE rp_inverse (symbol TEXT PRIMARY KEY, inverse TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(text)); err != nil {
		t.Fatal(err)
	}
	var bad []string
	if err := db.Select(&bad, `SELECT a.symbol || '->' || a.inverse FROM rp_inverse a
		LEFT JOIN rp_inverse b ON b.symbol = a.inverse AND b.inverse = a.symbol WHERE b.symbol IS NULL`); err != nil || len(bad) != 0 {
		t.Fatalf("pairs that do not round trip: %v (err %v)", bad, err)
	}
	var n int
	if err := db.Get(&n, "SELECT COUNT(*) FROM rp_inverse WHERE symbol IN ('TSLL', 'NVDL')"); err != nil || n != 0 {
		t.Fatalf("single-name bull ETFs must not have a guessed inverse (%d, err %v)", n, err)
	}
}

// A loser row buys the previous period's worst name. The expected pick is worked
// out here from the literal bars, not by the SQL under test.
func TestLoserRowBuysThePreviousPeriodsWorstName(t *testing.T) {
	market, dates, bars := gappedMarketDB(t)
	row := periodRow("1m", "long")
	row.Pick = "loser"
	s := &Strategy{Row: row}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.SetDatabases(market, filepath.Join(t.TempDir(), "calc.db"))
	got := map[string]string{} // entry date -> symbol
	for _, sig := range s.GenerateSignals(bars) {
		if sig.Entry == 1 {
			got[sig.Date] = sig.Symbol
		}
	}

	type span struct{ open, close float64 }
	months := []string{}
	first := map[string]string{}
	ret := map[string]map[string]float64{} // month -> symbol -> return
	for _, sym := range testSymbols {
		spans := map[string]*span{}
		for _, b := range bars[sym] {
			k := b.Date[:7]
			if spans[k] == nil {
				spans[k] = &span{open: b.Open}
			}
			spans[k].close = b.Close
			if _, ok := first[k]; !ok {
				first[k] = b.Date
				months = append(months, k)
			}
		}
		for k, sp := range spans {
			if ret[k] == nil {
				ret[k] = map[string]float64{}
			}
			ret[k][sym] = (sp.close - sp.open) / sp.open
		}
	}
	sort.Strings(months)
	if len(months) < 6 || dates[0] != first[months[0]] {
		t.Fatalf("test bars should span several months, got %v", months)
	}
	for i := 1; i < len(months); i++ {
		worst, worstRet := "", 1e18
		for _, sym := range testSymbols {
			if r := ret[months[i-1]][sym]; r < worstRet || (r == worstRet && sym < worst) {
				worst, worstRet = sym, r
			}
		}
		if got[first[months[i]]] != worst {
			t.Errorf("%s: bought %q, want the worst of %s, %q", months[i], got[first[months[i]]], months[i-1], worst)
		}
	}
	if (&Strategy{Row: func() refdb.RotationStrategy { r := row; r.Pick = "best"; return r }()}).Validate() == nil {
		t.Error("pick \"best\" should not validate")
	}
}

// allRow is a pick = all row over the test symbols: every name each session, a
// 90% limit, 5% target, 10% stop, 3 session hold, 10% positions, once a month.
func allRow() refdb.RotationStrategy {
	return refdb.RotationStrategy{
		ID: "rot-all", Name: "all", Symbols: strings.Join(testSymbols, ","), TopK: len(testSymbols), UniverseSize: len(testSymbols),
		MaxWeightPct: 0.10, RegimeSymbol: "QQQ", AllocationPct: 0.10, SlippagePct: 0.0005,
		Period: "1d", Side: "long", Pick: "all",
		EntryLimitPct: 0.9, TakeProfitPct: 0.05, HoldDays: 3, StopLossPct: 0.10, Cooldown: "month",
	}
}

func TestPickAllConfigIsPositionSizedLimitRow(t *testing.T) {
	s := &Strategy{Row: allRow()}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg := s.DefaultConfig()
	if cfg.PositionCap != len(testSymbols) || cfg.AllocationPct != 0.10 || cfg.EntryLimitPct != 0.9 || !cfg.SameDayExit ||
		cfg.TakeProfitPct != 0.05 || cfg.HoldingWindow != 3 || !cfg.OneFillPerMonth {
		t.Fatalf("config %+v", cfg)
	}
	if got := cfg.StopLossPct; got < 0.8999 || got > 0.9001 {
		t.Fatalf("stop multiplier %v, want 0.9 for a 10%% stop", got)
	}
}

func TestPickAllValidate(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*refdb.RotationStrategy)
		ok   bool
	}{
		{"as seeded", func(*refdb.RotationStrategy) {}, true},
		{"no list", func(r *refdb.RotationStrategy) { r.Symbols = "" }, false},
		{"no period", func(r *refdb.RotationStrategy) { r.Period = "" }, false},
		{"bad cooldown", func(r *refdb.RotationStrategy) { r.Cooldown = "week" }, false},
		{"cooldown on a short row", func(r *refdb.RotationStrategy) { r.Side = "short" }, false},
		{"unknown pick", func(r *refdb.RotationStrategy) { r.Pick = "most" }, false},
	} {
		r := allRow()
		c.edit(&r)
		if err := (&Strategy{Row: r}).Validate(); (err == nil) != c.ok {
			t.Errorf("%s: err %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

// Every name gets an entry every session after the first, and a row with a hold
// window writes no period-end exit (the hold window, target and stop are the exits).
func TestPickAllEntersEveryNameEverySession(t *testing.T) {
	market, dates, bars := gappedMarketDB(t)
	signalsOf := func(r refdb.RotationStrategy) []models.Signal {
		s := &Strategy{Row: r}
		if err := s.Validate(); err != nil {
			t.Fatal(err)
		}
		s.SetDatabases(market, filepath.Join(t.TempDir(), "calc.db"))
		return s.GenerateSignals(bars)
	}
	run := func(r refdb.RotationStrategy) map[string]map[string]int {
		got := map[string]map[string]int{}
		for _, sig := range signalsOf(r) {
			if got[sig.Date] == nil {
				got[sig.Date] = map[string]int{}
			}
			got[sig.Date][sig.Symbol] = sig.Entry
		}
		return got
	}

	got := run(allRow())
	for i, d := range dates {
		if i == 0 {
			if len(got[d]) != 0 {
				t.Errorf("%s: no previous session to rank on, got %v", d, got[d])
			}
			continue
		}
		if len(got[d]) != len(testSymbols) {
			t.Errorf("%s: %d entries, want all %d names: %v", d, len(got[d]), len(testSymbols), got[d])
		}
		for sym, entry := range got[d] {
			if entry != 1 {
				t.Errorf("%s %s: entry %d, want only entries", d, sym, entry)
			}
		}
	}

	noHold := allRow()
	noHold.HoldDays = 0
	exits := 0
	for _, sig := range signalsOf(noHold) {
		if sig.Entry < 0 {
			exits++
		}
	}
	if exits == 0 {
		t.Error("a 1d row with no hold window still ends each period with a sale at the next session")
	}
}
