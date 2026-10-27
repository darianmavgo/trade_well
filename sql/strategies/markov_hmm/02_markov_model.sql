ATTACH DATABASE '__HMM_DB__' AS hmm;

-- Phase 1: Regime Definition (HMM)
CREATE TEMP VIEW IF NOT EXISTS markov_model_regimes AS
SELECT 
    b.symbol,
    b.Date, 
    b.close,
    h.return as ret_20d,
    CASE 
        WHEN h.predicted_state = 2 THEN 1
        WHEN h.predicted_state = 0 THEN -1
        ELSE 0 
    END as regime
FROM market.backtest_start b
JOIN hmm.hmm_regime_history h 
    ON b.symbol = h.symbol 
    AND b.Date = substr(h.date, 1, 10)
WHERE b.timeframe = '1d';

-- Phase 2: Expanding Window Transition Matrix (Walk-Forward)
CREATE TEMP VIEW IF NOT EXISTS markov_model_transitions AS
SELECT 
    symbol,
    Date,
    regime as from_state,
    LEAD(regime) OVER (PARTITION BY symbol ORDER BY Date) as to_state
FROM markov_model_regimes
WHERE ret_20d IS NOT NULL;

CREATE TEMP VIEW IF NOT EXISTS markov_model_cumulative_matrix AS
SELECT 
    symbol,
    Date,
    from_state,
    -- Cumulative sum of transitions EXCLUDING the current row (which transitions into the future)
    -- This ensures we only use transitions that are fully known as of today.
    SUM(CASE WHEN to_state = 1 THEN 1 ELSE 0 END) OVER (PARTITION BY symbol, from_state ORDER BY Date ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) as cum_to_bull,
    SUM(CASE WHEN to_state = -1 THEN 1 ELSE 0 END) OVER (PARTITION BY symbol, from_state ORDER BY Date ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) as cum_to_bear,
    SUM(CASE WHEN to_state = 0 THEN 1 ELSE 0 END) OVER (PARTITION BY symbol, from_state ORDER BY Date ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) as cum_to_sideways,
    COUNT(to_state) OVER (PARTITION BY symbol, from_state ORDER BY Date ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) as total_transitions
FROM markov_model_transitions;

-- Phase 3: Signal Generation Pipeline
CREATE TABLE IF NOT EXISTS markov_model_predictions AS
SELECT 
    symbol,
    Date,
    from_state as current_state,
    CASE WHEN total_transitions > 0 THEN CAST(cum_to_bull AS FLOAT) / total_transitions ELSE 0.0 END as prob_bull,
    CASE WHEN total_transitions > 0 THEN CAST(cum_to_bear AS FLOAT) / total_transitions ELSE 0.0 END as prob_bear,
    CASE WHEN total_transitions > 0 THEN 
        (CAST(cum_to_bull AS FLOAT) / total_transitions) - (CAST(cum_to_bear AS FLOAT) / total_transitions) 
    ELSE 0.0 END as signal
FROM markov_model_cumulative_matrix
ORDER BY Date;
