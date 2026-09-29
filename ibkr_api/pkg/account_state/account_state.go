// Package account_state reads live IBKR accounts and mirrors them into ibkr.db with
// schwaber's own accountstore, into the same account_state_* tables trade_orchestrator
// uses, so account_state_position looks identical whichever broker filled it.
package account_state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/darianmavgo/ibkr_api/pkg/ibkr"
	"github.com/darianmavgo/schwaber/pkg/accountstore"
)

var store = accountstore.New(accountstore.Tables{
	Raw: "account_state_raw_response", Account: "account_state_account", Position: "account_state_position",
	Order: "account_state_order", OrderLeg: "account_state_order_leg", Transaction: "account_state_transaction",
	Preference: "account_state_preference",
})

// Result summarizes one Run.
type Result struct {
	Accounts  int
	Positions int
	Orders    int
}

// Run fetches each account (balances and positions) from IBKR and writes them to db,
// replacing the previous mirror of the positions and accounts tables. Progress goes to out.
func Run(ctx context.Context, out io.Writer, db *sql.DB, client *ibkr.Client, accountIDs []string) (*Result, error) {
	if len(accountIDs) == 0 {
		linked, err := client.ListLinkedAccounts(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing accounts: %w", err)
		}
		for _, a := range linked {
			accountIDs = append(accountIDs, a.AccountNumber)
		}
	}
	if len(accountIDs) == 0 {
		return nil, fmt.Errorf("no IBKR accounts to read (set IBKR_ACCOUNT_ID)")
	}
	if err := store.EnsureSchema(db); err != nil {
		return nil, fmt.Errorf("ensure schema: %w", err)
	}
	now := time.Now().UTC()
	// Read everything first so a failure leaves the previous mirror untouched.
	var docs [][]byte
	for _, id := range accountIDs {
		acc, err := client.GetAccount(ctx, id, "positions")
		if err != nil {
			return nil, fmt.Errorf("account %s: %w", id, err)
		}
		docs = append(docs, acc.SecuritiesAccount)
	}
	// Orders too: their working take-profit and stop become the position's profit_taker
	// and stop_loss (accountstore does that from the order list, as for Schwab).
	var orderDocs [][]byte
	for _, id := range accountIDs {
		orders, err := client.ListAccountOrders(ctx, id, ibkr.OrderListRequest{})
		if err != nil {
			return nil, fmt.Errorf("orders for %s: %w", id, err)
		}
		doc, _ := json.Marshal(orders)
		orderDocs = append(orderDocs, doc)
	}
	for _, name := range []string{store.Tables().Account, store.Tables().Position, store.Tables().Order, store.Tables().OrderLeg} {
		if _, err := db.Exec("DELETE FROM " + name); err != nil {
			return nil, err
		}
	}
	res := &Result{}
	for i, sec := range docs {
		wrapped, _ := json.Marshal(map[string]json.RawMessage{"securitiesAccount": sec})
		_, pos := store.ParseAccounts(db, wrapped, now)
		res.Accounts++
		if pos != nil {
			res.Positions += len(pos.Rows)
		}
		store.ParseOrders(db, accountIDs[i], orderDocs[i], now)
		fmt.Fprintf(out, "account %s: written\n", accountIDs[i])
	}
	return res, nil
}
