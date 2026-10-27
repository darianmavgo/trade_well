package modelstate

// Everything in this file reads or writes the Markov model ASSET: markov_models.db, built in
// backtestgosqlite and shipped in the binary. The instance holds a copy in its working folder
// that the markov strategies extend with one prediction per new session; the functions here carry
// those extension rows between that copy and Turso.

import (
	"context"
	"database/sql"
	"fmt"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

type markovModelAssetMeta struct {
	symbol, trainedAt, lastDate string
	trainedThrough              sql.NullString
}

func readMarkovModelAssetMeta(local *sql.DB) ([]markovModelAssetMeta, error) {
	rows, err := local.Query(orchestratorsql.Q("modelstate/read_markov_model_asset_meta"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []markovModelAssetMeta
	for rows.Next() {
		var m markovModelAssetMeta
		if err := rows.Scan(&m.symbol, &m.trainedAt, &m.lastDate, &m.trainedThrough); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// HydrateMarkovModelAssetFromTurso copies the stored predictions newer than each local Markov model's last_date
// into the model database at modelPath and moves last_date forward, as ExtendMarkov would: it
// also adds the sessions to the model's bar count and, for a model that has not been extended
// yet, records the old last_date as trained_through (the session it learned from). It returns
// how many sessions it added.
func HydrateMarkovModelAssetFromTurso(ctx context.Context, db *sql.DB, modelPath string) (int, error) {
	if err := ensureMarkovSchema(db); err != nil {
		return 0, err
	}
	local, err := openModelAsset(modelPath)
	if err != nil {
		return 0, err
	}
	defer local.Close()
	ms, err := readMarkovModelAssetMeta(local)
	if err != nil {
		return 0, fmt.Errorf("modelstate: read %s: %w", modelPath, err)
	}
	added := 0
	for _, m := range ms {
		n, err := hydrateMarkovModelAssetFromTursoOne(ctx, db, local, m)
		if err != nil {
			return added, fmt.Errorf("modelstate: load %s predictions: %w", m.symbol, err)
		}
		added += n
	}
	return added, nil
}

func hydrateMarkovModelAssetFromTursoOne(ctx context.Context, db, local *sql.DB, m markovModelAssetMeta) (int, error) {
	rows, err := db.QueryContext(ctx, orchestratorsql.Q("modelstate/hydrate_markov_model_asset_from_turso_one"), m.symbol, m.trainedAt, m.lastDate)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	tx, err := local.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	last, n := "", 0
	for rows.Next() {
		var date string
		var state int
		var bull, bear, signal float64
		if err := rows.Scan(&date, &state, &bull, &bear, &signal); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, orchestratorsql.Q("modelstate/hydrate_markov_model_asset_from_turso_one_2"),
			m.symbol, date, state, bull, bear, signal); err != nil {
			return 0, err
		}
		last = date
		n++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	if _, err := tx.ExecContext(ctx, orchestratorsql.Q("modelstate/hydrate_markov_model_asset_from_turso_one_3"),
		n, last, m.symbol); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// PersistMarkovModelAssetToTurso stores the predictions in the local Markov model database at modelPath that
// Turso does not have yet: those newer than both the model's learned-through session and the
// newest stored one. It returns how many sessions it stored.
func PersistMarkovModelAssetToTurso(ctx context.Context, db *sql.DB, modelPath string) (int, error) {
	if err := ensureMarkovSchema(db); err != nil {
		return 0, err
	}
	local, err := openModelAsset(modelPath)
	if err != nil {
		return 0, err
	}
	defer local.Close()
	ms, err := readMarkovModelAssetMeta(local)
	if err != nil {
		return 0, fmt.Errorf("modelstate: read %s: %w", modelPath, err)
	}
	stored := 0
	for _, m := range ms {
		n, err := persistMarkovModelAssetToTursoOne(ctx, db, local, m)
		if err != nil {
			return stored, fmt.Errorf("modelstate: store %s predictions: %w", m.symbol, err)
		}
		stored += n
	}
	return stored, nil
}

func persistMarkovModelAssetToTursoOne(ctx context.Context, db, local *sql.DB, m markovModelAssetMeta) (int, error) {
	since := m.lastDate
	if m.trainedThrough.Valid && m.trainedThrough.String != "" {
		since = m.trainedThrough.String
	}
	var newest sql.NullString
	if err := db.QueryRowContext(ctx, orchestratorsql.Q("modelstate/persist_markov_model_asset_to_turso_one"), m.symbol, m.trainedAt).Scan(&newest); err != nil {
		return 0, err
	}
	if newest.Valid && newest.String > since {
		since = newest.String
	}
	rows, err := local.QueryContext(ctx, orchestratorsql.Q("modelstate/persist_markov_model_asset_to_turso_one_2"), m.symbol, since)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for rows.Next() {
		var date string
		var state int
		var bull, bear, signal float64
		if err := rows.Scan(&date, &state, &bull, &bear, &signal); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, orchestratorsql.Q("modelstate/persist_markov_model_asset_to_turso_one_3"),
			m.symbol, m.trainedAt, date, state, bull, bear, signal); err != nil {
			return 0, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	return n, tx.Commit()
}
