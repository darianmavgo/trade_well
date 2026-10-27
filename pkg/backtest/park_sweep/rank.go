package park_sweep

import (
	"fmt"

	"github.com/jmoiron/sqlx"
)

// Rank prints done rows by kind, best edge versus holding the park symbol first.
func Rank(sweepPath string, ids []string) error {
	db, err := Open(sweepPath)
	if err != nil {
		return err
	}
	defer db.Close()
	cfg, err := loadConfig(db)
	if err != nil {
		return err
	}
	var asset struct {
		Final  float64 `db:"final_equity"`
		CAGR   float64 `db:"cagr"`
		DD     float64 `db:"max_drawdown_pct"`
		Sharpe float64 `db:"sharpe"`
		Bars   int     `db:"bar_count"`
	}
	if err := db.Get(&asset, `
		SELECT final_equity, cagr, max_drawdown_pct, sharpe, bar_count
		FROM park_asset WHERE symbol = ?`, cfg.ParkSymbol); err != nil {
		return fmt.Errorf("park_asset: %w", err)
	}
	fmt.Printf("%s buy-and-hold  equity %.2f  CAGR %.2f%%  maxDD %.2f%%  Sharpe %.3f  (%d sessions, $%.0f start)\n\n",
		cfg.ParkSymbol, asset.Final, asset.CAGR*100, asset.DD*100, asset.Sharpe, asset.Bars, cfg.Capital)

	type count struct {
		Kind   string `db:"kind"`
		Status string `db:"status"`
		N      int    `db:"n"`
	}
	var counts []count
	in, inArgs := inClause("r.strategy_id", ids)
	if err := db.Select(&counts, `
		SELECT s.kind, r.status, COUNT(*) AS n
		FROM strategy_run r
		JOIN sweep_strategy s ON s.strategy_id = r.strategy_id
		WHERE 1 = 1`+in+`
		GROUP BY s.kind, r.status
		ORDER BY s.kind, r.status`, inArgs...); err != nil {
		return err
	}
	fmt.Println("counts")
	for _, c := range counts {
		fmt.Printf("  %-14s %-8s %d\n", c.Kind, c.Status, c.N)
	}
	fmt.Println()

	for _, kind := range []string{"streak", "markov"} {
		if err := printKind(db, cfg, kind, ids); err != nil {
			return err
		}
	}
	return nil
}

func printKind(db *sqlx.DB, cfg Config, kind string, ids []string) error {
	in, inArgs := inClause("r.strategy_id", ids)
	var total int
	if err := db.Get(&total, `
		SELECT COUNT(*)
		FROM strategy_run r
		JOIN sweep_strategy s ON s.strategy_id = r.strategy_id
		WHERE r.status = 'done' AND s.kind = ?`+in, append([]interface{}{kind}, inArgs...)...); err != nil {
		return err
	}
	fmt.Printf("%s  (%d done)\n", kind, total)
	if total == 0 {
		fmt.Println()
		return nil
	}
	type row struct {
		ID     string  `db:"strategy_id"`
		Alloc  float64 `db:"allocation_pct"`
		Equity float64 `db:"final_equity"`
		CAGR   float64 `db:"cagr"`
		DD     float64 `db:"max_drawdown_pct"`
		Sharpe float64 `db:"sharpe"`
		Trades int     `db:"total_trades"`
		Win    float64 `db:"win_rate"`
		Sleeve float64 `db:"sleeve_net"`
		Park   float64 `db:"park_contribution"`
		Edge   float64 `db:"edge_vs_googl"`
	}
	var rows []row
	allocExpr := `s.allocation_pct`
	args := []interface{}{kind}
	if cfg.AllocationPct.Valid {
		allocExpr = `?`
		args = []interface{}{cfg.AllocationPct.Float64, kind}
	}
	q := fmt.Sprintf(`
		SELECT s.strategy_id, %s AS allocation_pct,
		       r.final_equity, r.cagr, r.max_drawdown_pct, r.sharpe,
		       r.total_trades, r.win_rate, r.sleeve_net, r.park_contribution, r.edge_vs_googl
		FROM strategy_run r
		JOIN sweep_strategy s ON s.strategy_id = r.strategy_id
		WHERE r.status = 'done' AND s.kind = ?`+in+`
		ORDER BY r.edge_vs_googl DESC
		LIMIT 25`, allocExpr)
	args = append(args, inArgs...)
	if err := db.Select(&rows, q, args...); err != nil {
		return err
	}
	fmt.Printf("  %-28s %6s %12s %8s %8s %7s %6s %7s %12s %12s %12s\n",
		"strategy", "alloc", "equity", "cagr", "maxDD", "sharpe", "trades", "win", "sleeve", "park", "edge")
	for _, r := range rows {
		fmt.Printf("  %-28s %5.0f%% %12.2f %7.2f%% %7.2f%% %7.3f %6d %6.1f%% %12.2f %12.2f %12.2f\n",
			r.ID, r.Alloc*100, r.Equity, r.CAGR*100, r.DD*100, r.Sharpe, r.Trades, r.Win*100, r.Sleeve, r.Park, r.Edge)
	}
	if total > len(rows) {
		fmt.Printf("  … %d more in strategy_run\n", total-len(rows))
	}
	fmt.Println()
	return nil
}
