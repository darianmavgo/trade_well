package modelstate

// Everything in this file reads or writes the hidden Markov model ASSET: hmm_models.db, built in
// backtestgosqlite and shipped in the binary. The instance holds a copy in its working folder
// that the hmm strategies extend with one prediction per new session; the functions here carry
// those extension rows between that copy and Turso.

import (
	"context"
	"database/sql"
	"fmt"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	_ "modernc.org/sqlite"
)

type hmmModelAssetMeta struct {
	symbol, trainedAt, trainedThrough, lastDate string
}

func readHMMModelAssetMeta(local *sql.DB) ([]hmmModelAssetMeta, error) {
	rows, err := local.Query(orchestratorsql.Q("modelstate/read_hmmmodel_asset_meta"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hmmModelAssetMeta
	for rows.Next() {
		var m hmmModelAssetMeta
		if err := rows.Scan(&m.symbol, &m.trainedAt, &m.trainedThrough, &m.lastDate); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// HydrateHMMModelAssetFromTurso copies the stored predictions newer than the local model's last_date into the local
// model database at modelPath and moves last_date to the newest one. It returns how many
// sessions it added. A model with nothing newer stored is left alone.
func HydrateHMMModelAssetFromTurso(ctx context.Context, db *sql.DB, modelPath string) (int, error) {
	if err := ensureHMMSchema(db); err != nil {
		return 0, err
	}
	local, err := openModelAsset(modelPath)
	if err != nil {
		return 0, err
	}
	defer local.Close()
	ms, err := readHMMModelAssetMeta(local)
	if err != nil {
		return 0, fmt.Errorf("modelstate: read %s: %w", modelPath, err)
	}
	added := 0
	for _, m := range ms {
		n, err := hydrateHMMModelAssetFromTursoOne(ctx, db, local, m)
		if err != nil {
			return added, fmt.Errorf("modelstate: load %s predictions: %w", m.symbol, err)
		}
		added += n
	}
	return added, nil
}

func hydrateHMMModelAssetFromTursoOne(ctx context.Context, db, local *sql.DB, m hmmModelAssetMeta) (int, error) {
	preds, err := db.QueryContext(ctx, orchestratorsql.Q("modelstate/hydrate_hmmmodel_asset_from_turso_one"), m.symbol, m.trainedAt, m.lastDate)
	if err != nil {
		return 0, err
	}
	defer preds.Close()
	tx, err := local.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	last, n := "", 0
	for preds.Next() {
		var date string
		var ret, vol, regProb, nextProb, ll float64
		var reg, nextReg, inSample int
		if err := preds.Scan(&date, &ret, &vol, &reg, &regProb, &nextReg, &nextProb, &ll, &inSample); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, orchestratorsql.Q("modelstate/hydrate_hmmmodel_asset_from_turso_one_2"), m.symbol, date, ret, vol, reg, regProb, nextReg, nextProb, ll, inSample); err != nil {
			return 0, err
		}
		last = date
		n++
	}
	if err := preds.Err(); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	states, err := db.QueryContext(ctx, orchestratorsql.Q("modelstate/hydrate_hmmmodel_asset_from_turso_one_3"), m.symbol, m.trainedAt, m.lastDate)
	if err != nil {
		return 0, err
	}
	defer states.Close()
	for states.Next() {
		var date string
		var state int
		var prob, next float64
		if err := states.Scan(&date, &state, &prob, &next); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, orchestratorsql.Q("modelstate/hydrate_hmmmodel_asset_from_turso_one_4"),
			m.symbol, date, state, prob, next); err != nil {
			return 0, err
		}
	}
	if err := states.Err(); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, orchestratorsql.Q("modelstate/hydrate_hmmmodel_asset_from_turso_one_5"), last, m.symbol); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// PersistHMMModelAssetToTurso stores the predictions in the local model database at modelPath that Turso does not
// have yet: those newer than both the model's trained_through and the newest stored one. It
// returns how many sessions it stored.
func PersistHMMModelAssetToTurso(ctx context.Context, db *sql.DB, modelPath string) (int, error) {
	if err := ensureHMMSchema(db); err != nil {
		return 0, err
	}
	local, err := openModelAsset(modelPath)
	if err != nil {
		return 0, err
	}
	defer local.Close()
	ms, err := readHMMModelAssetMeta(local)
	if err != nil {
		return 0, fmt.Errorf("modelstate: read %s: %w", modelPath, err)
	}
	stored := 0
	for _, m := range ms {
		n, err := persistHMMModelAssetToTursoOne(ctx, db, local, m)
		if err != nil {
			return stored, fmt.Errorf("modelstate: store %s predictions: %w", m.symbol, err)
		}
		stored += n
	}
	return stored, nil
}

func persistHMMModelAssetToTursoOne(ctx context.Context, db, local *sql.DB, m hmmModelAssetMeta) (int, error) {
	var newest sql.NullString
	if err := db.QueryRowContext(ctx, orchestratorsql.Q("modelstate/persist_hmmmodel_asset_to_turso_one"),
		m.symbol, m.trainedAt).Scan(&newest); err != nil {
		return 0, err
	}
	since := m.trainedThrough
	if newest.Valid && newest.String > since {
		since = newest.String
	}
	preds, err := local.QueryContext(ctx, orchestratorsql.Q("modelstate/persist_hmmmodel_asset_to_turso_one_2"), m.symbol, since)
	if err != nil {
		return 0, err
	}
	defer preds.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for preds.Next() {
		var date string
		var ret, vol, regProb, nextProb, ll float64
		var reg, nextReg, inSample int
		if err := preds.Scan(&date, &ret, &vol, &reg, &regProb, &nextReg, &nextProb, &ll, &inSample); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, orchestratorsql.Q("modelstate/persist_hmmmodel_asset_to_turso_one_3"), m.symbol, m.trainedAt, date, ret, vol, reg, regProb, nextReg, nextProb, ll, inSample); err != nil {
			return 0, err
		}
		n++
	}
	if err := preds.Err(); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	states, err := local.QueryContext(ctx, orchestratorsql.Q("modelstate/persist_hmmmodel_asset_to_turso_one_4"), m.symbol, since)
	if err != nil {
		return 0, err
	}
	defer states.Close()
	for states.Next() {
		var date string
		var state int
		var prob, next float64
		if err := states.Scan(&date, &state, &prob, &next); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, orchestratorsql.Q("modelstate/persist_hmmmodel_asset_to_turso_one_5"), m.symbol, m.trainedAt, date, state, prob, next); err != nil {
			return 0, err
		}
	}
	if err := states.Err(); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// ReadHMMModelAssetSymbols lists the symbols with a saved hidden Markov model in the model database at modelPath.
func ReadHMMModelAssetSymbols(modelPath string) ([]string, error) {
	local, err := openModelAsset(modelPath)
	if err != nil {
		return nil, err
	}
	defer local.Close()
	ms, err := readHMMModelAssetMeta(local)
	if err != nil {
		return nil, fmt.Errorf("modelstate: read %s: %w", modelPath, err)
	}
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.symbol
	}
	return out, nil
}

// ReadHMMModelAssetTrainedAt returns the trained_at of symbol's model in the model database at modelPath: the key
// its Turso predictions are stored under.
func ReadHMMModelAssetTrainedAt(modelPath, symbol string) (string, error) {
	local, err := openModelAsset(modelPath)
	if err != nil {
		return "", err
	}
	defer local.Close()
	ms, err := readHMMModelAssetMeta(local)
	if err != nil {
		return "", fmt.Errorf("modelstate: read %s: %w", modelPath, err)
	}
	for _, m := range ms {
		if m.symbol == symbol {
			return m.trainedAt, nil
		}
	}
	return "", fmt.Errorf("modelstate: no %s model in %s", symbol, modelPath)
}
