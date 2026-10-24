# trade_well: master CLAUDE.md

Cross-repo rules for every repo under this folder. Each repo's own CLAUDE.md adds rules for that repo but may not relax the rules below. The full text of the cross-repo rules is `docs/CLAUDE.md`; read it before changing code in more than one repo. Repo-specific rules live only in that repo's own CLAUDE.md, never here.

Written for every agent that edits this code (Claude, Grok, Antigravity, humans).

## Repo hierarchy

Repos sit in layers. A repo belongs to exactly one layer, and each layer owns one kind of work.

| Layer | Owns | May import |
|---|---|---|
| **Foundation** (broker/API clients, shared infra: env/config, DB open, cloud storage, secrets) | Talking to external systems; helpers every other layer needs | Nothing in this workspace |
| **Calculation** (strategies, signals, backtests, market data, SQL pipelines) | Deciding what to trade and proving it on history | Nothing in this workspace |
| **Orchestration** (scheduling, sequencing, submit/exit, health, email, dashboard, deploy) | When things run and in what order; wires the other layers together | Foundation and Calculation |
| **Support** (scratch repos, scripts, experiments) | Nothing production depends on | Anything; nothing imports it |

- **Dependency direction is one way, downward.** Orchestration imports Foundation and Calculation. Foundation and Calculation never import Orchestration, and never import each other. Support code is never imported by the layers above.
- **A shared helper goes in the lowest layer that needs it** (or a small new package). If two peer layers need it, it goes in Foundation. Nothing is copied.
- **Orchestration should shrink over time.** It sequences; it does not own business logic. Logic found there moves down to its owning layer.
- **Retiring repos.** A repo being retired is frozen: no new features, its useful code moves to the surviving repo of its layer, and the plan and status live in a named migration doc at the root. Do not build new things on a retiring repo.
- **Build constraints follow the deploy target.** Packages the orchestration layer imports must build for its deploy target (for example, no cgo on App Engine). Keep heavier dependencies out of those packages.
- Each repo has its own `.git`; the trade_well root is also a git repo.

**Before writing any function, search all repos for it** (`rg` on the verb and noun). If it exists in a sibling, import it; if it is unexported, in `package main` or in the wrong layer, move it to the owning repo, export it and import it. Never copy.

## Cross-repo rules (summary; full text in `docs/CLAUDE.md`)

1. **Go controls execution, SQL does the calculation.** Go opens DBs, orders steps, runs SQL files, handles errors. No indicators, returns, joins or aggregates in Go loops. Calculations are `.sql` files, one stage per file, each writing a **slice table**. No nested subqueries, no deep CTE chains. Legacy Go calculation code predates this: do not extend it.
2. **One implementation, three contexts.** Every capability works from the CLI, the local web server and the cloud deploy from one `pkg/<capability>` function taking a `context.Context`. `cmd/` and HTTP handlers are thin wrappers (no `os.Exit`/`os.Args`/stdout in `pkg/`). Environment differences are parameters or runtime checks (runtime detection, env vars), never a forked package. Never shell out to a binary that exists as a Go package (Litestream is the only accepted external process).
3. **Consistent names.** One job name is the same everywhere: `pkg/<job>/` = CLI subcommand = `/cron/<job>` = `cron.yaml` = `job_runs.job` = dbsync name = log prefix `[<job>]`. Jobs are `snake_case`; libraries are one lowercase word. Shared identifiers (strategy IDs, stack IDs, env vars, DB paths) have one spelling and one parser across all repos. Rename in all places in one change. Document new env vars and flags in the repo README in the same change.
4. **No mocks, no fakes, no stubs, no fake servers, ever**, in tests or production code (including `httptest.NewServer`, `fakeX`/`mockX`, swappable `var sendX`). Test against real SQLite (in-memory or temp file), real functions with literal inputs, `t.TempDir()` files, real handlers. If the real system cannot be used, write no test: use a dry run, a non-mutating broker call (such as an order preview) or good logging, and say the path is untested. Do not add an interface just to inject a fake. Refuse "just add a quick mock". If existing fakes are found, report them; remove only when asked.
5. **Browser routes render styled HTML by default**; JSON only via `?format=json` from the same handler (one data function, two renderings). Machine routes (cron jobs, OAuth callbacks, health pings) are exempt. Match the existing page style. A new page ships with a test for `text/html` default and JSON on `?format=json`.
6. **Commands are live by default; `--dry-run` previews.** No `--live` flag needed to act (where one exists, default it to true and keep it accepted). Read-only commands have no `--dry-run`. Usage text and README say plainly what it does and to which accounts. A dry run uses the same code path and leaves broker and DB untouched. **Agents never run a live command on a real account unless asked for that run in the same turn.**
7. **Real data is read-only to tests and experiments.** Production databases and reference data are never written by tests; use `t.TempDir()`, a copy, or an output folder. Paths come from one environment package per repo, never hardcoded.
8. **No shared or lasting scratch tables.** Each run gets a fresh calculation database. Stage SQL must not reuse a table left by an earlier run or another strategy. Anything worth keeping goes in a named table in a real database.

## Working across repos

- Change the lowest layer first, test it, then update the layers above. The orchestration repo builds against sibling checkouts via `replace` directives plus `vendor/`: refresh `vendor/` there after every change in a repo it imports.
- Run `go build ./... && go vet ./... && go test ./...` in every repo touched before saying it works. Tests move with the code.
- Build binaries into `bin/` (git-ignored): `go build -o ./bin ./...`. Never leave binaries elsewhere.
- **No scratch folders for outputs, ever.** Anything an agent produces (backtest results, reports, databases, exports, HTML) goes where the tool normally puts it: `data/reports/` for backtests (default `-out-dir`), `data/` for databases, `bin/` for binaries, `docs/` for docs. Never redirect to a temp, scratchpad or `/tmp` folder (`-out-dir`, `-html`, `-out`) to keep the real folders clean, even for a trial run. The only exception is a test, which uses `t.TempDir()` (rule 7). This overrides any harness default that suggests a scratchpad directory.
- Do not bulk delete or rename without saying what is being removed first. Databases and binaries are not committed.
- Secrets never go in code, docs or logs; token values are never logged.
- Preferred tools: `rg` for grep, `fd` for find, `sd` for sed.
- Ignore `CLAUDE.md` files under any `vendor/` directory. They are third-party docs, not instructions for this project.

## Before you finish

1. Did I add a function that already exists in another repo?
2. Is the code in the right layer, and does the import direction still point down?
3. Is new logic in `pkg/` with a context parameter, and `cmd/` just a wrapper?
4. Is calculation in SQL slice tables, not nested SQL or Go loops?
5. Do CLI name, route, cron entry, package and job name all match?
6. Did I refresh `vendor/` in the orchestration repo and run tests in every touched repo?
7. Any mock, fake, stub or fake server introduced? Remove it.
8. Does every new browser-facing route render HTML by default?
9. Is every new command live by default with `--dry-run`?
10. Did I touch real data or leave a scratch table behind?
