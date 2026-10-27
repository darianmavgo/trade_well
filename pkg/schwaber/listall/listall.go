// Package listall is the `schwaber listall` command: read everything from all
// authorized Schwab accounts (account numbers, balances, positions, orders,
// transactions, preferences) and save it into the shared Turso database.
package listall

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/darianmavgo/trade_well/pkg/schwaber/accountstore"
	"github.com/darianmavgo/trade_well/pkg/schwaber/clicolor"
	"github.com/darianmavgo/trade_well/pkg/schwaber/config"
	"github.com/darianmavgo/trade_well/pkg/schwaber/dbutil"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// AccountNumberHash is one entry of Schwab's accountNumbers response.
type AccountNumberHash struct {
	AccountNumber string `json:"accountNumber"`
	HashValue     string `json:"hashValue"`
}

// store is the shared Schwab-state mirror using the default (schwaber) table names.
var store = accountstore.New(accountstore.Mirror)

func initDatabase() (*sql.DB, error) {
	db, err := dbutil.Open("")
	if err != nil {
		return nil, err
	}
	if err := store.EnsureSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to init schema: %w", err)
	}
	return db, nil
}

// Run ingests the full Schwab data set into the shared database, printing progress to
// out. Failures are printed to out and returned so the caller can set the exit
// code.
func Run(ctx context.Context, out io.Writer, cfg *config.Config) error {
	fmt.Fprintf(out, "\n%s%s=== schwaber listall: Ingesting Complete Charles Schwab Data ===%s\n", clicolor.Bold, clicolor.Cyan, clicolor.Reset)

	tc, err := trader.NewTraderClient(ctx, cfg)
	if err != nil {
		fmt.Fprintf(out, "%s[Error]%s Failed to initialize Schwab client: %v\n", clicolor.Red, clicolor.Reset, err)
		return err
	}

	db, err := initDatabase()
	if err != nil {
		fmt.Fprintf(out, "%s[Error]%s Failed to initialize database: %v\n", clicolor.Red, clicolor.Reset, err)
		return err
	}
	defer db.Close()

	now := time.Now().UTC()
	baseURL := tc.Client.BaseURL()

	// 1. Fetch Account Numbers & Hashes
	fmt.Fprintf(out, "%s[1/5]%s Fetching authorized account numbers & hash mappings...\n", clicolor.Yellow, clicolor.Reset)
	accNumsURL := fmt.Sprintf("%s/accounts/accountNumbers", baseURL)
	if err := tc.Client.EnsureFreshToken(ctx); err != nil {
		fmt.Fprintf(out, "  %sToken refresh:%s %v\n", clicolor.Red, clicolor.Reset, err)
		fmt.Fprintf(out, "\n%s[Fatal]%s Access token could not be refreshed. The refresh token is dead (revoked/expired) or missing.\n", clicolor.Red, clicolor.Reset)
		fmt.Fprintf(out, "Re-authenticate: %sschwaber auth%s\n", clicolor.Cyan, clicolor.Reset)
		return err
	}
	body, status, err := tc.Client.Get(ctx, accNumsURL)
	if err != nil {
		fmt.Fprintf(out, "  %sError fetching %s:%s %v\n", clicolor.Red, accNumsURL, clicolor.Reset, err)
		if schwab.IsPermanentAuthError(err) {
			fmt.Fprintf(out, "\n%s[Fatal]%s Your authentication session has expired or is invalid.\n", clicolor.Red, clicolor.Reset)
			fmt.Fprintf(out, "Please re-authenticate by running: %sschwaber auth%s\n", clicolor.Cyan, clicolor.Reset)
		}
		return err
	}
	store.SaveRawResponse(db, accNumsURL, "GET", status, "", body, now)
	var list []AccountNumberHash
	if err := json.Unmarshal(body, &list); err == nil {
		fmt.Fprintf(out, "  %s✓ Discovered %d authorized account(s)%s\n", clicolor.Green, len(list), clicolor.Reset)
		for _, item := range list {
			_, _ = db.Exec(`INSERT INTO account_state_account (account_number, hash_value, fetched_at)
				VALUES (?, ?, ?)
				ON CONFLICT(account_number) DO UPDATE SET hash_value=excluded.hash_value, fetched_at=excluded.fetched_at`,
				item.AccountNumber, item.HashValue, now)
		}
	} else {
		fmt.Fprintf(out, "  %sError parsing account numbers:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return err
	}

	// 2. Fetch Accounts & Balances & Positions
	fmt.Fprintf(out, "%s[2/5]%s Fetching account details & positions...\n", clicolor.Yellow, clicolor.Reset)
	accountsURL := fmt.Sprintf("%s/accounts?fields=positions", baseURL)
	body, status, err = tc.Client.Get(ctx, accountsURL)
	if err != nil {
		fmt.Fprintf(out, "  %sError fetching %s:%s %v\n", clicolor.Red, accountsURL, clicolor.Reset, err)
		return err
	}
	store.SaveRawResponse(db, accountsURL, "GET", status, "", body, now)
	store.ParseAccounts(db, body, now)

	// Load all known accounts from DB to query account-specific endpoints
	rows, err := db.Query("SELECT account_number, hash_value FROM account_state_account WHERE hash_value IS NOT NULL AND hash_value != ''")
	var accounts []AccountNumberHash
	if err == nil {
		for rows.Next() {
			var a AccountNumberHash
			if err := rows.Scan(&a.AccountNumber, &a.HashValue); err == nil {
				accounts = append(accounts, a)
			}
		}
		rows.Close()
	}

	// Date range for orders & transactions (last 60 days to now)
	toTime := now.Format(time.RFC3339)
	fromTime := now.AddDate(0, 0, -60).Format(time.RFC3339)

	// 3. Fetch Orders (Global and Per-Account)
	fmt.Fprintf(out, "%s[3/5]%s Fetching order history (last 60 days)...\n", clicolor.Yellow, clicolor.Reset)
	globalOrdersURL := fmt.Sprintf("%s/orders?fromEnteredTime=%s&toEnteredTime=%s",
		baseURL, url.QueryEscape(fromTime), url.QueryEscape(toTime))
	body, status, err = tc.Client.Get(ctx, globalOrdersURL)
	if err != nil {
		fmt.Fprintf(out, "  %sError fetching %s:%s %v\n", clicolor.Red, globalOrdersURL, clicolor.Reset, err)
	} else {
		store.SaveRawResponse(db, globalOrdersURL, "GET", status, "GLOBAL", body, now)
		store.ParseOrders(db, "GLOBAL", body, now)
	}

	for _, acc := range accounts {
		accOrdersURL := fmt.Sprintf("%s/accounts/%s/orders?fromEnteredTime=%s&toEnteredTime=%s",
			baseURL, acc.HashValue, url.QueryEscape(fromTime), url.QueryEscape(toTime))
		body, status, err := tc.Client.Get(ctx, accOrdersURL)
		if err != nil {
			fmt.Fprintf(out, "  %sError fetching orders for account %s:%s %v\n", clicolor.Red, acc.AccountNumber, clicolor.Reset, err)
		} else {
			store.SaveRawResponse(db, accOrdersURL, "GET", status, acc.AccountNumber, body, now)
			store.ParseOrders(db, acc.AccountNumber, body, now)
		}
	}

	// 4. Fetch Transactions (Per-Account)
	fmt.Fprintf(out, "%s[4/5]%s Fetching account transactions...\n", clicolor.Yellow, clicolor.Reset)
	for _, acc := range accounts {
		txURL := fmt.Sprintf("%s/accounts/%s/transactions?startDate=%s&endDate=%s",
			baseURL, acc.HashValue, url.QueryEscape(fromTime), url.QueryEscape(toTime))
		body, status, err := tc.Client.Get(ctx, txURL)
		if err != nil {
			fmt.Fprintf(out, "  %sError fetching transactions for account %s:%s %v\n", clicolor.Red, acc.AccountNumber, clicolor.Reset, err)
		} else {
			store.SaveRawResponse(db, txURL, "GET", status, acc.AccountNumber, body, now)
			store.ParseTransactions(db, acc.AccountNumber, body, now)
		}
	}

	// 5. Fetch User Preferences & Streamer Info
	fmt.Fprintf(out, "%s[5/5]%s Fetching user preferences & WebSocket streamer config...\n", clicolor.Yellow, clicolor.Reset)
	prefURL := fmt.Sprintf("%s/userPreference", baseURL)
	body, status, err = tc.Client.Get(ctx, prefURL)
	if err != nil {
		fmt.Fprintf(out, "  %sError fetching %s:%s %v\n", clicolor.Red, prefURL, clicolor.Reset, err)
	} else {
		store.SaveRawResponse(db, prefURL, "GET", status, "", body, now)
		store.ParseUserPreferences(db, body, now)
	}

	// Print Summary
	printSummaryReport(out, db)
	return nil
}

func printSummaryReport(out io.Writer, db *sql.DB) {
	rep, _ := store.Summary(db)
	fmt.Fprintf(out, "\n%s%s=== schwaber Ingestion Summary (Stored in Turso) ===%s\n", clicolor.Bold, clicolor.Green, clicolor.Reset)
	for _, c := range rep.TableCounts {
		fmt.Fprintf(out, "  • %-32s : %s%d records%s\n", c.Label, clicolor.Bold, c.Count, clicolor.Reset)
	}

	if len(rep.Accounts) > 0 {
		fmt.Fprintf(out, "\n%sAccounts Saved in DB:%s\n", clicolor.Bold, clicolor.Reset)
		fmt.Fprintf(out, "  %-12s | %-10s | %16s | %16s\n", "Account #", "Type", "Cash Balance", "Total Value")
		fmt.Fprintln(out, "  ----------------------------------------------------------------")
		for _, a := range rep.Accounts {
			fmt.Fprintf(out, "  %-12s | %-10s | %16s | %16s\n",
				a.AccountNumber, a.AccountType, fmt.Sprintf("$%.2f", a.CashBalance), fmt.Sprintf("$%.2f", a.LiquidationValue))
		}
	}

	fmt.Fprintf(out, "\n%s✓ Complete data dump successfully saved to Turso%s\n\n", clicolor.Green, clicolor.Reset)
}
