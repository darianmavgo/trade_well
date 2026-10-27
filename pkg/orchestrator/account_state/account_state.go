// Package account_state mirrors Schwab account/position/order/transaction
// state into Turso (formerly the `schwabstate` job). It has 0
// business logic of its own beyond ingestion — it is the read model that
// pkg/stage_orders, pkg/manage_exits, and pkg/check_health depend on for
// current account/position/order state.
package account_state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/schwaber/accountstore"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// Result summarizes one Sync run.
type Result struct {
	AccountsFound     int
	OrdersFound       int
	TransactionsFound int
	Details           []string
}

// Table is one result table ready to stream as HTML.
type Table = accountstore.Table

// Event is one progress update from a Sync run: a status line, an error, or
// a populated table.
type Event struct {
	Status string
	Error  string
	Table  *Table
}

// tables.RawMirror() are exactly the tables Sync repopulates from the live
// Schwab API on every run. They track upstream broker state (not this
// orchestrator's own state — active_trades/strategy_signals/staged_orders/
// health_audits live in other packages) and are safe to fully reset each
// sync since the per-row upserts below only ever add or update rows.
func resetRawTables(db *sql.DB) error {
	if err := EnsureSchema(db); err != nil {
		return fmt.Errorf("failed to ensure schema: %w", err)
	}
	for _, t := range tables.RawMirror() {
		if _, err := db.Exec("DELETE FROM " + t); err != nil {
			if sqliteMissingTable(err) {
				if err := EnsureSchema(db); err != nil {
					return fmt.Errorf("failed to recreate schema after missing %s: %w", t, err)
				}
				continue
			}
			return fmt.Errorf("failed to clear %s: %w", t, err)
		}
	}
	return nil
}

func sqliteMissingTable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "does not exist")
}

const (
	schwabGetTimeout    = 45 * time.Second
	streamTableRowLimit = 200
)

type execer = accountstore.Execer

// store is the Schwab account mirror, in schwaber's accountstore.Mirror tables (account_state_*);
// store does all parsing and upserts.
var (
	tables = accountstore.Mirror
	store  = accountstore.New(tables)
)

// MirrorStore is the Schwab account mirror this process writes
// (account_state_order, account_state_transaction, and the rest).
func MirrorStore() *accountstore.Store { return store }

func accountTablesFromJSON(data []byte) (*Table, *Table) {
	acc, pos := store.ParseAccounts(accountstore.Discard, data, time.Time{})
	return accountstore.Cap(acc, streamTableRowLimit), accountstore.Cap(pos, streamTableRowLimit)
}

func orderTableFromJSON(accNum string, data []byte) *Table {
	return accountstore.Cap(store.ParseOrders(accountstore.Discard, accNum, data, time.Time{}), streamTableRowLimit)
}

// pendingOrderColumns are the columns of the pending-orders table.
var pendingOrderColumns = []string{"account_number", "symbol", "instruction", "order_id", "order_type", "status", "price", "stop_price", "quantity", "entered_time"}

func newPendingOrdersTable() *Table {
	return &Table{Name: "account_state_order_pending", Label: "Pending orders", Columns: pendingOrderColumns}
}

// addPendingOrders appends to t every order in one account's order list that is still live
// at the broker. Bracket children count: the take-profit and stop resting under a filled
// entry are pending orders too, and are not top-level rows of the Orders table.
// Wrapper orders (an OCO or TRIGGER parent) have no legs of their own and are skipped.
func addPendingOrders(t *Table, accNum string, data []byte) {
	var orders []schwab.Order
	if err := json.Unmarshal(data, &orders); err != nil {
		return
	}
	for _, o := range schwab.FlattenOrders(orders) {
		if !schwab.IsWorkingStatus(o.Status) {
			continue
		}
		legs, err := trader.ParseOrderLegs(&o)
		if err != nil || len(legs) == 0 {
			continue
		}
		var id, px, stop, entered string
		if o.OrderID != nil {
			id = fmt.Sprintf("%d", *o.OrderID)
		}
		if o.Price != nil {
			px = fmt.Sprintf("%.2f", *o.Price)
		}
		if o.StopPrice != nil {
			stop = fmt.Sprintf("%.2f", *o.StopPrice)
		}
		if o.EnteredTime != nil {
			entered = o.EnteredTime.Time.Format(time.RFC3339)
		}
		t.Rows = append(t.Rows, []string{accNum, legs[0].Instrument.Symbol, legs[0].Instruction, id,
			o.OrderType, strings.ToUpper(o.Status), px, stop, fmt.Sprintf("%g", legs[0].Quantity), entered})
	}
}

func transactionTableFromJSON(accNum string, data []byte) *Table {
	return accountstore.Cap(store.ParseTransactions(accountstore.Discard, accNum, data, time.Time{}), streamTableRowLimit)
}

func schwabGet(ctx context.Context, tc *trader.TraderClient, rawURL string) ([]byte, int, error) {
	c, cancel := context.WithTimeout(ctx, schwabGetTimeout)
	defer cancel()
	return tc.Client.Get(c, rawURL)
}

