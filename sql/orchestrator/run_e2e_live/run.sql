SELECT account_number, strategy_name, symbol, quantity, entry_price, status, IFNULL(reject_reason,'') FROM stage_order ORDER BY id DESC LIMIT 6
