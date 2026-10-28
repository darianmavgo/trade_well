# Plan: ingest JSON into SQLite with mksqlite, and land it in Turso

Status: plan only. Nothing is built. Source read: `github.com/darianmavgo/mksqlite` (converters, JSON driver, engine), `pkg/schwaber/dbutil`, `pkg/schwaber/accountstore`, `pkg/backtest/datasource/yahoo.go`.

Goal: pull data from Schwab, Yahoo and any other JSON source into SQLite tables without a schema hardcoded in Go, then land those rows in Turso.

## What mksqlite does and does not do

- **JSON shape.** An array root becomes one table, `jsontb0`. An object root becomes one table per array-valued key. Nested objects and arrays become TEXT columns holding JSON. Column types are inferred as INTEGER, REAL or TEXT, and any TEXT value in a column makes the whole column TEXT.
- **Output.** `ImportToSQLite` writes a whole new database file through `modernc.org/sqlite`, the driver this repo already uses, so it is pure Go. The `Register` / `Open` / `RowProvider` API is public.
- **What it lacks.** No primary keys, no indexes, no upsert. It uses plain `CREATE TABLE`, not `IF NOT EXISTS`, and has no append or merge. `engine.go` is marked "DO NOT MODIFY", so it is not patched.
- **Yahoo does not fit directly.** The chart response is columnar: parallel `timestamp`, `open`, `close` arrays nested under `chart.result[0].indicators`. The converter would land a few TEXT blobs, not bar rows.

## Design: land raw, then shape in SQL

Rule 1 puts calculation in SQL files, so JSON is landed raw and shaped by SQL stages.

1. **Land.** A new Foundation package (for example `pkg/schwaber/ingest`) with `ingest.Land(ctx, source, body io.Reader, dst *sql.DB, table)`.
   - It uses mksqlite's JSON driver as the `RowProvider`. It imports only `converters` and `converters/json`, not `converters/all`, so the PDF and Excel dependencies stay out of the App Engine build.
   - It runs the load itself with `common.GenCreateTableSQLWithTypes` and `GenPreparedStmt`, using `CREATE TABLE IF NOT EXISTS` and batched inserts. It does not call `ImportToSQLite`, which insists on creating its own file.
2. **Shape.** SQL files under `sql/stages` turn the landed tables into typed slice tables. For Yahoo, a `json_each` stage reads the raw body and joins `timestamp` to `close` by array position, producing bar rows with no Go struct (`YFResponse`) and no Go loop.
3. **Sources.**
   - Schwab positions, orders, transactions and quotes return arrays of objects, which suit mksqlite directly.
   - Yahoo and other columnar JSON: land the raw body in one row and unnest it in SQL.
   - A small per-source config (URL, key columns, pinned column types, target table) is configuration, not a schema.
4. **Order of work.** Foundation first, tested on real SQLite with literal JSON bodies (rule 4). Then a thin `cmd/` wrapper with `--dry-run` (rule 6). Existing Go clients and hardcoded tables move over one source at a time. State-machine tables (`staged_orders`, `active_trades`, `job_runs` and similar) stay hardcoded: they are not ingested data.

## What changes to land rows in Turso

mksqlite output is plain SQLite and Turso is libsql, so types are compatible. The work is in what mksqlite does not do.

| Gap | Change |
|---|---|
| No keys, so reloads duplicate rows | Take key columns from the source config. Create a unique index on them and load with `INSERT OR REPLACE`. For snapshot sources, delete and insert in one transaction. |
| New JSON fields cause schema drift | Before each load, compare `PRAGMA table_info` on the destination with the inferred headers and run `ALTER TABLE ADD COLUMN` for new ones. Never drop columns. |
| Inferred types can flip between loads | A column that looked INTEGER yesterday may be TEXT today. SQLite tolerates it, but a Turso table created with the wrong affinity keeps it. Pin types for key and date columns in the source config. |
| Every statement is an HTTPS call | Batch inserts as `PersistBars` already does (200 rows), not mksqlite's 1000-row transactions. |
| Date columns | Open Turso only through `dbutil.OpenTurso`. Its time adapter learns date columns from the DDL it sees, so tables must be created through that connection. |
| `dbutil.Turso()` is off under `go test` | The Turso path cannot be tested. Test the loader against two real SQLite files (same `database/sql` interface) and report the Turso step as untested. A `--dry-run` that prints the DDL, the column diff and row counts covers it. |
| Alternative: stage a file, then copy | `ImportToSQLite` could write a scratch file and a generic copier push its tables to Turso. That is a second pass with no benefit; the direct loader is recommended. |

## Constraints from CLAUDE.md

- The loader lives in Foundation (`pkg/schwaber` or a small new package) and imports nothing from Calculation or Orchestration.
- Logic is in `pkg/` with a `context.Context`; `cmd/` and HTTP handlers only wrap it.
- No mocks, fakes or fake servers. No `httptest.NewServer` for the sources: use literal JSON bodies.
- Each run gets a fresh scratch database. Real databases are read-only to tests (rule 7).
- New env vars and flags are documented in the README of the layer in the same change.

## Open items to check before building

- Whether the Schwab client exposes raw response bodies. `accountstore` has a raw-response table, so probably yes, but it may return typed structs.
- Whether mksqlite's Go 1.25 requirement matches this module's `go` line.
- Which tables move first. Proposed: Yahoo bars (removes `YFResponse`, read-only market data). The Schwab account mirror tables are live and go second.
