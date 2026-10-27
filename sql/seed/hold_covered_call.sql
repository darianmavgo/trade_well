-- Dividend ETFs held with a monthly covered call, 10% and 5% out of the money. They replace the
-- dividend-covered-call strategies that were written in Go and keep their ids. Each needs option
-- history: market_history -source polygon-options -symbols <SYM> -otm <10|5>.
-- Run: sqlite3 refdata/strategies.db < sql/seed/hold_covered_call.sql
INSERT OR REPLACE INTO hold_strategy
    (id, name, symbol, total_return, allocation_pct, cash_yield, slippage_pct, covered_call_otm_pct)
VALUES
    ('schd-covered-call',      'SCHD Covered Call 10% OTM (Schwab U.S. Dividend Equity ETF, dividends withdrawn)', 'SCHD', 0, 1.0, 0, 0.0005, 10),
    ('schd-covered-call-5pct', 'SCHD Covered Call 5% OTM (Schwab U.S. Dividend Equity ETF, dividends withdrawn)',  'SCHD', 0, 1.0, 0, 0.0005, 5),
    ('vym-covered-call',       'VYM Covered Call 10% OTM (Vanguard High Dividend Yield ETF, dividends withdrawn)', 'VYM',  0, 1.0, 0, 0.0005, 10),
    ('vym-covered-call-5pct',  'VYM Covered Call 5% OTM (Vanguard High Dividend Yield ETF, dividends withdrawn)',  'VYM',  0, 1.0, 0, 0.0005, 5),
    ('dvy-covered-call',       'DVY Covered Call 10% OTM (iShares Select Dividend ETF, dividends withdrawn)',      'DVY',  0, 1.0, 0, 0.0005, 10),
    ('dvy-covered-call-5pct',  'DVY Covered Call 5% OTM (iShares Select Dividend ETF, dividends withdrawn)',       'DVY',  0, 1.0, 0, 0.0005, 5);
