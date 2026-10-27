# Stack search gate

Goal: stack with CAGR >= 80% and max drawdown <= 10%, judged on a held-out year.

Run from the repo root, one `sqlite3` session per database named below.
Parameters are set with `.parameter set` inside the session.

1. `01_gate_walk_forward.sql` on `<run>/walk_forward.db`
   `.parameter set :max_sleeve_dd 0.10`
2. `02_liquidity_returns.sql`, `03_liquidity_screen.sql` on a COPY of the market DB
   (they create tables). `.parameter set :min_adv 50000000`
3. `04_candidates.sql` on `walk_forward.db` with `ATTACH` as `wf`, `mkt`, `ref`
   (`refdata/strategies.db`).
4. Build stacks from `search_04_candidates`, run `backtest stack` (holdout on).
5. `05_stack_gate.sql` on `<run>/stack.db` with `ATTACH '<run>/oos/stack.db' AS oos`
   `.parameter set :min_cagr 0.80` / `.parameter set :max_dd 0.10`

The simulator has no volume cap yet, so stage 3 is the only liquidity control.
