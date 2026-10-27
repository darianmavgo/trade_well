-- Is a name held after this session's close? A name is bought when it ranks in
-- the top __TOP_K__ with the gate on, and sold when it ranks worse than
-- __EXIT_RANK__ or the gate goes off. Between those it keeps its last state.
-- Each name's state depends only on its own ranks, so it is the last decision
-- (1 buy, 0 sell) seen so far in that name's sessions.
--
-- A sell is judged from the rank on the session itself. A name that leaves the
-- universe has no row, so it is not sold by this table (it dropped out of the
-- liquid set, the simulator closes it at the end of the window).
INSERT INTO rot_state (symbol, date, held, prev_held)
WITH decided AS (
    SELECT r.symbol, r.date,
           ROW_NUMBER() OVER (PARTITION BY r.symbol ORDER BY r.date) AS seq,
           CASE WHEN g.risk_on = 1 AND r.rank <= __TOP_K__ THEN 1
                WHEN g.risk_on = 0 OR r.rank > __EXIT_RANK__ THEN 0 END AS decision
    FROM rot_rank r
    JOIN rot_regime g ON g.date = r.date
),
latched AS (
    SELECT symbol, date, seq,
           MAX(CASE WHEN decision IS NOT NULL THEN seq * 10 + decision END)
               OVER (PARTITION BY symbol ORDER BY seq) AS last_decision
    FROM decided
)
SELECT symbol, date,
       coalesce(last_decision % 10, 0),
       coalesce(LAG(last_decision % 10) OVER (PARTITION BY symbol ORDER BY seq), 0)
FROM latched;

CREATE INDEX rot_state_sd ON rot_state (symbol, date);
