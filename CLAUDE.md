# trade_well: rules for all three repos

Applies to `backtestgosqlite` (BT), `schwaber` (SW) and `trade_orchestrator` (TO). If a repo has its own CLAUDE.md, it may add rules but not relax these. Written for every agent that edits this code (Claude, Grok, Antigravity, humans).

## The three repos and who owns what

| Repo | Owns | Depends on |
|---|---|---|
| `schwaber` (SW) | Everything Schwab: API client, OAuth, token storage/sync, order intent, account/order mirror, shared infra (env/config, DB open, GCS, Secret Manager) | nothing local |
| `backtestgosqlite` (BT) | Strategies, signals, backtests, market data, trading calendar, SQL pipelines | nothing local |
| `trade_orchestrator` (TO) | Scheduling and sequencing of jobs: scan, stage, submit, exit, health, email, dashboard, GAE deploy | SW, BT |

Dependency direction is one way: TO imports SW and BT. SW and BT never import TO, and SW and BT do not import each other. If SW and BT both need the same helper, it goes in whichever is lower-level, or in a small new package in SW and BT copies nothing.

**Before writing any function, search all three repos for it** (`grep -rn` on the verb and the noun). If something similar exists in a sibling, import it. If it exists but is unexported, in `package main`, or in the wrong repo, move it to the owning repo, export it, and import it. Never copy it. TO should shrink over time, not grow. Known leftovers are tracked in `KillDupeCode.html` (sections A and C are still open).

## Rule 1: BT: Go controls execution, SQL does the calculation

- Go opens databases, orders the steps, runs SQL files, handles errors, and prints/serves results. It does not compute indicators, returns, joins, windows, or aggregates row by row.
- Calculations live in `.sql` under `backtestgosqlite/sql/` (strategy pipelines under `sql/strategies/`), run through `storage.ExecuteSQLFile`.
- **No nesting.** No subquery inside a subquery, no deep CTE chains. Break a calculation into steps and write each step's result to a **slice table** (a real table holding one stage's output, e.g. one row per symbol per date), then the next step reads that table. Slice tables are how calculation state persists and how you debug: you can `SELECT` any stage.
- Name slice tables by stage so the order is obvious, and make each `.sql` file do one stage.
- Existing Go indicator/tree code (`strategy/indicators.go`, `decisiontree.go`, `pkg/study/*`) predates this rule. Do not add to it. When you touch that logic, move the calculation into SQL slice tables rather than extending the Go.

## Rule 2: one implementation, three execution contexts

Every capability must work from the **CLI**, the **local web server** and **GAE** (cron and HTTP), from one code path.

- `pkg/<capability>` holds the logic, as an exported function that takes a `context.Context`, its inputs, and (if needed) an explicit environment/config value. It returns a result struct and an error. It never calls `os.Exit`, never reads `os.Args`, never writes to `os.Stdout` directly (accept an `io.Writer` or return data), and never assumes it is on a laptop or on GAE.
- `cmd/<binary>` is a thin wrapper: parse flags, build the config, call the `pkg/` function, print the result, set the exit code. No business logic in `cmd/`. (BT already does this well with `pkg/backtest/main.go` and `pkg/livescan/main.go`; follow that.)
- HTTP handlers (`/cron/<job>`, dashboard, OAuth) are the same kind of wrapper: check auth, build the config, call the same `pkg/` function, write the response.
- Where the environment differs (paths, DB replication, secrets, token sync), the difference is a **parameter or a runtime check inside the one package** (`runtimeenv.Detect()`, `APP_FOLDER`, `GCS_BUCKET`, `MARKET_DB`), never a second package or a copy of the function. "Change the context parameter, don't fork the code."
- Never shell out to a binary that exists as a Go package (this is how the old `livescan` binary and `sync_schwab_token.sh` broke on GAE). Import the package. The only acceptable external process is a tool with no Go equivalent (Litestream today).
- A change to a capability is not done until you have checked that its CLI command, its `/cron/...` route (if it has one) and its package tests all still go through the same function.

## Rule 3: consistent names, everywhere

One job has one name, and that name is the same in every place it appears:

