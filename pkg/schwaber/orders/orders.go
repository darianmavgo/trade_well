// Package orders holds the `schwaber` order commands: buy, sell, bracket,
// orders (history) and cancel. Each takes an output writer (and, where it
// prompts, an input reader) and never exits the process: failures are printed
// to out and returned as clicolor.ErrReported; the caller decides the exit code.
package orders

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/schwaber/clicolor"
	"github.com/darianmavgo/trade_well/pkg/schwaber/config"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// PlaceOptions are the inputs to Place (buy / sell).
type PlaceOptions struct {
	Instruction string  // trader.InstructionBuy or trader.InstructionSell
	Symbol      string  // ticker; upper-cased here
	Qty         float64 // shares
	Price       float64 // limit price; 0 = market order
	DryRun      bool    // preview only
	Live        bool    // submit for real (otherwise cfg.DryRunDefault decides)
	Yes         bool    // skip the confirmation prompt
}

// BracketOptions are the inputs to Bracket.
type BracketOptions struct {
	Symbol string  // ticker; upper-cased here
	Qty    float64 // shares
	Entry  float64 // entry limit price
	Profit float64 // take-profit price; <=0 = entry +10%
	Stop   float64 // stop-loss price; <=0 = entry -5%
	DryRun bool
	Live   bool
	Yes    bool
}

