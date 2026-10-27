-- Stage 3: candidates = walk-forward survivors whose trade symbol is liquid.
-- Needs: wf.search_01_gate, mkt.search_03_liquidity, ref.streak_strategy.
DROP TABLE IF EXISTS search_04_candidates;
CREATE TABLE search_04_candidates AS
SELECT g.strategy_id, s.trade_symbol, g.n_folds, g.sum_oos_trades,
       g.mean_oos_sharpe, g.mean_is_sharpe, g.worst_fold_dd,
       l.avg_dollar_vol, l.min_close
FROM wf.search_01_gate g
JOIN ref.streak_strategy s ON s.id = g.strategy_id
JOIN mkt.search_03_liquidity l ON l.symbol = s.trade_symbol;
