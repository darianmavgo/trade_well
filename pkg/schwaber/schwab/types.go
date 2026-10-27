package schwab

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// FlexTime unmarshals Schwab timestamps (…Z, …+00:00, …+0000).
type FlexTime struct{ time.Time }

func (f *FlexTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	layouts := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05.000-0700",
		"2006-01-02T15:04:05-0700",
		"2006-01-02T15:04:05.000+0000",
		"2006-01-02T15:04:05+0000",
		"2006-01-02T15:04:05Z0700",
	}
	for _, layout := range layouts {
		if tt, err := time.Parse(layout, s); err == nil {
			f.Time = tt
			return nil
		}
	}
	// last resort: insert colon in zone +0000 -> +00:00
	if len(s) >= 5 && (s[len(s)-5] == '+' || s[len(s)-5] == '-') && s[len(s)-3] != ':' {
		s2 := s[:len(s)-2] + ":" + s[len(s)-2:]
		if tt, err := time.Parse(time.RFC3339, s2); err == nil {
			f.Time = tt
			return nil
		}
	}
	return fmt.Errorf("schwab time: %q", s)
}

// FlexString unmarshals JSON string or number into a string (Schwab accountNumber is numeric).
type FlexString string

func (f *FlexString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == "" {
		*f = ""
		return nil
	}
	if len(s) >= 2 && s[0] == '"' {
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

type LinkedAccount struct {
	AccountNumber string `json:"accountNumber"`
	HashValue     string `json:"hashValue"`
}

type Account struct {
	SecuritiesAccount json.RawMessage `json:"securitiesAccount"`
}

type Quote struct {
	AssetMainType string          `json:"assetMainType"`
	Realtime      *bool           `json:"realtime,omitempty"`
	Quote         json.RawMessage `json:"quote"`
}

// QuoteDetail is the price fields of an equity quote. LowPrice and HighPrice are the
// regular-session extremes so far today.
type QuoteDetail struct {
	LastPrice  float64 `json:"lastPrice"`
	BidPrice   float64 `json:"bidPrice"`
	AskPrice   float64 `json:"askPrice"`
	LowPrice   float64 `json:"lowPrice"`
	HighPrice  float64 `json:"highPrice"`
	OpenPrice  float64 `json:"openPrice"`
	ClosePrice float64 `json:"closePrice"`
}

// Detail decodes the price fields of the quote.
func (q Quote) Detail() (QuoteDetail, error) {
	var d QuoteDetail
	err := json.Unmarshal(q.Quote, &d)
	return d, err
}

type Order struct {
	OrderType            string          `json:"orderType,omitempty"`
	Session              string          `json:"session,omitempty"`
	Price                *float64        `json:"price,omitempty"`
	StopPrice            *float64        `json:"stopPrice,omitempty"`
	Duration             string          `json:"duration,omitempty"`
	OrderStrategyType    string          `json:"orderStrategyType,omitempty"`
	OrderLegCollection   json.RawMessage `json:"orderLegCollection,omitempty"`
	ChildOrderStrategies json.RawMessage `json:"childOrderStrategies,omitempty"`
	OrderID              *int64          `json:"orderId,omitempty"`
	// ClientOrderID is our durable idempotency key. It is NEVER sent to Schwab
	// (json:"-"): the public order schema has no such field, Schwab does not
	// filter list/get by it, and the first live orders that carried it were
	// rejected with 400 "A validation error occurred". It lives in memory and in
	// order_intents (persisted before HTTP); reconcile matches by
	// symbol/side/qty/time.
	ClientOrderID     string     `json:"-"`
	Status            string     `json:"status,omitempty"`
	StatusDescription string     `json:"statusDescription,omitempty"`
	Cancelable        *bool      `json:"cancelable,omitempty"`
	AccountNumber     FlexString `json:"accountNumber,omitempty"`
	Quantity          float64    `json:"quantity,omitempty"`
	// FilledQuantity is how many shares have executed so far; on a CANCELED or EXPIRED order a
	// value above zero means it partly filled before it ended.
	FilledQuantity float64   `json:"filledQuantity,omitempty"`
	EnteredTime    *FlexTime `json:"enteredTime,omitempty"`
	// CloseTime is when the order reached a final state; for a filled order
	// it is the fill time.
	CloseTime *FlexTime `json:"closeTime,omitempty"`
	// OrderActivityCollection holds the executions: what actually traded and at what price. The
	// order's own Price and StopPrice are the limit and the trigger, never the fill. Read it with
	// FillPrice.
	OrderActivityCollection json.RawMessage `json:"orderActivityCollection,omitempty"`
}

type OrderListRequest struct {
	From   time.Time
	To     time.Time
	Status string
}
