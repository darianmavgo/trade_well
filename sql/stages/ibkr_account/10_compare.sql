-- The claimed table beside the measured one, timeframe by timeframe.
CREATE TABLE ibkr_claim_vs_actual AS
SELECT c.timeframe,
       c.total_return AS claimed_total, a.total_return AS actual_total,
       c.car AS claimed_car, a.car AS actual_car,
       c.max_drawdown AS claimed_mdd, a.max_drawdown AS actual_mdd,
       c.calmar AS claimed_calmar, a.calmar AS actual_calmar,
       a.start_date AS actual_start, a.end_date AS actual_end,
       c.avg_trades_per_year AS claimed_trades_per_year,
       a.fills * 365.0 / a.days AS actual_fills_per_year
FROM claimed_performance c
LEFT JOIN ibkr_actual_performance a ON a.timeframe = c.timeframe
