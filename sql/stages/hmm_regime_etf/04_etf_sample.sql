-- Each ETF return with the prior-close regime, once for the whole window (sample all) and once for
-- the sessions the model learned from (in) or only scored with frozen parameters (oos).
DROP TABLE IF EXISTS sample_name;

CREATE TABLE sample_name (sample TEXT);

INSERT INTO sample_name (sample) VALUES ('all'), ('in'), ('oos');

DROP TABLE IF EXISTS etf_sample;

CREATE TABLE etf_sample AS
SELECT
    e.symbol AS symbol,
    s.sample AS sample,
    e.ret AS ret,
    g.r_prior AS r_prior,
    CASE WHEN g.r_prior = k.rank_stress THEN 1 ELSE 0 END AS is_stress
FROM etf_ret e
JOIN regime_prior g ON g.date = e.date
CROSS JOIN stress_rank k
CROSS JOIN sample_name s
WHERE g.r_prior IS NOT NULL
  AND (s.sample = 'all' OR (s.sample = 'in' AND g.in_sample = 1) OR (s.sample = 'oos' AND g.in_sample = 0));

CREATE INDEX IF NOT EXISTS idx_etf_sample ON etf_sample(symbol, sample);
