-- Every strategy row tied to a symbol, as signal or trade symbol. The
-- reference database is attached as r.
SELECT id, family, name, sym AS symbol FROM (
  SELECT id, 'streak' AS family, name, upper(signal_symbol) AS sym FROM r.streak_strategy
  UNION SELECT id, 'streak', name, upper(trade_symbol) FROM r.streak_strategy
  UNION SELECT id, 'tree', name, upper(signal_symbol) FROM r.tree_strategy
  UNION SELECT id, 'tree', name, upper(trade_symbol) FROM r.tree_strategy
  UNION SELECT id, 'markov', name, upper(signal_symbol) FROM r.markov_strategy
  UNION SELECT id, 'markov', name, upper(trade_symbol) FROM r.markov_strategy
  UNION SELECT id, 'hmm', name, upper(hmm_symbol) FROM r.hmm_strategy
  UNION SELECT id, 'hmm', name, upper(trade_symbol) FROM r.hmm_strategy
  UNION SELECT id, 'hold', name, upper(symbol) FROM r.hold_strategy
)
ORDER BY sym, family, id