func persistTx(db *sql.DB, fn func(execer) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func emitEvent(res *Result, emit func(Event), ev Event) {
	if ev.Status == "" && ev.Error == "" && ev.Table == nil {
		return
	}
	if ev.Status != "" {
		res.Details = append(res.Details, ev.Status)
	}
	if ev.Error != "" {
		res.Details = append(res.Details, ev.Error)
	}
	if emit != nil {
		emit(ev)
	}
}

// Sync pulls current account/position/order/transaction state from Schwab
// and repopulates the raw mirror tables starting from a clean slate. targets
// must be non-empty (resolve via pkg/account_allowlist).
func Sync(ctx context.Context, db *sql.DB, tc *trader.TraderClient, targets []string) (*Result, error) {
	return SyncWithProgress(ctx, db, tc, targets, nil)
}

// SyncWithProgress is Sync plus a callback invoked as soon as each table is
// ready, so a caller streaming to HTTP can render each table as it arrives
// instead of waiting for the whole run.
func SyncWithProgress(ctx context.Context, db *sql.DB, tc *trader.TraderClient, targets []string, emit func(Event)) (*Result, error) {
	res := &Result{}
	if len(targets) == 0 {
		return res, fmt.Errorf("account_state sync requires at least one configured account")
	}

	now := time.Now().UTC()
	baseURL := tc.Client.BaseURL()

	// 1. Fetch Account Numbers & Hashes -- do this before wiping anything, so
	// a broken/expired auth session fails loudly against the *old* data
	// still intact rather than leaving the database empty for no reason.
	emitEvent(res, emit, Event{Status: "Fetching authorized account numbers & hash mappings..."})
	accNumsURL := fmt.Sprintf("%s/accounts/accountNumbers", baseURL)
	body, status, err := schwabGet(ctx, tc, accNumsURL)
	if err != nil {
		if schwab.IsPermanentAuthError(err) {
			return res, fmt.Errorf("authentication expired or invalid -- re-authenticate with `schwaber auth`: %w", err)
		}
		return res, fmt.Errorf("failed to fetch account numbers: %w", err)
	}
	var linked []schwab.LinkedAccount
	if err := json.Unmarshal(body, &linked); err != nil {
		return res, fmt.Errorf("failed to parse account numbers: %w", err)
	}

	selected, err := trader.FilterLinkedAccounts(linked, targets)
	if err != nil {
		return res, err
	}

	// Auth + targeting succeeded -- now it's safe to start fresh. Only the
	// configured accounts are rewritten; anything else that was in the raw
	// mirror is dropped so Sync doesn't accumulate untargeted accounts.
	emitEvent(res, emit, Event{Status: "Clearing previous raw Schwab data..."})
	if err := resetRawTables(db); err != nil {
		return res, fmt.Errorf("failed to reset raw tables before sync: %w", err)
	}

	saveRaw := emit == nil
	if saveRaw {
		store.SaveRawResponse(db, accNumsURL, "GET", status, "", body, now)
	}
	if err := persistTx(db, func(e execer) error {
		for _, item := range selected {
			if _, err := e.Exec("INSERT INTO "+tables.Account+` (account_number, hash_value, fetched_at)
				VALUES (?, ?, ?)
				ON CONFLICT(account_number) DO UPDATE SET hash_value=excluded.hash_value, fetched_at=excluded.fetched_at`,
				item.AccountNumber, item.HashValue, now); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return res, fmt.Errorf("failed to store account hashes: %w", err)
	}

	toTime := now.Format(time.RFC3339)
	fromTime := now.AddDate(0, 0, -60).Format(time.RFC3339)

	type fetchedAccount struct {
		acc    schwab.LinkedAccount
		url    string
		status int
		body   []byte
	}
	accountsTbl := &Table{
		Name:    "account_state_account",
		Label:   "Accounts",
		Columns: []string{"account_number", "account_type", "cash_balance", "available_funds", "buying_power", "liquidation_value"},
	}
	var fetched []fetchedAccount

	emitEvent(res, emit, Event{Status: "Fetching account details & positions..."})
	for _, acc := range selected {
		accURL := fmt.Sprintf("%s/accounts/%s?fields=positions", baseURL, acc.HashValue)
		body, status, err := schwabGet(ctx, tc, accURL)
		if err != nil {
			emitEvent(res, emit, Event{Error: fmt.Sprintf("ERROR fetching details for account %s: %v", acc.AccountNumber, err)})
			continue
		}
		accTbl, _ := accountTablesFromJSON(body)
		if accTbl != nil {
			accountsTbl.Rows = append(accountsTbl.Rows, accTbl.Rows...)
		}
		fetched = append(fetched, fetchedAccount{acc: acc, url: accURL, status: status, body: body})
	}
	accountstore.SortByColumn(accountsTbl, "account_number")
	emitEvent(res, emit, Event{Table: accountstore.Cap(accountsTbl, streamTableRowLimit)})
	for _, f := range fetched {
		if saveRaw {
			store.SaveRawResponse(db, f.url, "GET", f.status, f.acc.AccountNumber, f.body, now)
		}
		if err := persistTx(db, func(e execer) error {
			store.ParseAccounts(e, f.body, now)
			return nil
		}); err != nil {
			emitEvent(res, emit, Event{Error: fmt.Sprintf("ERROR saving details for account %s: %v", f.acc.AccountNumber, err)})
		}
	}

	// Orders are fetched and stored now but shown last: Positions (which needs them stored)
	// and Pending orders come first, because the full order history always fills the screen.
	pendingTbl := newPendingOrdersTable()
	var orderTables []*Table
	for _, acc := range selected {
		emitEvent(res, emit, Event{Status: fmt.Sprintf("Fetching orders (last 60 days) for %s...", acc.AccountNumber)})
		accOrdersURL := fmt.Sprintf("%s/accounts/%s/orders?fromEnteredTime=%s&toEnteredTime=%s",
			baseURL, acc.HashValue, url.QueryEscape(fromTime), url.QueryEscape(toTime))
		body, status, err = schwabGet(ctx, tc, accOrdersURL)
		if err != nil {
			emitEvent(res, emit, Event{Error: fmt.Sprintf("ERROR fetching orders for account %s: %v", acc.AccountNumber, err)})
		} else {
			orderTables = append(orderTables, orderTableFromJSON(acc.AccountNumber, body))
			addPendingOrders(pendingTbl, acc.AccountNumber, body)
			if saveRaw {
				store.SaveRawResponse(db, accOrdersURL, "GET", status, acc.AccountNumber, body, now)
			}
			if err := persistTx(db, func(e execer) error {
				store.ParseOrders(e, acc.AccountNumber, body, now)
				return nil
			}); err != nil {
				emitEvent(res, emit, Event{Error: fmt.Sprintf("ERROR saving orders for account %s: %v", acc.AccountNumber, err)})
			}
		}

	}

	// The planned exit date is calculated, not read from Schwab: the pipeline's own record of
	// each managed position (entry fill date plus the strategy's hold window).
	accountNumbers := make([]string, len(selected))
	for i, a := range selected {
		accountNumbers[i] = a.AccountNumber
	}
	if err := applyPlannedExitDates(db, accountNumbers); err != nil {
		emitEvent(res, emit, Event{Error: fmt.Sprintf("ERROR recording planned exit dates: %v", err)})
	}

	// Positions are shown only now, once their orders are stored too: each row carries the
	// take-profit and stop Schwab is actually holding (read from its working orders), which
	// cannot be known when the positions themselves arrive, plus the planned exit date.
	if tbl, err := store.PositionExitsTable(db); err != nil {
		emitEvent(res, emit, Event{Error: fmt.Sprintf("ERROR reading positions: %v", err)})
	} else {
		emitEvent(res, emit, Event{Table: accountstore.Cap(tbl, streamTableRowLimit)})
	}
	accountstore.SortByColumn(pendingTbl, "account_number")
	emitEvent(res, emit, Event{Table: accountstore.Cap(pendingTbl, streamTableRowLimit)})
	for _, t := range orderTables {
		emitEvent(res, emit, Event{Table: t})
	}

	for _, acc := range selected {
		emitEvent(res, emit, Event{Status: fmt.Sprintf("Fetching transactions for %s...", acc.AccountNumber)})
		txURL := fmt.Sprintf("%s/accounts/%s/transactions?startDate=%s&endDate=%s",
			baseURL, acc.HashValue, url.QueryEscape(fromTime), url.QueryEscape(toTime))
		body, status, err = schwabGet(ctx, tc, txURL)
		if err != nil {
			emitEvent(res, emit, Event{Error: fmt.Sprintf("ERROR fetching transactions for account %s: %v", acc.AccountNumber, err)})
		} else {
			emitEvent(res, emit, Event{Table: transactionTableFromJSON(acc.AccountNumber, body)})
			if saveRaw {
				store.SaveRawResponse(db, txURL, "GET", status, acc.AccountNumber, body, now)
			}
			if err := persistTx(db, func(e execer) error {
				store.ParseTransactions(e, acc.AccountNumber, body, now)
				return nil
			}); err != nil {
				emitEvent(res, emit, Event{Error: fmt.Sprintf("ERROR saving transactions for account %s: %v", acc.AccountNumber, err)})
			}
		}
	}

	res.AccountsFound = len(selected)
	_ = db.QueryRow(orchestratorsql.Q("account_state/sync_with_progress")).Scan(&res.OrdersFound)
	_ = db.QueryRow(orchestratorsql.Q("account_state/sync_with_progress_2")).Scan(&res.TransactionsFound)

	return res, nil
}

// applyPlannedExitDates writes each managed position's planned exit date onto
// account_state_position for the accounts just synced. Before the orchestrator has ever
// tracked a trade there is no manage_exit table, which just means no dates.
func applyPlannedExitDates(db *sql.DB, accounts []string) error {
	dates, err := manage_exits.PlannedExitDates(db)
	if err != nil {
		if !sqliteMissingTable(err) {
			return err
		}
		dates = nil
	}
	store.SetPositionExitDates(db, accounts, dates)
	return nil
}
