package account_state

import (
	"database/sql"

	"github.com/darianmavgo/trade_well/pkg/schwaber/accountstore"
)

// Report is everything the CLI's ingestion-summary output needs.
type Report = accountstore.Report

// Summary builds a Report of the current account_state tables' contents.
func Summary(db *sql.DB) (Report, error) {
	return store.Summary(db)
}
