-- Close-to-close moves between consecutive sessions beyond 1.8x (sessions.DefaultSplitJumpRatio) or under its
-- inverse. In this data that is almost always an unadjusted split. Filter by date or symbol when querying.
DROP VIEW IF EXISTS v_session_ratio;
CREATE VIEW v_session_ratio AS
SELECT symbol, date, close / lag(close) OVER (PARTITION BY symbol ORDER BY date) AS ratio
FROM v_daily_bars
WHERE close > 0;

DROP VIEW IF EXISTS v_split_jumps;
CREATE VIEW v_split_jumps AS
SELECT symbol, date, ratio
FROM v_session_ratio
WHERE ratio > 1.8 OR ratio < 1.0 / 1.8;
