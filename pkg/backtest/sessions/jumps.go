package sessions

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	sqlfiles "github.com/darianmavgo/trade_well/sql"
)

// DefaultSplitJumpRatio is the close-to-close ratio beyond which a move between two sessions is
// treated as an unadjusted split rather than price action (and under its inverse). A leveraged
// ETF does not double in a day.
const DefaultSplitJumpRatio = 1.8

// Jump is one session whose close is out of line with the session before it.
type Jump struct {
	Symbol string  `db:"symbol"`
	Date   string  `db:"date"`
	Ratio  float64 `db:"ratio"`
}

func (j Jump) String() string { return fmt.Sprintf("%s %s x%.2f", j.Symbol, j.Date, j.Ratio) }

// SplitJumps lists the sessions of symbols between start and end (YYYY-MM-DD, inclusive) whose
// close moved by more than ratio from the session before. The query is the stage
// sql/stages/split_jumps/jumps.sql. A ratio of 0 uses DefaultSplitJumpRatio.
func SplitJumps(db *sqlx.DB, symbols []string, ratio float64, start, end string) ([]Jump, error) {
	list := strategy.SQLSymbolList(symbols)
	if list == "" {
		return nil, nil
	}
	if ratio <= 1 {
		ratio = DefaultSplitJumpRatio
	}
	text, err := fs.ReadFile(sqlfiles.Stages, "stages/split_jumps/jumps.sql")
	if err != nil {
		return nil, err
	}
	if start == "" {
		start = "0000-00-00"
	}
	if end == "" {
		end = "9999-99-99"
	}
	q := strings.NewReplacer("__SYMBOLS__", list, "__RATIO__", fmt.Sprintf("%g", ratio), "__START__", start, "__END__", end).Replace(string(text))
	var out []Jump
	if err := db.Select(&out, q); err != nil {
		return nil, fmt.Errorf("split jumps: %w", err)
	}
	return out, nil
}
