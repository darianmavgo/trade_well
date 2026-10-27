#!/bin/bash
sed -i '' -e '/markov_strategy (/i\
CREATE TABLE IF NOT EXISTS tree_strategy (\
	id               TEXT PRIMARY KEY,\
	name             TEXT NOT NULL,\
	signal_symbol    TEXT NOT NULL,\
	trade_symbol     TEXT NOT NULL,\
	direction        TEXT NOT NULL,\
	hold_days        INTEGER NOT NULL,\
	take_profit_pct  REAL NOT NULL,\
	stop_loss_pct    REAL NOT NULL,\
	allocation_pct   REAL NOT NULL,\
	cash_yield       REAL NOT NULL,\
	slippage_pct     REAL NOT NULL,\
	next_day_limit   INTEGER NOT NULL,\
	coil_range_max   REAL NOT NULL,\
	sma_bounce_min   REAL NOT NULL,\
	sma_bounce_max   REAL NOT NULL,\
	source_strategy  TEXT,\
	source_label     TEXT,\
	win_rate         REAL,\
	total_trades     INTEGER\
);\
' pkg/refdb/refdb.go
