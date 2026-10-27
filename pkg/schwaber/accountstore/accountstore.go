// Package accountstore mirrors Schwab account, position, order, transaction
// and user-preference API responses into SQLite. The parsing and upsert logic
// is shared by `schwaber listall` (which uses Default table names) and
// trade_orchestrator's account_state job (which uses its own account_state_*
// names via Tables), so the two can never drift apart.
package accountstore

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/schwaber/dbutil"
)

// Execer is the write surface the parsers need: *sql.DB, *sql.Tx, or a no-op
// (see Discard) when only the display Table is wanted.
type Execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type discard struct{}

func (discard) Exec(string, ...any) (sql.Result, error) { return nil, nil }

// Discard is an Execer that writes nothing, for building display tables from
// JSON without touching a database.
var Discard Execer = discard{}

// Tables names the SQLite tables a Store reads and writes.
type Tables struct {
	Raw, Account, Position, Order, OrderLeg, Transaction, Preference string
}

// Default is schwaber's own naming (the `schwaber listall` mirror).
var Default = Tables{
	Raw: "raw_responses", Account: "accounts", Position: "positions", Order: "orders",
	OrderLeg: "order_legs", Transaction: "transactions", Preference: "user_preferences",
}

// RawMirror lists the tables that mirror upstream broker state and may be
// wiped and repopulated on every sync (everything except preferences).
func (t Tables) RawMirror() []string {
	return []string{t.Raw, t.Account, t.Position, t.Order, t.OrderLeg, t.Transaction}
}

// Table is one result table ready to render (HTML or terminal).
type Table struct {
	Name    string
	Label   string
	Columns []string
	Rows    [][]string
}

// Store is the mirror for one set of table names.
type Store struct {
	t Tables
	r *strings.Replacer
}

// New returns a Store over t.
func New(t Tables) *Store {
	return &Store{t: t, r: strings.NewReplacer(
		"{raw}", t.Raw, "{account}", t.Account, "{position}", t.Position, "{order}", t.Order,
		"{orderleg}", t.OrderLeg, "{transaction}", t.Transaction, "{preference}", t.Preference)}
}

// Tables returns the table names this Store uses.
func (s *Store) Tables() Tables { return s.t }

func (s *Store) q(query string) string { return s.r.Replace(query) }

