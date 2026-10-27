package runner

import (
	"fmt"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
)

// MarketSource is one market_history database a run reads.
type MarketSource struct {
	Label  string
	Path   string // absolute where it can be resolved
	Detail string // size and last update, or why it is not there
	Use    string // what the run uses it for
}

// MarketSources names the database a run reads bars from. There is one: the
// hourly database, whose session rows (derived from the hourly bars, see
// pkg/sessions) feed signals and the simulation clock, and whose hourly rows
// decide limit fills and exits.
func MarketSources(marketDB string) []MarketSource {
	return MarketSourcesFor(marketDB, true)
}

// MarketSourcesFor is MarketSources for a command that reads the hourly bars
// only when hourly is true; otherwise the use says they are not read.
func MarketSourcesFor(marketDB string, hourly bool) []MarketSource {
	path := appenv.BarDB(marketDB, "1h")
	s := MarketSource{Label: "Hourly bars", Path: path,
		Use: "session bars derived from the hourly bars feed signals; hourly bars decide limit fills and exits"}
	if !hourly {
		s.Use = "session bars derived from the hourly bars feed signals; hourly bars are not read for fills here"
	}
	if abs, err := filepath.Abs(path); err == nil {
		s.Path = abs
	}
	if fi, err := os.Stat(path); err != nil {
		s.Detail = "not found"
	} else {
		s.Detail = fmt.Sprintf("%.0f MB, updated %s", float64(fi.Size())/1e6, fi.ModTime().Format("2006-01-02 15:04"))
	}
	return []MarketSource{s}
}

// PrintMarketSources writes the database a run reads.
func PrintMarketSources(w io.Writer, marketDB, table string) {
	PrintMarketSourcesFor(w, marketDB, table, true)
}

// PrintMarketSourcesFor is PrintMarketSources for a command that may not read the hourly bars for fills.
func PrintMarketSourcesFor(w io.Writer, marketDB, table string, hourly bool) {
	fmt.Fprintf(w, "Market data (table %s):\n", table)
	for _, s := range MarketSourcesFor(marketDB, hourly) {
		fmt.Fprintf(w, "  %-11s %s (%s; %s)\n", s.Label, s.Path, s.Detail, s.Use)
	}
}

// LoadFillBars loads the hourly bars that decide limit fills for symbols, from
// the hourly market database.
// Symbols with no hourly bars at all are named in the warning: their fills
// fall back to the daily bar.
func LoadFillBars(marketDB string, symbols []string) (map[string][]models.Bar, error) {
	in, err := LoadIntraday(marketDB, symbols)
	if err != nil {
		return nil, err
	}
	var none []string
	for _, sym := range symbols {
		if len(in[sym]) == 0 {
			none = append(none, sym)
		}
	}
	if len(none) > 0 {
		sort.Strings(none)
		log.Printf("Warning: no hourly bars in %s for %s; their limit fills use the derived session bar (market_history update -strategy <id>)", appenv.BarDB(marketDB, "1h"), strings.Join(none, ","))
	}
	return in, nil
}
