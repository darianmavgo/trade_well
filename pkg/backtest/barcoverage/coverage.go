// Package barcoverage judges how complete the stored bar history is for a set
// of symbols. `strategy coverage` reports it and `market_history update` uses
// it to decide which symbols to download again.
package barcoverage

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/olekukonko/tablewriter"

	"github.com/darianmavgo/trade_well/pkg/backtest/sessions"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// calendarSymbol is added to the session calendar when it is in the database.
const calendarSymbol = "SPY"

// CoverageOptions selects what coverage reports on.
type CoverageOptions struct {
	Symbols []string // relevant symbols
	DB      string   // the hourly market database (hourly bars and the sessions derived from them)
	Start   string   // first session, YYYY-MM-DD
	End     string   // last session; "" = latest bar
}

// SymbolCoverage is one symbol's completeness over its own listed window.
type SymbolCoverage struct {
	Symbol      string
	First, Last string  // first and last session with hourly bars
	Expected    int     // sessions the symbol should have bars for
	Have        int     // of those, sessions with hourly bars
	Missing     int     // Expected - Have
	LongestGap  int     // longest run of consecutive missing sessions
	BarsPerDay  float64 // average regular-session hourly bars on a session that has any (a full session is 7)
	Bars        int
	Note        string
}

// Pct is the share of expected sessions that have bars.
func (c SymbolCoverage) Pct() float64 {
	if c.Expected == 0 {
		return 0
	}
	return 100 * float64(c.Have) / float64(c.Expected)
}

// Coverage judges, for each symbol, how many sessions in the window have hourly
// bars. A session is a day any of the symbols (or SPY) traded, so a day the
// whole set is missing cannot be seen, and a symbol is only expected from its
// first to its last session, so a later listing is not counted as missing
// history. A symbol that did not trade in any hour of a day looks the same as a
// missing download; the thin names are where that happens.
func Coverage(opts CoverageOptions) ([]SymbolCoverage, error) {
	start := opts.Start
	if start == "" {
		start = "2021-01-01"
	}
	db, err := storage.OpenSQLite(opts.DB)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", opts.DB, err)
	}
	defer db.Close()

	sessionsOf := func(symbol string) ([]string, error) {
		q := `SELECT DISTINCT substr(Date, 1, 10) FROM backtest_start
			WHERE symbol = ? AND timeframe = '1d' AND length(Date) = 10 AND substr(Date, 1, 10) >= ?`
		args := []any{symbol, start}
		if opts.End != "" {
			q += ` AND substr(Date, 1, 10) <= ?`
			args = append(args, opts.End)
		}
		var days []string
		err := db.Select(&days, q+` ORDER BY 1`, args...)
		return days, err
	}

	own := map[string][]string{}
	calSet := map[string]bool{}
	for _, sym := range append(append([]string(nil), opts.Symbols...), calendarSymbol) {
		days, err := sessionsOf(sym)
		if err != nil {
			return nil, err
		}
		if sym != calendarSymbol {
			own[sym] = days
		}
		for _, d := range days {
			calSet[d] = true
		}
	}
	calendar := make([]string, 0, len(calSet))
	for d := range calSet {
		calendar = append(calendar, d)
	}
	sort.Strings(calendar)

	out := make([]SymbolCoverage, 0, len(opts.Symbols))
	for _, sym := range opts.Symbols {
		c := SymbolCoverage{Symbol: sym}
		days := own[sym]
		if len(days) == 0 {
			c.Note = "no sessions in window"
			out = append(out, c)
			continue
		}
		listed, last := days[0], days[len(days)-1]
		have := map[string]bool{}
		for _, d := range days {
			have[d] = true
		}
		var hours []string
		if err := db.Select(&hours, `SELECT CAST(Date AS TEXT) FROM backtest_start
			WHERE symbol = ? AND timeframe = '1h' AND length(Date) = 19 AND Date >= ? AND Date < ?`,
			sym, listed+" 00:00:00", nextDay(last)+" 12:00:00"); err != nil {
			return nil, err
		}
		for _, h := range hours {
			if d, ok := sessions.Regular(h); ok && d >= listed && d <= last {
				c.Bars++
			}
		}
		run := 0
		for _, d := range calendar {
			if d < listed || d > last {
				continue
			}
			c.Expected++
			if have[d] {
				c.Have++
				run = 0
				if c.First == "" {
					c.First = d
				}
				c.Last = d
			} else {
				c.Missing++
				if run++; run > c.LongestGap {
					c.LongestGap = run
				}
			}
		}
		if c.Have > 0 {
			c.BarsPerDay = float64(c.Bars) / float64(c.Have)
		}
		if c.Have == 0 {
			c.Note = "no bars"
		}
		out = append(out, c)
	}
	return out, nil
}

func nextDay(d string) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return t.AddDate(0, 0, 1).Format("2006-01-02")
}

// WriteCoverage prints the report table.
func WriteCoverage(w io.Writer, opts CoverageOptions, rows []SymbolCoverage) {
	fmt.Fprintf(w, "\nhourly bar completeness from %s (%d symbols, bars in %s)\n", opts.Start, len(rows), opts.DB)
	fmt.Fprintf(w, "Expected = sessions any of the symbols traded between this symbol's first and last session.\n")
	table := tablewriter.NewWriter(w)
	header := []string{"SYMBOL", "FIRST", "LAST", "EXPECTED", "HAVE", "MISSING", "LONGEST GAP", "COVERAGE", "BARS/DAY", "NOTE"}
	table.SetHeader(header)
	table.SetBorder(true)
	var expected, have int
	for _, c := range rows {
		expected += c.Expected
		have += c.Have
		row := []string{c.Symbol, c.First, c.Last, fmt.Sprint(c.Expected), fmt.Sprint(c.Have), fmt.Sprint(c.Missing),
			fmt.Sprint(c.LongestGap), fmt.Sprintf("%.1f%%", c.Pct()), fmt.Sprintf("%.1f", c.BarsPerDay)}
		table.Append(append(row, c.Note))
	}
	table.Render()
	if expected > 0 {
		fmt.Fprintf(w, "All symbols: %d of %d sessions covered (%.1f%%)\n", have, expected, 100*float64(have)/float64(expected))
	}
}