// EnsureSchema creates every table this Store owns if it does not exist.
func (s *Store) EnsureSchema(db Execer) error {
	_, err := db.Exec(s.q(`
	CREATE TABLE IF NOT EXISTS {raw} (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		endpoint TEXT NOT NULL,
		http_method TEXT NOT NULL,
		http_status INTEGER NOT NULL,
		account_number TEXT,
		payload TEXT NOT NULL,
		fetched_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS {account} (
		account_number TEXT PRIMARY KEY,
		hash_value TEXT,
		account_type TEXT,
		round_trips INTEGER,
		is_day_trader INTEGER,
		is_closing_only INTEGER,
		cash_balance REAL,
		available_funds REAL,
		buying_power REAL,
		liquidation_value REAL,
		raw_json TEXT,
		fetched_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS {position} (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		account_number TEXT NOT NULL,
		symbol TEXT NOT NULL,
		asset_type TEXT,
		cusip TEXT,
		long_quantity REAL,
		short_quantity REAL,
		average_price REAL,
		market_value REAL,
		current_day_pl REAL,
		current_day_pl_pct REAL,
		settled_long_qty REAL,
		settled_short_qty REAL,
		raw_json TEXT,
		fetched_at DATETIME NOT NULL,
		profit_taker REAL,
		stop_loss REAL,
		exit_date TEXT,
		UNIQUE(account_number, symbol, asset_type)
	);

	CREATE TABLE IF NOT EXISTS {order} (
		order_id TEXT PRIMARY KEY,
		account_number TEXT NOT NULL,
		order_type TEXT,
		session TEXT,
		duration TEXT,
		order_strategy_type TEXT,
		status TEXT,
		price REAL,
		stop_price REAL,
		quantity REAL,
		filled_quantity REAL,
		remaining_quantity REAL,
		entered_time TEXT,
		close_time TEXT,
		raw_json TEXT,
		fetched_at DATETIME NOT NULL,
		execution_price REAL
	);

	CREATE TABLE IF NOT EXISTS {orderleg} (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		order_id TEXT NOT NULL,
		leg_id INTEGER,
		instruction TEXT,
		quantity REAL,
		symbol TEXT,
		asset_type TEXT,
		raw_json TEXT
	);

	CREATE TABLE IF NOT EXISTS {transaction} (
		activity_id TEXT PRIMARY KEY,
		transaction_id TEXT,
		account_number TEXT NOT NULL,
		transaction_type TEXT,
		status TEXT,
		sub_account TEXT,
		trade_date TEXT,
		settlement_date TEXT,
		net_amount REAL,
		description TEXT,
		raw_json TEXT,
		fetched_at DATETIME NOT NULL,
		fees REAL NOT NULL DEFAULT 0,
		symbol TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS {preference} (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		streamer_socket_url TEXT,
		schwab_client_correl_id TEXT,
		schwab_client_channel TEXT,
		schwab_client_function_id TEXT,
		raw_json TEXT,
		fetched_at DATETIME NOT NULL
	);
	`))
	if err != nil {
		return err
	}
	// A positions table created before profit_taker/stop_loss/exit_date existed gains them
	// (CREATE TABLE IF NOT EXISTS leaves an existing table as it is). Needs a *sql.DB
	// or *sql.Tx; the display-only Discard has nothing to migrate.
	if c, ok := db.(dbutil.Conn); ok {
		for _, col := range []struct{ name, decl string }{{"profit_taker", "REAL"}, {"stop_loss", "REAL"}, {"exit_date", "TEXT"}} {
			if err := dbutil.AddColumn(c, s.t.Position, col.name, col.decl); err != nil {
				return err
			}
		}
		if err := dbutil.AddColumn(c, s.t.Order, "execution_price", "REAL"); err != nil {
			return err
		}
		for _, col := range []struct{ name, decl string }{{"fees", "REAL NOT NULL DEFAULT 0"}, {"symbol", "TEXT NOT NULL DEFAULT ''"}} {
			if err := dbutil.AddColumn(c, s.t.Transaction, col.name, col.decl); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) SaveRawResponse(db Execer, endpoint, method string, status int, accNum string, payload []byte, fetchedAt time.Time) {
	_, _ = db.Exec(s.q(`INSERT INTO {raw} (endpoint, http_method, http_status, account_number, payload, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?)`), endpoint, method, status, accNum, string(payload), fetchedAt)
}

func (s *Store) ParseAccounts(db Execer, data []byte, now time.Time) (accountsTbl, positionsTbl *Table) {
	var list []map[string]interface{}
	if err := json.Unmarshal(data, &list); err != nil {
		var single map[string]interface{}
		if errSingle := json.Unmarshal(data, &single); errSingle == nil {
			list = append(list, single)
		} else {
			return nil, nil
		}
	}

	accountsTbl = &Table{
		Name:    s.t.Account,
		Label:   "Accounts",
		Columns: []string{"account_number", "account_type", "cash_balance", "available_funds", "buying_power", "liquidation_value"},
	}
	positionsTbl = &Table{
		Name:    s.t.Position,
		Label:   "Positions",
		Columns: []string{"account_number", "symbol", "asset_type", "long_quantity", "short_quantity", "average_price", "market_value", "current_day_pl"},
	}

	for _, item := range list {
		secMap, ok := item["securitiesAccount"].(map[string]interface{})
		if !ok {
			secMap = item
		}

		accNum := idFrom(secMap["accountNumber"])
		if accNum == "" {
			continue
		}
		accType, _ := secMap["type"].(string)
		roundTrips, _ := secMap["roundTrips"].(float64)
		isDayTrader, _ := secMap["isDayTrader"].(bool)
		isClosingOnly, _ := secMap["isClosingOnlyRestricted"].(bool)

		var cashBal, availFunds, buyingPower, liqVal float64
		if curr, ok := secMap["currentBalances"].(map[string]interface{}); ok {
			cashBal, _ = curr["cashBalance"].(float64)
			availFunds, _ = curr["availableFunds"].(float64)
			buyingPower, _ = curr["buyingPower"].(float64)
			liqVal, _ = curr["liquidationValue"].(float64)
		}

		rawJSON, _ := json.Marshal(secMap)

		_, _ = db.Exec(s.q(`INSERT INTO {account} (account_number, account_type, round_trips, is_day_trader, is_closing_only,
			cash_balance, available_funds, buying_power, liquidation_value, raw_json, fetched_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(account_number) DO UPDATE SET
				account_type=excluded.account_type,
				round_trips=excluded.round_trips,
				is_day_trader=excluded.is_day_trader,
				is_closing_only=excluded.is_closing_only,
				cash_balance=excluded.cash_balance,
				available_funds=excluded.available_funds,
				buying_power=excluded.buying_power,
				liquidation_value=excluded.liquidation_value,
				raw_json=excluded.raw_json,
				fetched_at=excluded.fetched_at`),
			accNum, accType, int(roundTrips), boolToInt(isDayTrader), boolToInt(isClosingOnly),
			cashBal, availFunds, buyingPower, liqVal, string(rawJSON), now)

		accountsTbl.Label = "Account " + accNum
		accountsTbl.Rows = append(accountsTbl.Rows, []string{
			accNum, accType, fmt.Sprintf("%.2f", cashBal), fmt.Sprintf("%.2f", availFunds),
			fmt.Sprintf("%.2f", buyingPower), fmt.Sprintf("%.2f", liqVal),
		})

		// Parse Positions
		if posList, ok := secMap["positions"].([]interface{}); ok {
			for _, p := range posList {
				posMap, ok := p.(map[string]interface{})
				if !ok {
					continue
				}

				var symbol, assetType, cusip string
				if inst, ok := posMap["instrument"].(map[string]interface{}); ok {
					symbol, _ = inst["symbol"].(string)
					assetType, _ = inst["assetType"].(string)
					cusip, _ = inst["cusip"].(string)
				}

				longQty, _ := posMap["longQuantity"].(float64)
				shortQty, _ := posMap["shortQuantity"].(float64)
				avgPrice, _ := posMap["averagePrice"].(float64)
				marketVal, _ := posMap["marketValue"].(float64)
				dayPL, _ := posMap["currentDayProfitLoss"].(float64)
				dayPLPct, _ := posMap["currentDayProfitLossPercentage"].(float64)
				settledLong, _ := posMap["settledLongQuantity"].(float64)
				settledShort, _ := posMap["settledShortQuantity"].(float64)
				posRaw, _ := json.Marshal(posMap)

				_, _ = db.Exec(s.q(`INSERT INTO {position} (account_number, symbol, asset_type, cusip, long_quantity, short_quantity,
					average_price, market_value, current_day_pl, current_day_pl_pct, settled_long_qty, settled_short_qty, raw_json, fetched_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT(account_number, symbol, asset_type) DO UPDATE SET
						cusip=excluded.cusip,
						long_quantity=excluded.long_quantity,
						short_quantity=excluded.short_quantity,
						average_price=excluded.average_price,
						market_value=excluded.market_value,
						current_day_pl=excluded.current_day_pl,
						current_day_pl_pct=excluded.current_day_pl_pct,
						settled_long_qty=excluded.settled_long_qty,
						settled_short_qty=excluded.settled_short_qty,
						raw_json=excluded.raw_json,
						fetched_at=excluded.fetched_at`),
					accNum, symbol, assetType, cusip, longQty, shortQty, avgPrice, marketVal, dayPL, dayPLPct, settledLong, settledShort, string(posRaw), now)

				positionsTbl.Label = "Positions " + accNum
				positionsTbl.Rows = append(positionsTbl.Rows, []string{
					accNum, symbol, assetType,
					fmt.Sprintf("%g", longQty), fmt.Sprintf("%g", shortQty),
					fmt.Sprintf("%.4f", avgPrice), fmt.Sprintf("%.2f", marketVal), fmt.Sprintf("%.2f", dayPL),
				})
			}
		}
	}
	return accountsTbl, positionsTbl
}

func (s *Store) ParseOrders(db Execer, fallbackAcc string, data []byte, now time.Time) *Table {
	tbl := &Table{
		Name:    s.t.Order,
		Label:   "Orders " + fallbackAcc,
		Columns: []string{"symbol", "instruction", "order_id", "account_number", "order_type", "status", "price", "quantity", "filled_quantity", "entered_time"},
	}
	var list []map[string]interface{}
	if err := json.Unmarshal(data, &list); err != nil {
		return tbl
	}
	found := map[string]map[string]*exitPrices{fallbackAcc: {}} // account -> symbol -> resting exits

	for _, ord := range list {
		ordID := idFrom(ord["orderId"])
		if ordID == "" || ordID == "<nil>" {
			continue
		}
		accNum := fallbackAcc
		if s := idFrom(ord["accountNumber"]); s != "" {
			accNum = s
		}
		if found[accNum] == nil {
			found[accNum] = map[string]*exitPrices{}
		}
		collectExits(ord, found[accNum])

		orderType, _ := ord["orderType"].(string)
		session, _ := ord["session"].(string)
		duration, _ := ord["duration"].(string)
		strategyType, _ := ord["orderStrategyType"].(string)
		status, _ := ord["status"].(string)
		price, _ := ord["price"].(float64)
		stopPrice, _ := ord["stopPrice"].(float64)
		qty, _ := ord["quantity"].(float64)
		filledQty, _ := ord["filledQuantity"].(float64)
		remainingQty, _ := ord["remainingQuantity"].(float64)
		enteredTime, _ := ord["enteredTime"].(string)
		closeTime, _ := ord["closeTime"].(string)
		rawJSON, _ := json.Marshal(ord)
		execPx, _, _ := FillPrice(ord, "")

		_, _ = db.Exec(s.q(`INSERT INTO {order} (order_id, account_number, order_type, session, duration, order_strategy_type,
			status, price, stop_price, quantity, filled_quantity, remaining_quantity, entered_time, close_time, raw_json, fetched_at, execution_price)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(order_id) DO UPDATE SET
				status=excluded.status,
				filled_quantity=excluded.filled_quantity,
				remaining_quantity=excluded.remaining_quantity,
				close_time=excluded.close_time,
				raw_json=excluded.raw_json,
				fetched_at=excluded.fetched_at,
				execution_price=excluded.execution_price`),
			ordID, accNum, orderType, session, duration, strategyType, status, price, stopPrice, qty, filledQty, remainingQty, enteredTime, closeTime, string(rawJSON), now, execPx)

		var symbols, instructions []string
		seenSym, seenInst := map[string]bool{}, map[string]bool{}
		if legs, ok := ord["orderLegCollection"].([]interface{}); ok {
			for _, l := range legs {
				legMap, ok := l.(map[string]interface{})
				if !ok {
					continue
				}
				legID, _ := legMap["legId"].(float64)
				instruction, _ := legMap["instruction"].(string)
				legQty, _ := legMap["quantity"].(float64)
				var symbol, assetType string
				if inst, ok := legMap["instrument"].(map[string]interface{}); ok {
					symbol, _ = inst["symbol"].(string)
					assetType, _ = inst["assetType"].(string)
				}
				legRaw, _ := json.Marshal(legMap)

				_, _ = db.Exec(s.q(`INSERT INTO {orderleg} (order_id, leg_id, instruction, quantity, symbol, asset_type, raw_json)
					VALUES (?, ?, ?, ?, ?, ?, ?)`), ordID, int(legID), instruction, legQty, symbol, assetType, string(legRaw))

				if symbol != "" && !seenSym[symbol] {
					seenSym[symbol] = true
					symbols = append(symbols, symbol)
				}
				if instruction != "" && !seenInst[instruction] {
					seenInst[instruction] = true
					instructions = append(instructions, instruction)
				}
			}
		}

		tbl.Rows = append(tbl.Rows, []string{
			strings.Join(symbols, ","), strings.Join(instructions, ","),
			ordID, accNum, orderType, status,
			fmt.Sprintf("%v", price), fmt.Sprintf("%g", qty), fmt.Sprintf("%g", filledQty), enteredTime,
		})
	}
	s.applyExits(db, found)
	return tbl
}

func (s *Store) ParseTransactions(db Execer, accNum string, data []byte, now time.Time) *Table {
	tbl := &Table{
		Name:    s.t.Transaction,
		Label:   "Transactions " + accNum,
		Columns: []string{"activity_id", "account_number", "transaction_type", "status", "net_amount", "trade_date", "description"},
	}
	var list []map[string]interface{}
	if err := json.Unmarshal(data, &list); err != nil {
		return tbl
	}

	for _, tx := range list {
		actID := idFrom(tx["activityId"])
		if actID == "" || actID == "<nil>" {
			continue
		}
		txID := idFrom(tx["transactionId"])
		txType, _ := tx["type"].(string)
		status, _ := tx["status"].(string)
		subAcc, _ := tx["subAccount"].(string)
		tradeDate, _ := tx["tradeDate"].(string)
		settleDate, _ := tx["settlementDate"].(string)
		netAmt, _ := tx["netAmount"].(float64)
		desc, _ := tx["description"].(string)
		rawJSON, _ := json.Marshal(tx)
		fees, symbol := FeeAndSymbol(tx)

		_, _ = db.Exec(s.q(`INSERT INTO {transaction} (activity_id, transaction_id, account_number, transaction_type,
			status, sub_account, trade_date, settlement_date, net_amount, description, raw_json, fetched_at, fees, symbol)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(activity_id) DO UPDATE SET
				status=excluded.status,
				net_amount=excluded.net_amount,
				raw_json=excluded.raw_json,
				fetched_at=excluded.fetched_at,
				fees=excluded.fees,
				symbol=excluded.symbol`),
			actID, txID, accNum, txType, status, subAcc, tradeDate, settleDate, netAmt, desc, string(rawJSON), now, fees, symbol)

		tbl.Rows = append(tbl.Rows, []string{
			actID, accNum, txType, status, fmt.Sprintf("%.2f", netAmt), tradeDate, desc,
		})
	}
	return tbl
}

func (s *Store) ParseUserPreferences(db Execer, data []byte, now time.Time) {
	var pref map[string]interface{}
	if err := json.Unmarshal(data, &pref); err != nil {
		var prefList []map[string]interface{}
		if errList := json.Unmarshal(data, &prefList); errList == nil && len(prefList) > 0 {
			pref = prefList[0]
		} else {
			return
		}
	}

	var streamURL, correlID, channel, funcID string
	if streamInfo, ok := pref["streamerInfo"].([]interface{}); ok && len(streamInfo) > 0 {
		if sm, ok := streamInfo[0].(map[string]interface{}); ok {
			streamURL, _ = sm["streamerSocketUrl"].(string)
			correlID, _ = sm["schwabClientCorrelId"].(string)
			channel, _ = sm["schwabClientChannel"].(string)
			funcID, _ = sm["schwabClientFunctionId"].(string)
		}
	}

	rawJSON, _ := json.Marshal(pref)
	_, _ = db.Exec(s.q(`INSERT INTO {preference} (streamer_socket_url, schwab_client_correl_id, schwab_client_channel, schwab_client_function_id, raw_json, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?)`), streamURL, correlID, channel, funcID, string(rawJSON), now)
}

// idFrom stringifies a JSON-decoded ID field (order/activity/transaction/
// numeric IDs (e.g. orderId 1007974038446) are large enough that Go's
// default %v/%g formatting on the float64 encoding/json produces for a bare
// JSON number renders as "1.007974038446e+12" -- not just wrong for display,
// but wrong for the PRIMARY KEY string stored in the order table's order_id
// / the transaction table's activity_id, so this must run before the value
// ever reaches either a display row or a database write.
func idFrom(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return fmt.Sprintf("%.0f", t)
	case json.Number:
		return t.String()
	default:
		if v == nil {
			return ""
		}
		s := strings.TrimSpace(fmt.Sprintf("%v", v))
		if s == "" || s == "<nil>" {
			return ""
		}
		return s
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SortByColumn sorts t's rows (stably) by the named column, if present.
func SortByColumn(t *Table, col string) {
	if t == nil || len(t.Rows) < 2 {
		return
	}
	idx := -1
	for i, c := range t.Columns {
		if c == col {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	sort.SliceStable(t.Rows, func(i, j int) bool {
		a, b := "", ""
		if idx < len(t.Rows[i]) {
			a = t.Rows[i][idx]
		}
		if idx < len(t.Rows[j]) {
			b = t.Rows[j][idx]
		}
		return a < b
	})
}

// Cap truncates t to n rows, noting the truncation in its label.
func Cap(t *Table, n int) *Table {
	if t == nil || len(t.Rows) <= n {
		return t
	}
	t.Label = fmt.Sprintf("%s (showing %d of %d)", t.Label, n, len(t.Rows))
	t.Rows = t.Rows[:n]
	return t
}

// TableCount is one table's row count with a human label.
type TableCount struct {
	Label string
	Count int
}

// AccountRow is one row of the accounts breakdown.
type AccountRow struct {
	AccountNumber    string
	AccountType      string
	CashBalance      float64
	LiquidationValue float64
}

// Report is the ingestion summary: row counts per table plus saved accounts.
type Report struct {
	TableCounts []TableCount
	Accounts    []AccountRow
}

// Summary builds a Report of the current contents of this Store's tables.
func (s *Store) Summary(db *sql.DB) (Report, error) {
	rep := Report{}
	labeled := []struct{ table, label string }{
		{s.t.Account, "Accounts"},
		{s.t.Position, "Open Positions"},
		{s.t.Order, "Recent Orders"},
		{s.t.OrderLeg, "Order Legs"},
		{s.t.Transaction, "Transactions"},
		{s.t.Preference, "User Preferences"},
		{s.t.Raw, "Raw API Responses (Full Audit)"},
	}
	for _, l := range labeled {
		var count int
		_ = db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", l.table)).Scan(&count)
		rep.TableCounts = append(rep.TableCounts, TableCount{Label: l.label, Count: count})
	}

	rows, err := db.Query(fmt.Sprintf(`SELECT account_number, COALESCE(account_type, 'UNKNOWN'), cash_balance, liquidation_value FROM %s`, s.t.Account))
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var r AccountRow
			if err := rows.Scan(&r.AccountNumber, &r.AccountType, &r.CashBalance, &r.LiquidationValue); err == nil {
				rep.Accounts = append(rep.Accounts, r)
			}
		}
	}
	return rep, nil
}
