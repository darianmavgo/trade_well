package train

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// ExtendConfig says which saved Markov models to bring up to date, and how far.
type ExtendConfig struct {
	MarketDB, ModelDB string
	Symbols           []string
	// Through is the last completed session (YYYY-MM-DD); later bars are ignored because the
	// session may still be open. "" = no limit.
	Through string
	// CalcDir keeps the slice tables for inspection; "" uses a scratch directory.
	CalcDir string
}

// ExtendMarkov appends predictions for the sessions after each saved model's last_date. It does
// not refit: the counts stay frozen at the model's trained_through, and each new session is scored
// from its own state (the 20-bar return needs only the last 21 closes). It returns the symbols
// that gained rows. A symbol with no saved model is not touched; train it first.
func ExtendMarkov(ctx context.Context, cfg ExtendConfig) ([]string, error) {
	if !isoDateOrEmpty(cfg.Through) {
		return nil, fmt.Errorf("extend markov: %q is not a YYYY-MM-DD date", cfg.Through)
	}
	symbols := cleanSymbols(cfg.Symbols)
	list := strategy.SQLSymbolList(symbols)
	if list == "" {
		return nil, fmt.Errorf("extend markov: no symbols")
	}
	if fi, err := os.Stat(cfg.ModelDB); err != nil || fi.Size() == 0 {
		return nil, fmt.Errorf("extend markov: model database %s not found or empty", cfg.ModelDB)
	}
	calcDir := cfg.CalcDir
	if calcDir == "" {
		tmp, err := os.MkdirTemp("", "extend-markov-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		calcDir = tmp
	}
	calc, err := strategy.OpenCalcDB(cfg.MarketDB, filepath.Join(calcDir, "markov_extend.db"))
	if err != nil {
		return nil, err
	}
	defer calc.Close()
	if _, err := calc.ExecContext(ctx, fmt.Sprintf("ATTACH DATABASE '%s' AS model", strings.ReplaceAll(cfg.ModelDB, "'", "''"))); err != nil {
		return nil, fmt.Errorf("extend markov: attach model database: %w", err)
	}
	if err := storage.RunStage(calc, "markov_extend", map[string]string{"__SYMBOL_LIST__": list, "__THROUGH__": cfg.Through}); err != nil {
		return nil, err
	}
	var extended []string
	if err := calc.SelectContext(ctx, &extended, "SELECT symbol FROM markov_x_meta ORDER BY symbol"); err != nil {
		return nil, err
	}
	return extended, nil
}

func isoDateOrEmpty(d string) bool { return d == "" || isoDate.MatchString(d) }
