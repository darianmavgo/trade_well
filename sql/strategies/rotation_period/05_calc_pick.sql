-- For each period, the previous period's best __PICK_LIMIT__ returns (worst when __PICK_ORDER__
-- is ASC) among symbols that also trade in the period. Ties go to the lower symbol. A row
-- that picks every name has a limit above the list size.
INSERT INTO rp_pick (period, symbol, ret)
SELECT period, symbol, ret
FROM (
    SELECT
        ns.period AS period,
        p.symbol,
        p.ret,
        ROW_NUMBER() OVER (PARTITION BY ns.period ORDER BY p.ret __PICK_ORDER__, p.symbol) AS rk
    FROM rp_period p
    JOIN rp_seq ps ON ps.period = p.period
    JOIN rp_seq ns ON ns.seq = ps.seq + 1
    JOIN rp_period c ON c.symbol = p.symbol AND c.period = ns.period
    WHERE p.ret IS NOT NULL
)
WHERE rk <= __PICK_LIMIT__;
