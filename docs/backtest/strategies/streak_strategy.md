# Streak strategies

A streak strategy is one row in `streak_strategy`. You do not write Go for it. Backtest, gridsearch, and scoreboard load every valid row on startup and treat it like any other strategy.

Run the commands below from the repository root, after `make build`, so `.env` is found.

## Where the files are

`APP_FOLDER` (from the environment or `.env`, default `.`) is the repository root. Every command below reads and writes under it. Data, reference data and reports sit in fixed subfolders: `data/`, `refdata/`, `data/reports/`.

| What | Path |
|---|---|
| The strategy table | `$APP_FOLDER/refdata/strategies.db`, table `streak_strategy` |
| Daily bars | `$APP_FOLDER/data/market_history.db`, table `backtest_start` |
| One strategy's result | `$APP_FOLDER/data/reports/<id>.db`, then `<id>_2.db`, `<id>_3.db`, … |
| A sweep | `$APP_FOLDER/data/reports/gridsearch.db` |
| The ranking | `$APP_FOLDER/data/reports/scoreboard.db` |

There is one copy of each folder under `APP_FOLDER`. Point `APP_FOLDER` at the checkout you mean to use.

## What one row does

On each session the row watches `signal_symbol`.

- `direction = 'drop'` fires after `signal_days` strict down closes.
- `direction = 'rally'` fires after `signal_days` strict up closes.

It then buys `trade_symbol` long. The limit is that session's close of the bought symbol. Hold is `hold_days` sessions. Take-profit and stop are fractional offsets on that entry: `0.03` is 3 percent, `0.08` is 8 percent. A `0` take-profit means no target. A `0` stop means no stop. The engine turns a stop offset of `0.08` into the simulator's `0.92` multiplier. Position size is `allocation_pct` of equity, and only one position is open.

`regime` filters the watch bar:

| Value | Meaning |
|---|---|
| `All Regimes` | No filter |
| `<signal>>=SMA200` | Watch close is at or above its 200-session average. Example: `VOO>=SMA200` |
| `<signal><SMA200` | Watch close is below that average. Example: `GLD<SMA200` |

If the average is still 0, the regime filter does nothing. `SMA50` labels are rejected.

`next_day_limit` is `0` or `1`.

- `0` fills on the signal session. The limit is that session's close, so the low has already traded through it.
- `1` fills on the next session only when that session's low is at or below the limit. The fill is the limit, or the open when the open is already through the limit.

`slippage_pct` is a fraction of the fill (`0.001` is 0.1 percent). Promote writes `0` for both slippage and next-day limit, so the row repeats the sweep.

The id is `streak-<signal>-<up|down><days>-<trade>`, lowercase. `VOO`, rally, 5 days, buy `TQQQ` is `streak-voo-up5-tqqq`. The display name is `VOO up5 → TQQQ`. Hold, take-profit, stop, and regime are columns on that row. They are not part of the id, so two different exits for the same streak share one id. Promote keeps the higher win rate.

## 1. Put both symbols in the market database

The watch symbol and the bought symbol both need daily bars (`length(Date) = 10`).

```bash
./bin/market_history -symbols VOO,TQQQ -years 6
```

Backtest also downloads missing symbols when `-auto-download` is left on (the default).

## 2. Create the row

Use either a hand insert or a sweep. Both land in the same table. A bad row is logged and skipped. The command still starts. `./bin/backtest -strategylist` shows only the rows that passed.

A row is skipped when the id is empty or normalizes to `streakstrategy`, a symbol is not one to ten characters matching `A–Z`, digits, `.`, or `-`, direction is anything other than `drop` or `rally`, `signal_days` or `hold_days` is below 1, either offset is negative, the stop offset is 1 or more, the regime is not one of the three labels above, allocation is outside `(0, 1]`, cash yield or slippage is negative, or `next_day_limit` is not 0 or 1.

### By hand

Open the app reference database, not the copy in the git repo.

```bash
sqlite3 refdata/strategies.db
```

The table appears the first time backtest, gridsearch, or scoreboard opens that file. If the `INSERT` says there is no such table, run `./bin/backtest -strategylist` once and try again.

```sql
INSERT INTO streak_strategy (
    id, name,
    signal_symbol, trade_symbol, direction, signal_days,
    hold_days, take_profit_pct, stop_loss_pct, regime,
    allocation_pct, cash_yield, slippage_pct, next_day_limit,
    source_strategy, source_label
) VALUES (
    'streak-voo-up5-tqqq', 'VOO up5 → TQQQ',
    'VOO', 'TQQQ', 'rally', 5,
    15, 0.03, 0.08, 'All Regimes',
    0.65, 0.045, 0, 0,
    'hand', ''
);
```

A second plain `INSERT` of the same id fails. `UPDATE` the columns, or `DELETE` the row and insert it again. Promote upserts, so a later promote of that same streak replaces the row. Use the generated id when you want that.

### From a sweep

Gridsearch a parent that already knows how to streak, then copy the winners.

These parents can be promoted. Anything else is skipped with `direction is not a streak`.

| Parent | Watch | Direction | What the sweep varies |
|---|---|---|---|
| `voo-up3` | VOO | rally | streak length, hold, take-profit, stop. Buy ticker is TQQQ until you pass `-symbol` |

Sweep one parent. `-symbol` is allowed only when `-strategy` names a single id.

```bash
./bin/gridsearch -strategy voo-up3 -symbol TQQQ -no-html
```

