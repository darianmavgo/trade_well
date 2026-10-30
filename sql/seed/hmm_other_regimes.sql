-- Adds normal_filter, stress_filter and calm_down rows next to the calm_filter rows for the symbols with the most history.
-- Run: sqlite3 refdata/strategies.db "ATTACH 'data/reports/<N>/hmm.db' AS calm;" ".read sql/seed/hmm_other_regimes.sql"
-- calm is the result database of a backtest of the calm_filter rows, and the 1000 symbols with the most trades there are used.
-- Same exits as the calm_filter rows (hold 3, no take profit, 8% stop), a uniform start for gridsearch to tune.
CREATE TEMP TABLE regime_rule (rule TEXT, tag TEXT, label TEXT);
INSERT INTO regime_rule VALUES ('normal_filter', 'normal', 'Normal');
INSERT INTO regime_rule VALUES ('stress_filter', 'stressed', 'Stressed');
INSERT INTO regime_rule VALUES ('calm_down', 'calmdown', 'After Stress Eases');

CREATE TEMP TABLE regime_symbol AS
SELECT h.trade_symbol, h.allocation_pct, h.cash_yield, h.slippage_pct, h.id AS calm_id
FROM hmm_strategy h
JOIN calm.performance_summary p ON p.strategy_id = h.id
WHERE h.rule = 'calm_filter'
ORDER BY p.total_trades DESC, h.trade_symbol
LIMIT 1000;

INSERT OR IGNORE INTO hmm_strategy (id, name, hmm_symbol, trade_symbol, rule, stress_prob, hold_days, take_profit_pct, stop_loss_pct, allocation_pct, cash_yield, slippage_pct, source_strategy, source_label)
SELECT 'hmm-' || r.tag || '-' || lower(s.trade_symbol),
       s.trade_symbol || ' Long ' || r.label || ' (HMM)',
       'QQQ', s.trade_symbol, r.rule, 0.5, 3, 0.0, 0.08, s.allocation_pct, s.cash_yield, s.slippage_pct,
       s.calm_id, 'hmm regimes'
FROM regime_symbol s CROSS JOIN regime_rule r;

DROP TABLE regime_symbol;
DROP TABLE regime_rule;
