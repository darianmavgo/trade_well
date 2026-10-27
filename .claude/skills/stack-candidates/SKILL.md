---
name: stack-candidates
description: Run train, backtest, gridsearch and strateval as needed on the backtestgosqlite repo, then report the top 20 strategies across all families (markov, tree, streak, hold, hold_bail, builtin) worth trying in a stack. Use when asked which strategies to stack, for a fresh cross-family ranking, or to refresh results after new market data.
---

# Stack candidates

Goal: one ranked list of the 20 strategies across every family that are worth putting
in a stack, after doing only the work that is out of date.

Work from the repo root (`backtestgosqlite`). Never write to `data/market_history.db`
or `refdata/strategies.db`, never commit, and never run `market_history` unless the user
asks for fresh market data. Run the long steps in the background and poll their logs.

## 0. Build and read the state

```bash
go build -o ./bin ./...
MARKET_MAX=$(sqlite3 data/market_history.db "select max(Date) from backtest_start where symbol='SPY'")
IS_END=$(sqlite3 :memory: "select date('$MARKET_MAX','-12 months')")
echo "market data to $MARKET_MAX, in-sample ends $IS_END (last 12 months are held out)"
```

The backtest holds out the last 12 months by default. Everything below keeps those
months out of sample, so do not pass `-holdout-months 0` anywhere.

## 1. train, only if stale

Markov is walk-forward, so it never sees the future. It is fresh when its models reach
the last bar:

```bash
sqlite3 data/markov_models.db "select count(*), max(last_date) from markov_model_meta"
```

If it is missing or `max(last_date)` is before `$MARKET_MAX`: `./bin/train markov` (about 3 minutes).

Trees must be trained only on the in-sample window, or the held-out months are not out
of sample for them:

```bash
sqlite3 data/tree_models.db "select count(*), max(last_date) from tree_model_meta"
```

Fresh means `max(last_date)` is on or before `$IS_END` and within about 10 days of it.
Otherwise: `./bin/train tree -through $IS_END` (about 8 minutes). `streak`, `hold` and
`hold_bail` have nothing to train.

If the user wants to trade the result live, say that the trees then need one more
`./bin/train tree` over all history. Do not do it as part of this skill.

## 2. backtest every family, only if there is no current run

Find the newest run (`data/reports/<number>/`). It is current if it has a `.db` for each
family plus the same files in `oos/`, and its held-out results reach the last bar:

```bash
RUN=$(ls -d data/reports/[0-9]* 2>/dev/null | sort -t/ -k3 -n | tail -1)
sqlite3 $RUN/oos/markov.db "select max(market_max_date) from runs"   # must equal $MARKET_MAX
ls $RUN $RUN/oos
```

It is also stale if the models were trained after it was written. If it is not current:

```bash
./bin/backtest -strategy all -auto-download=false > /tmp/backtest_all.log 2>&1 &
```

This starts a new run folder and prints `📁 Run N: ...` first. All families take roughly
15 to 30 minutes, with the price-action and annual-winner strategies the slowest. If it is
interrupted, continue it with `-run-id N`: strategies already finished in that run are skipped.

## 3. rank, then gridsearch the front runners

First pass over the in-sample and held-out results:

```bash
.claude/skills/stack-candidates/top_strategies.sh $RUN 50
IDS=$(IDS_ONLY=1 .claude/skills/stack-candidates/top_strategies.sh $RUN 50)
```

Then tune only those, on the in-sample window only. `-end` is what keeps the held-out
months from tuning the parameters:

```bash
./bin/gridsearch -strategy "$IDS" -end $IS_END -no-html -min-trades 20
```

`gridsearch` skips strategies it has already swept, so add `-force` only if the market
data or the window changed. Streak rows search signal days, hold, take-profit, stop and
regime. Tree and markov rows search exits only. `hold` and `hold_bail` run once. The
columns are listed in `strategy_family_param` in `refdata/strategies.db`.

## 4. strateval on the same candidates

```bash
./bin/strateval -strategy "$IDS" -optimize -oos-months 12
./bin/strateval report
```

This does its own in-sample parameter sweep and a held-out evaluation and writes a tier
(A is best) to `data/reports/strategies.db`. The ranking script shows that tier next to
each strategy.

## 5. report

```bash
.claude/skills/stack-candidates/top_strategies.sh $RUN 20
```

The script ranks by the lower of the in-sample and held-out Calmar ratio. A strategy must
have at least 30 in-sample trades and 8 held-out trades, be profitable in both windows and
have held-out drawdown of at most 30%, and trade at least $5M a day on average over the
held-out year (the simulator has no volume limit, so without this gate the list fills with
penny stocks that cannot take real size). It keeps one strategy per trade symbol and at most
8 per family. Override with `MIN_IS_TRADES`, `MIN_OOS_TRADES`, `MAX_OOS_DD`, `PER_FAMILY`,
`MIN_DOLLAR_VOL`.

Give the user:

1. The table: rank, strategy, family, symbol, score, in-sample and held-out CAGR,
   drawdown and trades, strateval tier, and the gridsearch best config.
2. The funnel the script prints (how many strategies survive each gate), and which
   steps you ran or skipped and why.
3. How many of the 20 are in each family. If one family has them all, say so.
4. These caveats, because they change how far to trust the list:
   - The ranking is a screen over tens of thousands of strategies, so some winners are
     luck. The held-out window is only 12 months.
   - The row parameters of the markov and tree families are identical templates
     (only the symbol differs), so the list ranks symbols more than ideas.
   - The held-out year helped choose the list, so its held-out numbers are optimistic.
   - The score picks strategies one at a time. It does not check that they are
     complementary.

## 6. optional: confirm as a stack

Only when the user asks for it:

```bash
./bin/backtest stack-eval -primary <rank 1 id> -secondary <ids of ranks 2 to 20, comma separated> -persist-best
```

`stack-eval` ranks each strategy as an overlay on the primary in one shared cash account
and builds a greedy stack, then runs the held-out months once. Report the stack it
builds and its held-out result next to the list.
