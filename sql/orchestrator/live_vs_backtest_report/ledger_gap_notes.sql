SELECT account_number, symbol, exit_fill_price FROM live_vs_backtest_trade WHERE exit_fill_price = 0 AND live_outcome IN ('OPEN', 'CLOSED')