That writes `$APP_FOLDER/data/reports/gridsearch.db`. A sweep you already finished somewhere else has to be named:

```bash
./bin/gridsearch promote -strategy voo-up3 \
  -gridsearch-db data/reports/gridsearch.db \
  -min-win-rate 0.6 -min-trades 30 -top 5
```

Leave `-gridsearch-db` off when the sweep used the default file.

Promote defaults, used only for this subcommand: win rate `0.6`, at least `30` trades, `-top 5`. Pass the flag when you want a different cutoff (`-min-trades 20` is enough). `-strategy` is required. Parents are comma-separated.

```bash
./bin/gridsearch promote -strategy voo-up3 \
  -min-win-rate 0.6 -min-trades 30 -top 5
```

The printout lists each written id, the skipped duplicates, and a note when an older sweep row had no `signal_symbol`. Those older rows use the parent's watch symbol. A `-signal` you passed on that old sweep is not stored, so promote cannot recover it. A new sweep stores `signal_symbol`.

`-top` is applied per parent before ids are collapsed. Five VOO-up rows that all buy TQQQ on a 5-day streak become one id, `streak-voo-up5-tqqq`, and the higher win rate is the one written.

## 3. See that it registered

```bash
./bin/backtest -strategylist
./bin/gridsearch -list
```

Both lists include `streak-voo-up5-tqqq` when the row is valid. A skipped row prints `streak_strategy: skip <id>: ...` above the list.

Read the table back:

```bash
sqlite3 refdata/strategies.db \
  "SELECT id, signal_symbol, trade_symbol, direction, signal_days, hold_days,
          take_profit_pct, stop_loss_pct, regime, win_rate, total_trades
   FROM streak_strategy ORDER BY id;"
```

`win_rate` is a fraction. `0.76` is 76 percent.

## 4. Backtest it

```bash
./bin/backtest -strategy streak-voo-up5-tqqq
```

Several ids are comma-separated. `all` includes every streak row along with the strategies that have their own pipeline.

```bash
./bin/backtest -strategy streak-voo-up5-tqqq,streak-gld-down4-gld
./bin/backtest -strategy all
```

One named id always runs. A comma list, and `-strategy all`, skip any id that already has a usable result. Pass `-force` to redo those.

The window starts `2021-01-01`. Capital is `$100,000`. The result database is `$APP_FOLDER/data/reports/streak-voo-up5-tqqq.db`. The next run of that same id writes `streak-voo-up5-tqqq_2.db`, then `_3`, and so on. The HTML tear sheet is `$APP_FOLDER/data/reports/backtest_report.html`.

`-hold`, `-target`, `-stoploss`, and `-alloc` override the row for that run when you set them. They do not change the table. On this command `-target 1.03` is a 3 percent target and `-stoploss 0.92` is an 8 percent stop.

After you edit a row, run that one id again so a new result database is written. Scoreboard keeps the previous result until that newer file exists, or until you pass `-force` to scoreboard.

## 5. Gridsearch a streak row

Searching for a better hold, target, or stop is a sweep of the parent, then another promote. Promoting again upserts the same id.

A streak row's own grid is the single point already stored in the row. Naming it runs that one configuration and records it in `gridsearch.db`:

```bash
./bin/gridsearch params streak-voo-up5-tqqq
./bin/gridsearch -strategy streak-voo-up5-tqqq -no-html
```

`./bin/gridsearch -strategy all` leaves every `streak-*` id out. Pass `-include-streak` to include them. That flag does not matter when `-strategy` names the id.

`./bin/gridsearch -strategy all -include-streak` still sweeps the parents too. Each streak row adds one permutation.

## 6. Scoreboard them

Scoreboard has no strategy filter. It ranks every registered strategy, and streak rows are registered.

```bash
./bin/scoreboard status
./bin/scoreboard
```

`status` only reports which registered ids have a usable result. It does not write `scoreboard.db`.

`./bin/scoreboard` backtests each registered id that lacks a usable result, then writes `$APP_FOLDER/data/reports/scoreboard.db`. An id you already backtested in step 4 is reused. `-force` redoes every strategy, including the Go baselines.

```bash
./bin/scoreboard compile
```

`compile` reads the result databases already on disk and does not simulate. It still lists any registered id that has no usable result. Run `./bin/backtest -strategy <id>` for those, then compile again.

`win_rate` in `scoreboard` is the same fraction as in the strategy table. `0.60` is 60 percent.

```bash
sqlite3 $APP_FOLDER/data/reports/scoreboard.db \
  "SELECT strategy_id, round(win_rate, 3), trades, round(cagr, 3)
   FROM scoreboard
   WHERE strategy_id LIKE 'streak-%'
   ORDER BY win_rate DESC;"
```

## A full pass

This is one parent, one buy ticker, then the new id through backtest and the board.

```bash
make build
./bin/market_history -symbols VOO,TQQQ -years 6
./bin/gridsearch -strategy voo-up3 -symbol TQQQ -no-html
./bin/gridsearch promote -strategy voo-up3 -min-win-rate 0.6 -min-trades 30 -top 5
./bin/backtest -strategylist
./bin/backtest -strategy streak-voo-up5-tqqq
./bin/scoreboard status
./bin/scoreboard compile
```

Promote prints the ids it actually wrote. If the best 5-day TQQQ row lost to a 3-day row, backtest the id in that printout. `streak-voo-up5-tqqq` exists only when a qualifying row had signal days 5 and trade symbol TQQQ.
