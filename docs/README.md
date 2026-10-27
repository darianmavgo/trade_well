# trade_well docs

One repo, three layers: `pkg/schwaber` (Schwab client and shared infrastructure), `pkg/backtest` (strategies, backtests, SQL pipelines) and `pkg/orchestrator` (jobs, dashboard, GAE deploy). They used to be the repositories `schwaber`, `backtestgosqlite` and `trade_orchestrator`; those names still appear in older text. Paths in these docs are relative to the repo root. Build with `make build` (binaries in `bin/`), check with `make check`.

Reviewed and moved on 2026-10-27. "Updated" means paths and statements were corrected for the merged tree; "as written" means the content is a dated record and was only moved.

## Start here

| Doc | What it is |
|---|---|
| [CLAUDE.md](CLAUDE.md) | The rules for anyone editing this code (layers, SQL does the calculation, one implementation for CLI/web/GAE, naming, no mocks, HTML by default, live-by-default commands). Restored from git history and updated for one repo. |
| [FlattenAssessment.md](FlattenAssessment.md) | Why and how the three repos were merged, and what is still open. |
| [KillDupeCode.md](KillDupeCode.md) | Duplicate-code inventory and what has been removed. |

## Per layer (updated)

| Layer | Docs |
|---|---|
| Orchestration | [orchestrator/README.md](orchestrator/README.md) (jobs, deploy, config object, dashboard), [OrchestratorAPI.md](orchestrator/OrchestratorAPI.md) (every subcommand, web route and cron endpoint), [MarketHistoryDependencies.md](orchestrator/MarketHistoryDependencies.md), [Endpoints.html](orchestrator/Endpoints.html) |
| Calculation | [backtest/README.md](backtest/README.md) (every command, in pipeline order), [Architecture.md](backtest/Architecture.md) (its Simulation section is out of date, see the note there), [ArchitectureViolation.md](backtest/ArchitectureViolation.md), [MigrateGotoSQLTodo.md](backtest/MigrateGotoSQLTodo.md), [strategies/](backtest/strategies) (streak strategies, writing a strategy) |
| Calculation, as written | [HourlyEngineParity.md](backtest/HourlyEngineParity.md), [StrategyLog.md](backtest/StrategyLog.md) (log of false successes), [SQLvsGOModels.md](backtest/SQLvsGOModels.md) (why SQL does the calculation) |
| Foundation | [schwaber/README.md](schwaber/README.md), [SETUP_GUIDE.md](schwaber/SETUP_GUIDE.md) (Schwab developer account and first run), [SchwabAPI.html](schwaber/SchwabAPI.html) (API reference) |

## Cross-cutting (written before the merge, not edited)

[RiskControls.md](RiskControls.md), [MarkovQQQ.md](MarkovQQQ.md), [CorrectlyUsingMarkov.md](CorrectlyUsingMarkov.md), [MixedBacktestUsage.md](MixedBacktestUsage.md), [StrategiesExplored.md](StrategiesExplored.md), [LearningLog.md](LearningLog.md), [CommandConsistency.html](CommandConsistency.html), [MakeMoneyNextDevCycle.html](MakeMoneyNextDevCycle.html), [AllMyProjects.html](AllMyProjects.html). The repo-retirement plans [BacktestConsolidation.md](BacktestConsolidation.md) and [BacktestUnify.md](BacktestUnify.md) predate the merge and talk about `backtestgo` and `backtestgoduckdb`, which are not in this tree.

## research/ (as written)

Studies of other tools and benchmark work: Backtrader and LEAN studies, the Omnifunds benchmark and one-year comparison, the Markov source notes, the research idea list and plan, and a 2026-10-03 stack snapshot (`ViableUpgrades.html`). Not tied to code paths; numbers are as of their dates.

## archive/ (historical)

Superseded or cancelled: the old backtestgo settings table, the Markov implementation plan (superseded by MarkovQQQ and CorrectlyUsingMarkov), the cancelled libsql experiment, and three orchestrator reports from before the Turso migration (stack review, Turso end-to-end run, data-needs analysis). Each carries a note at the top.

## Not carried over

Left behind in the old repositories, which are untouched: scratch and to-do notes (`DoingStuff2026Oct4`, `UpgradeArchitecture.md`, `PotentialUpgradestoTrees.md`, `OctoberDone.md`, `Why.txt`, `STRATEGY_EVAL.md`), the orchestrator `Purpose.md` (superseded by the root `Purpose.md`), an unreferenced image, and a saved copy of Schwab's developer portal page (about 4 MB of third-party HTML and scripts; use https://developer.schwab.com).
