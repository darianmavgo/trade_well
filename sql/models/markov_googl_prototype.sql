-- Phase 1: Regime Definition
DROP VIEW IF EXISTS markov_googl_regimes;
CREATE VIEW markov_googl_regimes AS
SELECT 
    Date, 
    close,
    (close - LAG(close, 20) OVER (ORDER BY Date)) / LAG(close, 20) OVER (ORDER BY Date) as ret_20d,
    CASE 
        WHEN ((close - LAG(close, 20) OVER (ORDER BY Date)) / LAG(close, 20) OVER (ORDER BY Date)) >= 0.05 THEN 1
        WHEN ((close - LAG(close, 20) OVER (ORDER BY Date)) / LAG(close, 20) OVER (ORDER BY Date)) <= -0.05 THEN -1
        ELSE 0 
    END as regime
FROM backtest_start 
WHERE symbol = 'GOOGL' AND timeframe = '1d';

-- Phase 2: Transition Matrix
DROP TABLE IF EXISTS markov_googl_transition_matrix;
CREATE TABLE markov_googl_transition_matrix AS
WITH state_pairs AS (
    SELECT 
        regime as from_state,
        LEAD(regime) OVER (ORDER BY Date) as to_state
    FROM markov_googl_regimes
    WHERE ret_20d IS NOT NULL
)
SELECT 
    from_state,
    to_state,
    CAST(COUNT(*) AS FLOAT) / SUM(COUNT(*)) OVER (PARTITION BY from_state) as probability
FROM state_pairs
WHERE to_state IS NOT NULL
GROUP BY from_state, to_state;

-- Phase 3: Signal Generation Pipeline
DROP TABLE IF EXISTS markov_googl_signals;
CREATE TABLE markov_googl_signals AS
SELECT 
    r.Date,
    'GOOGL' as symbol,
    r.regime as current_state,
    COALESCE(MAX(CASE WHEN t.to_state = 1 THEN t.probability END), 0.0) as prob_bull,
    COALESCE(MAX(CASE WHEN t.to_state = -1 THEN t.probability END), 0.0) as prob_bear,
    COALESCE(MAX(CASE WHEN t.to_state = 1 THEN t.probability END), 0.0) - COALESCE(MAX(CASE WHEN t.to_state = -1 THEN t.probability END), 0.0) as signal
FROM markov_googl_regimes r
JOIN markov_googl_transition_matrix t ON r.regime = t.from_state
GROUP BY r.Date, r.regime
ORDER BY r.Date;
