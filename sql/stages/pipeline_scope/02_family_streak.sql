-- Every streak strategy row, with the symbols it reads and trades.
-- __LIMIT__ is a row count; -1 means every row.
SELECT id, upper(signal_symbol) AS signal_symbol, upper(trade_symbol) AS trade_symbol
FROM streak_strategy
ORDER BY id
LIMIT __LIMIT__
