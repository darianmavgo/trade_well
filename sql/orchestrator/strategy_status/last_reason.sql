SELECT reason FROM scan_market_trace WHERE strategy_id = ? AND is_asof = 1
		ORDER BY id DESC LIMIT 1
