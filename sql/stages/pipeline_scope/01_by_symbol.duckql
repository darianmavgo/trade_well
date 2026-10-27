-- Every strategy row tied to one of the symbols, by signal symbol or trade symbol.
-- __SYMBOL_LIST__ is the quoted, upper-case, comma-separated symbol list.
SELECT id FROM streak_strategy WHERE upper(signal_symbol) IN (__SYMBOL_LIST__) OR upper(trade_symbol) IN (__SYMBOL_LIST__)
UNION
SELECT id FROM tree_strategy WHERE upper(signal_symbol) IN (__SYMBOL_LIST__) OR upper(trade_symbol) IN (__SYMBOL_LIST__)
UNION
SELECT id FROM markov_strategy WHERE upper(signal_symbol) IN (__SYMBOL_LIST__) OR upper(trade_symbol) IN (__SYMBOL_LIST__)
UNION
SELECT id FROM hmm_strategy WHERE upper(hmm_symbol) IN (__SYMBOL_LIST__) OR upper(trade_symbol) IN (__SYMBOL_LIST__)
UNION
SELECT id FROM hold_strategy WHERE upper(symbol) IN (__SYMBOL_LIST__)
ORDER BY 1