// Bracket places (or previews) a 1st-Triggers-OCO bracket order: entry,
// take-profit and stop-loss.
func Bracket(ctx context.Context, out io.Writer, in io.Reader, cfg *config.Config, o BracketOptions) error {
	ticker := strings.ToUpper(strings.TrimSpace(o.Symbol))
	quantity, entry, profit, stop := o.Qty, o.Entry, o.Profit, o.Stop
	if profit <= 0 && entry > 0 {
		profit = entry * 1.10 // Default +10%
	}
	if stop <= 0 && entry > 0 {
		stop = entry * 0.95 // Default -5%
	}

	isAutoConfirm := o.Yes
	isDryRun := o.DryRun || (!o.Live && cfg.DryRunDefault)
	// Build Bracket Order
	order, err := trader.BuildBracketOrderSpec(ticker, quantity, entry, profit, stop, trader.OrderTypeLimit)
	if err != nil {
		fmt.Fprintf(out, "%sBracket Order Build Error:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	if isDryRun && (cfg.AppKey == "" || cfg.AppSecret == "") {
		fmt.Fprintf(out, "\n%s=== Bracket Order Preview (1st-Triggers-OCO) ===%s\n", clicolor.Cyan, clicolor.Reset)
		fmt.Fprintf(out, "  Symbol:           %s%s%s\n", clicolor.Yellow, ticker, clicolor.Reset)
		fmt.Fprintf(out, "  Quantity:         %.2f shares\n", quantity)
		fmt.Fprintf(out, "  %s1. Entry Order:%s   BUY Limit @ $%.2f (Est. Value: $%.2f)\n", clicolor.Green, clicolor.Reset, entry, quantity*entry)
		fmt.Fprintf(out, "  %s2. Take-Profit:%s   SELL Limit @ $%.2f (+%.1f%% gain)\n", clicolor.Green, clicolor.Reset, profit, ((profit-entry)/entry)*100.0)
		fmt.Fprintf(out, "  %s3. Stop-Loss:%s     SELL Stop @ $%.2f (-%.1f%% loss)\n", clicolor.Red, clicolor.Reset, stop, ((entry-stop)/entry)*100.0)
		fmt.Fprintf(out, "\n%s=== DRY-RUN MODE (No order placed) ===%s\n", clicolor.Cyan, clicolor.Reset)
		payload, _ := json.MarshalIndent(order, "", "  ")
		fmt.Fprintf(out, "Schwab API JSON Payload:\n%s\n\n", string(payload))
		fmt.Fprintf(out, "%s✓ Bracket order is structurally valid according to Schwab API schema.%s\n", clicolor.Green, clicolor.Reset)
		return nil
	}

	tc, err := trader.NewTraderClient(ctx, cfg)
	if err != nil {
		fmt.Fprintf(out, "%sClient Error:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	// Resolve Roth IRA
	roth, err := tc.GetRothAccount(ctx)
	if err != nil {
		fmt.Fprintf(out, "%sAccount Resolution Error:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	// Pre-trade Safety Validation
	validation, err := tc.Safety.ValidateOrder(order, &roth.Details, entry)
	if err != nil {
		fmt.Fprintf(out, "\n%s✕ Order Rejected by Pre-Trade Safety Guard:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	// Render Bracket Order Card
	cash := trader.GetBuyingPower(&roth.Details)
	fmt.Fprintf(out, "\n%s=== Bracket Order Preview (1st-Triggers-OCO) ===%s\n", clicolor.Cyan, clicolor.Reset)
	fmt.Fprintf(out, "  Target Account:   %s (Hash: %s...)\n", roth.AccountNumber, roth.HashValue[:10])
	fmt.Fprintf(out, "  Symbol:           %s%s%s\n", clicolor.Yellow, ticker, clicolor.Reset)
	fmt.Fprintf(out, "  Quantity:         %.2f shares\n", quantity)
	fmt.Fprintf(out, "  %s1. Entry Order:%s   BUY Limit @ $%.2f (Est. Value: $%.2f)\n", clicolor.Green, clicolor.Reset, entry, validation.EstimatedValue)
	fmt.Fprintf(out, "  %s2. Take-Profit:%s   SELL Limit @ $%.2f (+%.1f%% gain)\n", clicolor.Green, clicolor.Reset, profit, ((profit-entry)/entry)*100.0)
	fmt.Fprintf(out, "  %s3. Stop-Loss:%s     SELL Stop @ $%.2f (-%.1f%% loss)\n", clicolor.Red, clicolor.Reset, stop, ((entry-stop)/entry)*100.0)
	fmt.Fprintf(out, "  Available Cash:   $%.2f\n", cash)
	fmt.Fprintf(out, "  Log Output File:  %s\n", cfg.LogFile)

	if isDryRun {
		fmt.Fprintf(out, "\n%s=== DRY-RUN MODE (No order placed) ===%s\n", clicolor.Cyan, clicolor.Reset)
		payload, _ := json.MarshalIndent(order, "", "  ")
		fmt.Fprintf(out, "Schwab API JSON Payload:\n%s\n\n", string(payload))
		fmt.Fprintf(out, "%s✓ Bracket order is valid and passes all safety checks.%s\n", clicolor.Green, clicolor.Reset)
		fmt.Fprintf(out, "To submit live to Schwab with response logging, run:\n")
		fmt.Fprintf(out, "  %sschwaber bracket -symbol %s -qty %.0f -entry %.2f -profit %.2f -stop %.2f --debug%s\n",
			clicolor.Green, ticker, quantity, entry, profit, stop, clicolor.Reset)
		return nil
	}

	// Live confirmation
	if !isAutoConfirm {
		fmt.Fprintf(out, "\n%s⚠ LIVE BRACKET ORDER EXECUTION WARNING ⚠%s\n", clicolor.Red, clicolor.Reset)
		fmt.Fprintf(out, "Submit live Bracket Order for %.2f %s to Charles Schwab? (y/N): ", quantity, ticker)
		reader := bufio.NewReader(in)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))
		if input != "y" && input != "yes" {
			fmt.Fprintf(out, "%sOrder cancelled by user.%s\n", clicolor.Yellow, clicolor.Reset)
			return nil
		}
	}

	// Place Bracket Order via native client
	fmt.Fprintf(out, "\n%sSubmitting Bracket Order to Charles Schwab API...%s\n", clicolor.Cyan, clicolor.Reset)
	orderID, err := tc.Client.PlaceOrder(ctx, roth.HashValue, order)
	if err != nil {
		fmt.Fprintf(out, "%sOrder Placement Failed:%s %v\n", clicolor.Red, clicolor.Reset, err)
		fmt.Fprintf(out, "Check %s for full API response headers and body.\n", cfg.LogFile)
		return clicolor.ErrReported
	}

	oid, status, desc, cerr := schwab.ConfirmPlacedOrder(ctx, tc.Client, roth.HashValue, orderID)
	if cerr != nil {
		fmt.Fprintf(out, "%sPlaced but could not confirm status:%s id=%s err=%v\n", clicolor.Yellow, clicolor.Reset, orderID, cerr)
		fmt.Fprintf(out, "Not treating as success. Check %s / Order Status.\n", cfg.LogFile)
		return clicolor.ErrReported
	}
	if schwab.IsTerminalReject(status) {
		fmt.Fprintf(out, "\n%s✕ Bracket REJECTED by Schwab:%s %s\n", clicolor.Red, clicolor.Reset, desc)
		fmt.Fprintf(out, "Order ID: %s  Status: %s\n", oid, status)
		return clicolor.ErrReported
	}
	fmt.Fprintf(out, "\n%s✓ Bracket accepted:%s %s\n", clicolor.Green, clicolor.Reset, status)
	if desc != "" {
		fmt.Fprintf(out, "Detail:   %s\n", desc)
	}
	fmt.Fprintf(out, "Order ID: %s%s%s\n", clicolor.Bold, oid, clicolor.Reset)
	fmt.Fprintf(out, "Account:  %s\n", roth.AccountNumber)
	return nil
}

// Place places (or previews) a buy or sell order for the default Roth IRA
// account: market, or limit when Price > 0.
func Place(ctx context.Context, out io.Writer, in io.Reader, cfg *config.Config, o PlaceOptions) error {
	instruction := o.Instruction
	ticker := strings.ToUpper(strings.TrimSpace(o.Symbol))
	quantity := o.Qty
	limitPrice := o.Price
	isAutoConfirm := o.Yes
	isDryRun := o.DryRun || (!o.Live && cfg.DryRunDefault)

	if ticker == "" || quantity <= 0 {
		fmt.Fprintf(out, "%sError: -symbol and -qty (> 0) are required.%s\n", clicolor.Red, clicolor.Reset)
		return clicolor.ErrReported
	}

	tc, err := trader.NewTraderClient(ctx, cfg)
	if err != nil {
		fmt.Fprintf(out, "%sClient Error:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	// Resolve Roth IRA account
	roth, err := tc.GetRothAccount(ctx)
	if err != nil {
		fmt.Fprintf(out, "%sAccount Resolution Error:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	// Fetch real-time market quote
	var marketPrice float64
	quote, err := tc.Client.GetQuote(ctx, ticker)
	if err == nil {
		marketPrice = trader.ExtractQuoteLastPrice(quote)
	}

	// Build Order
	orderType := trader.OrderTypeMarket
	var pPtr *float64
	if limitPrice > 0 {
		orderType = trader.OrderTypeLimit
		p := limitPrice
		pPtr = &p
	}

	order, err := trader.BuildOrderSpec(orderType, instruction, ticker, quantity, pPtr, trader.DurationDay, trader.SessionNormal)
	if err != nil {
		fmt.Fprintf(out, "%sOrder Build Error:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	// Pre-trade Safety Validation
	validation, err := tc.Safety.ValidateOrder(order, &roth.Details, marketPrice)
	if err != nil {
		fmt.Fprintf(out, "\n%s✕ Order Rejected by Pre-Trade Safety Guard:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	// Order Preview Card
	actionCol := clicolor.Green
	if instruction == trader.InstructionSell {
		actionCol = clicolor.Red
	}
	cash := trader.GetBuyingPower(&roth.Details)

	fmt.Fprintf(out, "\n%s=== %s Order Preview ===%s\n", actionCol, instruction, clicolor.Reset)
	fmt.Fprintf(out, "  Account:         %s (Hash: %s...)\n", roth.AccountNumber, roth.HashValue[:10])
	fmt.Fprintf(out, "  Action:          %s%s%s\n", actionCol, instruction, clicolor.Reset)
	fmt.Fprintf(out, "  Symbol:          %s%s%s\n", clicolor.Yellow, ticker, clicolor.Reset)
	fmt.Fprintf(out, "  Quantity:        %.2f shares\n", quantity)
	fmt.Fprintf(out, "  Order Type:      %s\n", orderType)
	if limitPrice > 0 {
		fmt.Fprintf(out, "  Limit Price:     $%.2f\n", limitPrice)
	}
	fmt.Fprintf(out, "  Est. Total Value: $%.2f\n", validation.EstimatedValue)
	fmt.Fprintf(out, "  Available Cash:  $%.2f\n", cash)

	for _, w := range validation.Warnings {
		fmt.Fprintf(out, "  %s⚠ Warning: %s%s\n", clicolor.Yellow, w, clicolor.Reset)
	}

	if isDryRun {
		fmt.Fprintf(out, "\n%s=== DRY-RUN MODE (No order submitted) ===%s\n", clicolor.Cyan, clicolor.Reset)
		payload, _ := json.MarshalIndent(order, "", "  ")
		fmt.Fprintf(out, "Schwab API Payload:\n%s\n", string(payload))
		fmt.Fprintf(out, "%s✓ Order is valid and passes all pre-trade safety checks.%s\n", clicolor.Green, clicolor.Reset)
		return nil
	}

	// Live confirmation
	if !isAutoConfirm {
		fmt.Fprintf(out, "\n%s⚠ LIVE ORDER EXECUTION WARNING ⚠%s\n", clicolor.Red, clicolor.Reset)
		fmt.Fprintf(out, "Submit live %s order for %.2f %s to Charles Schwab? (y/N): ", instruction, quantity, ticker)
		reader := bufio.NewReader(in)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))
		if input != "y" && input != "yes" {
			fmt.Fprintf(out, "%sOrder cancelled by user.%s\n", clicolor.Yellow, clicolor.Reset)
			return nil
		}
	}

	// Place order via native client
	fmt.Fprintf(out, "\n%sSubmitting order to Charles Schwab...%s\n", clicolor.Cyan, clicolor.Reset)
	orderID, err := tc.Client.PlaceOrder(ctx, roth.HashValue, order)
	if err != nil {
		fmt.Fprintf(out, "%sOrder Placement Failed:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	oid, status, desc, cerr := schwab.ConfirmPlacedOrder(ctx, tc.Client, roth.HashValue, orderID)
	if cerr != nil {
		fmt.Fprintf(out, "%sPlaced but could not confirm status:%s id=%s err=%v\n", clicolor.Yellow, clicolor.Reset, orderID, cerr)
		return clicolor.ErrReported
	}
	if schwab.IsTerminalReject(status) {
		fmt.Fprintf(out, "\n%s✕ Order REJECTED by Schwab:%s %s\n", clicolor.Red, clicolor.Reset, desc)
		fmt.Fprintf(out, "Order ID: %s  Status: %s\n", oid, status)
		return clicolor.ErrReported
	}
	fmt.Fprintf(out, "\n%s✓ Order accepted:%s %s\n", clicolor.Green, clicolor.Reset, status)
	if desc != "" {
		fmt.Fprintf(out, "Detail:   %s\n", desc)
	}
	fmt.Fprintf(out, "Order ID: %s%s%s\n", clicolor.Bold, oid, clicolor.Reset)
	fmt.Fprintf(out, "Account:  %s\n", roth.AccountNumber)
	return nil
}

// List prints the last 30 days of orders for the default Roth IRA account,
// optionally filtered by status (FILLED, WORKING, CANCELED, ...).
func List(ctx context.Context, out io.Writer, cfg *config.Config, status string) error {
	tc, err := trader.NewTraderClient(ctx, cfg)
	if err != nil {
		fmt.Fprintf(out, "%sError:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	roth, err := tc.GetRothAccount(ctx)
	if err != nil {
		fmt.Fprintf(out, "%sAccount Error:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	req := schwab.OrderListRequest{
		From:   time.Now().AddDate(0, -1, 0), // Last 30 days
		To:     time.Now(),
		Status: status,
	}

	orders, err := tc.Client.ListAccountOrders(ctx, roth.HashValue, req)
	if err != nil {
		fmt.Fprintf(out, "%sFailed to fetch orders:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	fmt.Fprintf(out, "\n%s=== Orders for Account %s ===%s\n", clicolor.Bold, roth.AccountNumber, clicolor.Reset)
	fmt.Fprintf(out, "%-14s | %-12s | %-10s | %-16s | %-19s\n", "Order ID", "Status", "Type", "Price", "Entered Time")
	fmt.Fprintln(out, strings.Repeat("-", 80))

	if len(orders) == 0 {
		fmt.Fprintln(out, "No orders found.")
		return nil
	}

	for _, o := range orders {
		idStr := "N/A"
		if o.OrderID != nil {
			idStr = fmt.Sprintf("%d", *o.OrderID)
		}
		priceStr := o.OrderType
		if o.Price != nil {
			priceStr = fmt.Sprintf("$%.2f (%s)", *o.Price, o.OrderType)
		}
		entered := ""
		if o.EnteredTime != nil {
			entered = o.EnteredTime.Format("2006-01-02 15:04:05")
		}

		statusCol := clicolor.White
		if o.Status == "FILLED" {
			statusCol = clicolor.Green
		} else if o.Status == "WORKING" || o.Status == "QUEUED" {
			statusCol = clicolor.Yellow
		} else if o.Status == "CANCELED" || o.Status == "REJECTED" {
			statusCol = clicolor.Red
		}

		fmt.Fprintf(out, "%-14s | %s%-12s%s | %-10s | %-16s | %-19s\n",
			idStr,
			statusCol, o.Status, clicolor.Reset,
			o.OrderType,
			priceStr,
			entered,
		)
	}
	return nil
}

// Cancel cancels an open order on the default Roth IRA account.
func Cancel(ctx context.Context, out io.Writer, cfg *config.Config, orderID string) error {
	tc, err := trader.NewTraderClient(ctx, cfg)
	if err != nil {
		fmt.Fprintf(out, "%sError:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	roth, err := tc.GetRothAccount(ctx)
	if err != nil {
		fmt.Fprintf(out, "%sAccount Error:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	err = tc.Client.CancelOrder(ctx, roth.HashValue, orderID)
	if err != nil {
		fmt.Fprintf(out, "%sCancel failed:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	fmt.Fprintf(out, "%s✓ Cancel request submitted for order %s%s\n", clicolor.Green, orderID, clicolor.Reset)
	return nil
}
