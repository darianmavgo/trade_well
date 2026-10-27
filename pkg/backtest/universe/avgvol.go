package universe

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// AvgVolConfig controls the avgvol command.
type AvgVolConfig struct {
	DBPath   string // universe database that receives the avg_volume table
	MarketDB string // market database holding the daily bars (table backtest_start)
	Windows  []int  // trailing daily bars to average, for example 20 and 60
	Out      io.Writer
}

// ParseWindows reads a comma-separated list such as "20,60".
func ParseWindows(arg string) ([]int, error) {
	var out []int
	seen := map[int]bool{}
	for _, p := range strings.Split(arg, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("avgvol: %q is not a number of days", p)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("avgvol: no window given")
	}
	return out, nil
}

// AvgVol calculates the average daily share volume (ADTV) of every symbol in the
// market database over each trailing window and stores it in the avg_volume table
// of the universe database. The calculation is SQL (sql/stages/avgvol): a slice
// table of each symbol's last N bars, then the average. It returns the number of
// rows now stored for the last window.
func AvgVol(cfg AvgVolConfig) (int, error) {
	out := cfg.Out
	if out == nil {
		out = io.Discard
	}
	if len(cfg.Windows) == 0 {
		return 0, fmt.Errorf("avgvol: no window given")
	}
	db, err := Open(cfg.DBPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	// One connection, so the ATTACH is seen by every statement that follows.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`ATTACH DATABASE ? AS mkt`, cfg.MarketDB); err != nil {
		return 0, fmt.Errorf("avgvol: attach %s: %w", cfg.MarketDB, err)
	}
	rows := 0
	for _, days := range cfg.Windows {
		if err := storage.RunStage(db, "avgvol", map[string]string{"__DAYS__": strconv.Itoa(days)}); err != nil {
			return 0, err
		}
		if err := db.Get(&rows, `SELECT COUNT(*) FROM avg_volume WHERE window_days = ?`, days); err != nil {
			return 0, err
		}
		fmt.Fprintf(out, "avg_volume: %d symbols, %d-day window\n", rows, days)
	}
	return rows, nil
}
