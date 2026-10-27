SELECT symbol,
			       'exit date ' || CASE WHEN exit_date <> '' THEN exit_date || ' (' || exit_date_source || ')' ELSE 'unset' END
			       || ' has passed; held ' || trading_days_held || '/' || max_trading_days || ' trading days -- manage-exits should have closed this'
			FROM manage_exit
			WHERE account_number = ? AND status = 'OPEN'
			  AND ( (exit_date <> '' AND exit_date < date('now', '-5 hours'))
			     OR (exit_date = ''  AND trading_days_held >= max_trading_days) )