`pkg/<job>/` = CLI subcommand `<job>` = HTTP route `/cron/<job>` = `cron.yaml` entry = `job_runs.job` value = dbsync job name = log prefix `[<job>]` = `withDBSync("<job>", ...)`.

- Job names are `snake_case` verb_noun or noun, as TO already has: `account_state`, `scan_market`, `submit_orders`, `manage_exits`, `check_health`. Do not invent a second spelling (`health-checks`, `run-all-jobs`, `active-trades`, `staged-orders`, `open-db` are the current outliers: rename to snake_case and keep the old name only as a documented deprecated alias).
- Libraries (not jobs) are a single lowercase word: `tokensync`, `orderintent`, `accountstore`, `oauthcli`, `dbutil`, `calendar`.
- Same concept, same identifier across repos: strategy IDs, stack IDs (`a+b+c`, parsed only by `strategy.ParseStack`), env var names (`APP_FOLDER`, `SCHWAB_DB_PATH`, `MARKET_DB`, `GCS_BUCKET`, `STRATEGY_ALLOWLIST`), table prefixes, and error/event names.
- Entry-point names match across `cmd/`, `pkg/` and deploy files. If you rename one, rename all of them in the same change (cmd, pkg dir, route, cron.yaml, docs, tests).
- New env vars and flags are documented in the repo README in the same change.

## Working across the repos

- Change the lower-level repo first (SW/BT), test it there, then update TO. TO builds against sibling checkouts through `replace` directives plus `vendor/`: run `go mod vendor` in TO after every SW/BT change, or the build silently uses stale copies.
- Run `go build ./... && go vet ./... && go test ./...` in every repo you touched before saying it works.
- Tests move with the code. When you move a function to a shared package, move its test there too.
- No git repo exists at the trade_well level. Do not do bulk deletes or renames without saying what you are removing first.
- Secrets never go in code, docs, or logs. Token values are never logged.

## Rule 4: no mocks, no fakes, no fake servers, ever

Never write a mock, fake, stub, spy or fake server of any kind, in any repo, in tests or in production code. This includes `httptest.NewServer` and any other in-process stand-in for Schwab, Google, GCS, SMTP, gcloud, the OAuth endpoints or any other external system; hand-written types that impersonate an interface (`fakeX`, `mockX`, `stubX`); test-only variables swapped to change behaviour (`var sendX = ...` "swapped in tests"); and fabricated tokens or responses built to satisfy a fake.

A test that passes against a stand-in proves the stand-in works, not the code, and it hides exactly the failures that cost money here: Schwab's real answers (the TSLL 400) do not match anyone's guess of them.

- Test against the real thing: real SQLite (in-memory or a temp file is the real engine, so it is allowed), real functions with literal inputs, real files in `t.TempDir()`, and the real handlers called directly.
- Where the real external system cannot be used from a test, do **not** write the test. Cover that path with a dry-run mode, a non-mutating broker call (e.g. Schwab `previewOrder`), or logging good enough to diagnose the first live failure. Say plainly that the path has no automated test; do not paper over it.
- Do not add an interface just so a fake can be injected. Interfaces are for real polymorphism.
- If you find an existing mock/fake/fake server, do not extend it and do not copy its pattern. Tell the user, and remove it when asked.
- Requests to "just add a quick mock" are refused; propose the real alternative instead.

Known leftovers that predate this rule and are not yet removed: `fakeLister` (schwaber `pkg/orderintent/store_test.go`), `mockStrategy` (backtestgosqlite `pkg/simulator/shared_account_test.go`), `fakeValidator` (TO `pkg/appserve/cronauth_test.go`), `fakeGcloud` (TO `pkg/cronclient/cronclient_test.go`), and the `sendFailureEmail` variable in TO `pkg/appserve/cron.go`.

## Before you finish, check

1. Did I add a function that already exists in another repo? (search)
2. Is the new logic in `pkg/` with a context parameter, and is `cmd/` just a wrapper?
3. In BT, is the calculation in SQL with slice tables, not nested SQL or Go loops?
4. Do the CLI name, route, cron entry, package name and job name all match?
5. Did I run `go mod vendor` in TO and the tests in every touched repo?
6. Did I introduce any mock, fake, stub or fake server (Rule 4)? If yes, remove it.
