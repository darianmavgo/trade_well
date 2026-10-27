create view if not exists v_symbol_stats as 
select distinct symbol, count(*),  min(date), max (date) from backtest_start group by symbol;