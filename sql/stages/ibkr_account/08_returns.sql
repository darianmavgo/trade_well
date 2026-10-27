-- Time-weighted daily return. A deposit or withdrawal is taken as arriving at the close, so the
-- day's return is the change in equity not explained by the flow, over the previous close.
CREATE TABLE ibkr_return_step AS
SELECT date, equity, flow,
       LAG(equity) OVER (ORDER BY date) AS prev_equity,
       CASE WHEN LAG(equity) OVER (ORDER BY date) > 0
            THEN (equity - flow) / LAG(equity) OVER (ORDER BY date) - 1
       END AS daily_return
FROM ibkr_equity_daily;

-- The index is 1 on the first day the account holds money and then the running product of the returns.
CREATE TABLE ibkr_return_daily AS
SELECT date, equity, flow, prev_equity, daily_return,
       CASE WHEN prev_equity > 0 THEN exp(SUM(ln(1 + daily_return)) OVER (ORDER BY date)) END AS idx
FROM ibkr_return_step
