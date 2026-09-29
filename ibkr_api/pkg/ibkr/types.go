package ibkr

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/darianmavgo/schwaber/pkg/schwab"
)

// The types below deliberately have schwaber's shapes (schwab.Order, schwab.Account,
// schwab.Quote, ...) so callers written against schwaber read the same here: an order's
// legs are still {"instruction","quantity","instrument":{"symbol","assetType"}}, its
// statuses are still WORKING / FILLED / CANCELED / REJECTED, and a quote still carries
// lastPrice / bidPrice / askPrice. The translation to and from IBKR's own wire format is
// in orders.go and account.go.

// FlexTime unmarshals IBKR timestamps: epoch milliseconds (number or string),
// RFC 3339, or IBKR's yyMMddHHmmss (UTC).
type FlexTime struct{ time.Time }

func (f *FlexTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		switch {
		case n > 1e11: // epoch ms
			f.Time = time.UnixMilli(n).UTC()
			return nil
		case n > 1e9: // epoch s
			f.Time = time.Unix(n, 0).UTC()
			return nil
		}
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "060102150405", "20060102-15:04:05", "2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			f.Time = t
			return nil
		}
	}
	return fmt.Errorf("ibkr time: %q", s)
}

// FlexString unmarshals a JSON string or number into a string (conids arrive both ways).
type FlexString string

func (f *FlexString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == "" {
		*f = ""
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		*f = FlexString(str)
		return nil
	}
	*f = FlexString(s)
	return nil
}

func (f FlexString) String() string { return string(f) }

// FlexNumber unmarshals a JSON number or a numeric string ("1,234.5", "C182.50",
// "" and null read as 0). IBKR sends prices and sizes both ways.
type FlexNumber float64

func (f *FlexNumber) UnmarshalJSON(b []byte) error {
	*f = FlexNumber(ParseNumber(strings.Trim(strings.TrimSpace(string(b)), `"`)))
	return nil
}

// ParseNumber reads a number that may carry IBKR's decoration: thousands commas, a
// leading status letter on market data ("C" = close, "H" = halted), a trailing "%".
func ParseNumber(s string) float64 {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	s = strings.TrimSuffix(s, "%")
	for len(s) > 0 && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) {
		s = s[1:]
	}
	if s == "" || s == "null" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// The shapes below ARE schwaber's types (aliases, not copies): an *ibkr.Order is a
// *schwab.Order, so everything written against schwaber's Order, Account, Quote and
// LinkedAccount (trader.BuildOrderSpec, trader.ParseOrderLegs, the safety guard,
// trade_orchestrator's job packages) takes an IBKR value unchanged. An order's legs are
// {"instruction","quantity","instrument":{"symbol","assetType"}}, its statuses WORKING /
// FILLED / CANCELED / REJECTED, a quote carries lastPrice / bidPrice / askPrice.
// The translation to and from IBKR's wire format is in orders.go and account.go.
//
// IBKR has no account hash: LinkedAccount.HashValue is the account id itself, so code
// that passes acc.HashValue to every call works unchanged. ClientOrderID becomes IBKR's
// cOID (unique per 24 h, <= 64 chars), so unlike Schwab the broker itself carries our
// idempotency key.
type (
	LinkedAccount    = schwab.LinkedAccount
	Account          = schwab.Account
	Quote            = schwab.Quote
	Order            = schwab.Order
	OrderListRequest = schwab.OrderListRequest
)
