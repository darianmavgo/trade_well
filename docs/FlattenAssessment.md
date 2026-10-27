# Flattening trade_orchestrator, backtestgosqlite and schwaber into one repo


Written 2026-10-27. Assessment only: nothing has been moved. Facts below come from the working trees on that date.

## The question

Replace three repos (three Go modules, three histories, `replace` directives and a `vendor/` refresh step) with one repo and one Go module whose `pkg/` holds everything. The aim is a public repo that others can clone, build and use, and less cross-repo overhead for agents and for you.

## Where things stand

| | trade_orchestrator | backtestgosqlite | schwaber |
|---|---|---|---|
| Layer (see CLAUDE.md) | Orchestration | Calculation | Foundation |
| Go packages | 44 | 80 | 17 |
| Go lines (incl. tests) | about 40k | about 58k | about 8k |
| Commands in `cmd/` | 1 (`orchestrator`) | 24 | 1 (`schwaber`) |
| `.git` size | 1.8 MB | 32 MB (refdata `.db` files) | 34 MB (old `schwaber.db` commits) |
| Imports the others? | schwaber and backtestgosqlite | nothing | nothing |

- Imports already point one way: orchestrator imports the other two (71 files import schwaber, 60 import backtestgosqlite). Neither of those imports the other, and neither imports orchestrator. There are no cycles to untangle.
- Two package names exist in more than one repo: `killswitch` and `orders`. They are **not duplicates**: schwaber's are CLI commands (`killswitch.Run`, `orders.Place/Cancel/Bracket/List`), while the orchestrator's are state and display (`killswitch.Set/Current/KillAll`, `orders.Rows/Format`). They only collide by name, so they get renamed or merged by role when flattened (see "Killing duplicate code" below for the real duplicates).
- Import paths would change in up to about 540 files (cross-repo imports plus each repo's own self-imports). This is mechanical and a build catches mistakes.

## Pros

1. **No more `replace` and `vendor/` dance.** The orchestrator builds against sibling checkouts through `replace ../schwaber` and `../backtestgosqlite`, and `vendor/` must be refreshed after every lower-layer change (CLAUDE.md "Working across repos"). One module removes that step and the stale-vendor bug class.
2. **It fixes the "others can use it" blocker.** Outsiders cannot `go get` the orchestrator today because of the `replace` lines and the private schwaber repo. One module with one tag works with `go install`.
3. **A clean history comes with it.** The new repo can start from a squashed or filtered history. That gets rid of `schwaber.db` (real account rows) in schwaber's history and the 33 MB of old `refdata/*.db` blobs in backtestgosqlite's history without a separate rewrite of each repo.
4. **Atomic cross-layer changes.** A change to a Foundation function and all its callers lands in one commit, one test run, one review. "Change the lowest layer first, then update the layers above" stops being three commits in three repos.
5. **One of everything else.** One LICENSE, README, CI workflow, `.env.example`, CLAUDE.md, release tag and dependency list. Today there are 44 + 16 + 16 `go.mod` requirement lines, with overlap and version skew possible.
6. **"Search all repos before writing a function" becomes `rg` in one tree.** Duplicate-function drift (docs/KillDupeCode.md) is easier to prevent and to see.
7. **Simpler for agents.** One working directory, one set of rules, no per-repo `CLAUDE.md` that may not relax the master. The retiring `backtestgo` repo question goes away too.
8. **Duplicate code gets easier to kill.** Removing a copy across repos means promoting it down a layer, tagging and re-vendoring. Most of the big duplicates are already gone; what is left is small, and the part that crosses the schwaber / backtestgosqlite layer boundary is blocked only by the repo split. One clone detector can also see the whole tree. See "Killing duplicate code" below.

## Cons

1. **The layer boundary stops being enforced by the build.** Today backtestgosqlite cannot import schwaber because schwaber is not in its `go.mod`. In one module any package can import any other, so Foundation could import Orchestration without an error. This is the biggest loss, because the layering is the core of your rules. It must be replaced by a test that fails when an import points the wrong way (a `go list` based check in `go test ./...`, or a linter such as depguard). Without that, flattening weakens the architecture.
2. **Schwab client is no longer separately reusable.** The most generally useful piece for outsiders (schwaber: OAuth, orders, quotes) is small, about 8k lines, and has only 16 requirements. Inside a big module it still imports fine, but its `go.mod` becomes the union of all dependencies, including Turso, GCP and email libraries. People who only want a Schwab client pull in far more than they need.
3. **One version number for everything.** You can no longer tag a Foundation fix without releasing the strategy code with it. For a trading system that is mostly fine, but it removes the option of pinning the order path while experimenting on strategies.
4. **Blast radius of the experiments.** backtestgosqlite is a research repo with 24 commands and a lot of study code (`pkg/backtest/study`, gridsearch, parked sweeps). In one repo, research churn sits beside the live order path, and a broken experimental package breaks `go build ./...` and `go vet ./...` for the whole tree. Mitigation: keep research packages compiling, or delete the dead ones first.
6. **Heavy data in the checkout.** `refdata/universe.db` (24 MB) and `refdata/strategies.db` (9 MB) come with it. Everyone who clones the repo downloads them unless they are removed or moved to release assets or a separate data repo. (Your docs say "Data is separate repo now" for `data/`, so there is precedent.)
7. **History is lost or tangled.** There are 233 + 144 + 40 commits. Options: squash (clean, loses blame), or `git filter-repo` path-rewrite merge (keeps blame, keeps the sensitive blobs unless filtered). Either way old commit hashes in docs and the "Update ..." messages stop resolving.
8. **Flattening is a large, risky one-time change.** Up to about 540 files get new import paths. The module is also the deploy unit: App Engine config (`deploy_gae/`), cron job names, `.gcloudignore` and the vendor flow all assume the current layout. A mistake could break the scheduled jobs that place real orders. It needs a dry run on a branch, a full test pass and a deploy to a non-live service first.
9. **The master rules need rewriting.** CLAUDE.md and docs/CLAUDE.md describe three repos, per-repo CLAUDE.md files, `replace` directives and vendor refresh. All of that changes, and the layer table has to move from repos to directories.
10. **Name collisions.** `killswitch` and `orders` exist in two repos each, and 26 `cmd/` entries share one `cmd/` tree. None collide today, but the merged `README` and binary list get long (24 research commands beside one production binary).

## Killing duplicate code (the second reason to flatten)

Flattening is the cheapest moment to remove duplicates. Across repos a duplicate can only be removed by promoting the code into a lower repo, tagging it and refreshing `vendor/`, so it often does not get done. In one module the same change is an ordinary refactor. The rule in CLAUDE.md ("search all repos, import, never copy") then becomes one `rg`.

`docs/KillDupeCode.md` was the inventory of 16 items, mostly orchestrator code that schwaber or backtestgosqlite already provides. It was **re-scanned on 2026-10-27** and rewritten with the current status (symbol searches plus reading the bodies of five items; no clone detector, nothing compiled for the scan). Result:

- **9 of 16 are done:** `gcsAccessToken`, Secret Manager, `flattenOrders`, token sync, interactive OAuth, the old `dbsync` (removed), market-DB path, the signal-scan wrapper, and the trading calendar. `SafetyGuard` is also now used by `submit_orders`.
- **5 are partly done, with small leftovers:** `.env` parsing (BT `appenv` still has its own loop), account ingest (`accountstore` exists; 386 lines remain in TO, not yet compared), linked-account resolution, CSV and stack parsing, and SQLite pragmas (SW `dbutil.Open` vs BT `storage.OpenSQLite`).
- **2 are open:** `orderNumericID` (identical 6 lines in TO and SW) and `windowForSignal` (in both `stage_orders` and `manage_exits`).
- **What is left to remove is about 120 to 150 lines**, not the roughly 900 to 1,700 the first version estimated. The large wins have already happened without flattening.

This changes the argument. Duplicate removal is a smaller reason to flatten than first thought. The remaining items that do cross the Foundation and Calculation boundary (`.env` parsing and the pragma list, both between schwaber and backtestgosqlite) are the ones the layer rule blocks today, and they are the strongest case for a shared foundation package inside one module.

### New leads from a function-name scan across the three repos

Functions with the same name in two or more repos (excluding `main`, `init`, `New`, `Run`, `Open`, `Close`). Most are coincidences; these deserve a real comparison:

| Candidate | Where | Read |
|---|---|---|
| `RunSignalScan` | orchestrator `scan_market`, backtestgosqlite `runner` | **Not a duplicate** (checked): the orchestrator's calls `runner.RunLiveScan` and adds bar sync and persistence. Name collision only; rename when flattened |
| `resolveStrategies` | orchestrator `appserve/cron.go:105` (DB allowlist), backtestgosqlite `strateval/cmd.go:305` (CLI string) | Different sources. BT also has `runner.ResolveStrategies`, so `strateval` may duplicate that inside BT (bodies not compared) |
| `heldTail` | orchestrator `modelstate/bars.go:109`, backtestgosqlite `dailybars/dailybars.go:143` | Same scan loop over different tables (`daily_bars_live` vs the backtest store). Share the row-scan, keep the SQL |
| `dedupe` | orchestrator `live_vs_backtest_trades`, backtestgosqlite `pipeline` | Different element types; use `slices.Compact` or a generic helper, not a copy |
| `recordRun` | orchestrator `appserve/runlog.go`, backtestgosqlite `gridsearch` | Different jobs (HTTP middleware vs sweep outcome). Not a duplicate; leave |
| `Assess` | orchestrator `risk`, backtestgosqlite `reliability` | Different meaning. Not a duplicate; leave |
| `EnsureSchema` | 21 packages in the orchestrator, plus schwaber and backtestgosqlite | Each package owns its tables, which fits the rules. Look only for one shared "run this DDL" helper, not a merged schema |
| `Format` | 5 packages | Each prints its own table. A shared table writer could replace the hand-rolled column code; low value |

Also worth checking, since the name scan misses them: `.env` parsing (now two real copies: schwaber `config.ParseEnv` and BT `appenv.load()`), `appenv.Folder()` vs the orchestrator's and schwaber's own `APP_FOLDER` handling (2.7), and the number and date formatting helpers (`pct`, `num`, `day`, `abs` appear in both orchestrator and backtestgosqlite).

### How to do it inside the flattening

1. **Before the move:** the re-scan is done (see `KillDupeCode.md` section 2). Do the two open items (`LocationOrderID`, `strategy.WindowFor`) and the CSV-parse helper first: they are small and need no layer decision.
2. **During the move:** when a package lands in the new tree, decide its owning layer. Where two copies exist, the lower layer's copy wins and the other is deleted, not moved.
3. **After the move:** one real-duplicate pass using a clone detector over the single module (for example `dupl` or `jscpd`), which cannot see across three modules today. Treat the output as leads: confirm by reading both copies, as the table above does.
4. Every removal needs its tests to move or merge with it (Rule 4: no stand-ins), and the vendor and import checks pass after each step.

Expected size: about 120 to 150 lines from the known items, plus whatever a clone detector finds after the move (the BT study decision-tree files are the one big unknown: they share only 1 function name, so they are not obvious copies).

## What does not change

- The one-way dependency rule, Rules 1 to 8, the job-naming rule and the "no mocks" rule all still apply. Flattening only changes how they are enforced.
- The deploy target (App Engine, pure Go, Turso) is unchanged.

## Options

| Option | What it means | Verdict |
|---|---|---|
| A. Stay at three repos, fix the blockers | Tag releases, drop `replace`, publish three repos | Least risk, but keeps vendor friction and three of everything |
| B. **Flatten all three into one module** | One repo, one `go.mod`, layers as directories, import test enforcing direction | Recommended, if the import check is added in the same change |
| C. Flatten two, keep schwaber | Orchestrator + backtestgosqlite merge, schwaber stays a public Schwab client library | Good middle path if you want the Schwab client to be reusable on its own |

## Recommendation

Go with **B**, in this order, so the layering is never unenforced:

1. Before moving anything, write the import-direction test (and run it against the three repos as they are, to prove it passes).
3. Decide what happens to the `refdata/*.db` files, `baseline.json` and the bundled `strategies.db` (these decide the clone size and the history question).
4. Create the new repo from a filtered or squashed history that omits `schwaber.db` and the large blobs.
5. Move schwaber, then backtestgosqlite, then orchestrator in, rewriting import paths; build and test after each move.
6. Run the post-move duplicate pass over the single module (promote or delete real copies, keep the lower layer's version).
7. Rewrite CLAUDE.md and docs/CLAUDE.md for one repo; delete the `replace` and `vendor` steps.
8. Deploy to a non-live service and compare `/ready` and the cron jobs before pointing production at it.

If the Schwab client's independence matters to you, choose **C** instead and keep the Foundation clients free of any import from the rest of the tree so they can be extracted later.

## Open questions for you

- Is the strategy set (`strategies.db`, `refdata/`) going into the public repo, or staying private?
- Do you want the Schwab client reusable on its own (option C), or is one repo enough?
- Squash history (simple) or keep blame with a filtered merge (more work, bigger repo)?
- What name should the new module have? It decides every import path, so it should be settled before the move.

## After the merge (2026-10-27)

Done: the layer test, three duplicate removals (`schwab.LocationOrderID`, `strategy.WindowFor`, `csvlist.Unique` / `cliutils.SplitList` / `slices.Contains`), merged `.gitignore`, test paths adjusted for the deeper layout.

Open, in rough priority order:

1. **Deploy scripts are untested.** `deploy_gae/*`, `deploy_live_local/*`, `deploy_report_local/*` and `scripts/export_strategies.sh` were copied; only the sibling-repo path references were fixed (`deploy.sh` change hash, `.gcloudignore` now keeps `sql/`, `export_strategies.sh` paths). `deploy.sh` still vendors "private modules" with a GitHub token, which a single module no longer needs, and the local launchers' `replace`-watching logic is now dead. No deploy was run.
2. **CLAUDE.md files.** The root `CLAUDE.md` and `docs/CLAUDE.md` still describe three repos, `replace` directives and the `vendor/` refresh. The per-repo rule files were copied next to their code (`pkg/orchestrator/CLAUDE.md`, `pkg/backtest/CLAUDE.md`, `GEMINI.md`, `GROK.md`) and still say "this repo".
3. **Data in the tree.** `refdata/*.db` and `pkg/orchestrator/strategies/deploy/strategies.db` are copied and un-ignored, as the old repos tracked them. The other three embedded bundle files (`markov_models.db`, `tree_models.db`, `hmm_models.db`) are ignored but needed to compile, as before. Decide whether any of this ships publicly.
4. **Layer-crossing duplicates** (`.env` parsing and the pragma list, schwaber vs backtest) need a decision on where a shared foundation package lives; the layer test currently forbids either importing the other.
5. **Name collisions** still exist as separate directories, not as errors: `pkg/schwaber/{killswitch,orders}` vs `pkg/orchestrator/{killswitch,orders}`, and the two `RunSignalScan`.
6. **Real data is not in the tree** (`data/` was not copied), so tests that read `data/market_history*.db` skip or run on empty paths here; they ran against the old repos' local `data/` before.
7. **Old repos are still on disk** next to the new tree (ignored by the root `.gitignore`). Archive or delete them only after the points above are settled.
8. **Everything in the earlier public-release list still applies**: LICENSE, hardcoded project ID and email, `schwaber.db` in schwaber's history (a new repo from this tree does not carry that history), READMEs, CI.
