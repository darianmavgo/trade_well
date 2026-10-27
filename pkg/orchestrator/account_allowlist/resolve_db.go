package account_allowlist

import (
	"database/sql"
	"fmt"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// ResolveFromDB loads one account (+ positions) from the Turso account_state mirror
// (pkg/account_state's account_state_account/account_state_position tables).
// Missing rows fail — never invent equity.
func ResolveFromDB(db *sql.DB, targetAcc string) (*trader.ResolvedAccount, error) {
	if targetAcc == "" {
		return nil, fmt.Errorf("no Schwab account number given: pass --account (the configured accounts are in %s)", EnvAccountNumbers)
	}

	var accNum, hashVal, accType string
	var cash, liqVal float64
	err := db.QueryRow(orchestratorsql.Q("account_allowlist/resolve_from_db"), targetAcc).Scan(&accNum, &hashVal, &accType, &cash, &liqVal)
	if err != nil {
		return nil, fmt.Errorf("account %s not found in the Turso account_state mirror (run `orchestrator account_state` first): %w", targetAcc, err)
	}

	rows, err := db.Query(orchestratorsql.Q("account_allowlist/resolve_from_db_2"), accNum)
	var positions []trader.Position
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var sym, assetType string
			var longQty, shortQty, avgP, mktVal float64
			if errScan := rows.Scan(&sym, &assetType, &longQty, &shortQty, &avgP, &mktVal); errScan == nil {
				positions = append(positions, trader.Position{
					LongQuantity:  longQty,
					ShortQuantity: shortQty,
					AveragePrice:  avgP,
					MarketValue:   mktVal,
					Instrument:    trader.PositionInstrument{Symbol: sym, AssetType: assetType},
				})
			}
		}
	}

	return &trader.ResolvedAccount{
		AccountNumber: accNum,
		HashValue:     hashVal,
		Details: trader.SecuritiesAccount{
			AccountNumber: accNum,
			Type:          accType,
			CurrentBalances: trader.Balances{
				CashBalance:      cash,
				LiquidationValue: liqVal,
				BuyingPower:      cash,
			},
			Positions: positions,
		},
	}, nil
}

// ResolveAllFromDB resolves every target from the Turso account_state mirror; any miss fails the batch.
func ResolveAllFromDB(db *sql.DB, targets []string) ([]*trader.ResolvedAccount, error) {
	var resolved []*trader.ResolvedAccount
	var failures []string
	for _, target := range targets {
		acc, err := ResolveFromDB(db, target)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", target, err))
			continue
		}
		resolved = append(resolved, acc)
	}
	if len(failures) > 0 {
		return nil, fmt.Errorf("failed to resolve %d of %d account(s) from Turso (run `orchestrator account_state` first):\n  - %s",
			len(failures), len(targets), joinNL(failures))
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("no accounts resolved")
	}
	return resolved, nil
}

func joinNL(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\n  - "
		}
		out += p
	}
	return out
}
