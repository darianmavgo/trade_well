package schwab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/schwaber/orderintent"
)

// EnsureClientOrderID sets order.ClientOrderID if empty, using a hash of the
// serialized order plus account hash (caller-supplied ids are preserved).
func EnsureClientOrderID(accountHash string, order *Order) string {
	if order == nil {
		return ""
	}
	if id := strings.TrimSpace(order.ClientOrderID); id != "" {
		order.ClientOrderID = id
		return id
	}
	raw, _ := json.Marshal(order)
	sum := sha256.Sum256([]byte(strings.TrimSpace(accountHash) + "|" + string(raw)))
	id := "co_" + hex.EncodeToString(sum[:16])
	order.ClientOrderID = id
	return id
}

// PlaceOrder places an order with durable intent protection when an
// orderintent.Store is configured on the client (see WithIntentStore /
// EnsureIntentStore). Flow:
//  1. Ensure clientOrderId
//  2. BeginIntent (pending) BEFORE HTTP
//  3. If already acked → return broker id, no HTTP
//  4. If pending/unknown → try find matching broker order; Ack + return if found
//  5. Else HTTP PlaceOrder; Ack on success, Fail on clear reject, leave
//     pending/unknown on ambiguous network errors
func (c *Client) PlaceOrder(ctx context.Context, hashValue string, order *Order) (string, error) {
	if order == nil {
		return "", fmt.Errorf("nil order")
	}
	clientOrderID := EnsureClientOrderID(hashValue, order)

	store := c.intentStore
	if store == nil {
		return "", fmt.Errorf("PlaceOrder refused: no order-intent store is attached, so a retry could place the same order twice; open the client with trader.NewTraderClient or call EnsureIntentStore (needs TURSO_DATABASE_URL and TURSO_AUTH_TOKEN) before placing orders")
	}

	sym, instr, qty := orderFingerprint(order)
	reqJSON, _ := json.Marshal(order)
	beginErr := store.BeginIntent(ctx, orderintent.Intent{
		ClientOrderID: clientOrderID,
		AccountHash:   hashValue,
		Symbol:        sym,
		Instruction:   instr,
		Quantity:      qty,
		RequestJSON:   string(reqJSON),
	})
	if beginErr != nil {
		if errors.Is(beginErr, orderintent.ErrAlreadyExists) {
			existing, gerr := store.GetIntent(ctx, clientOrderID)
			if gerr != nil {
				return "", fmt.Errorf("intent exists but get failed: %w", gerr)
			}
			if existing != nil && existing.Status == orderintent.StatusAcked && existing.BrokerOrderID != "" {
				return existing.BrokerOrderID, nil
			}
			if existing != nil && (existing.Status == orderintent.StatusPending || existing.Status == orderintent.StatusUnknown) {
				if loc, found := c.findExistingBrokerOrder(ctx, hashValue, existing); found {
					_ = store.AckIntent(ctx, clientOrderID, loc)
					return loc, nil
				}
				// Still unresolved and no broker match — do NOT blind re-place.
				return "", fmt.Errorf("order intent %s still %s with no broker match; run ReconcilePendingIntents before retrying: %w",
					clientOrderID, existing.Status, orderintent.ErrAlreadyExists)
			}
			if existing != nil && existing.Status == orderintent.StatusFailed {
				// BeginIntent should have overwritten failed → pending; fall through.
			} else {
				return "", beginErr
			}
		} else {
			return "", beginErr
		}
	}

	loc, err := c.placeOrderHTTP(ctx, hashValue, order)
	if err != nil {
		if isAmbiguousPlaceError(err) {
			_ = store.MarkUnknown(ctx, clientOrderID, err.Error())
			return "", fmt.Errorf("ambiguous place error (intent left unknown for reconcile): %w", err)
		}
		_ = store.FailIntent(ctx, clientOrderID, err.Error())
		return "", err
	}
	brokerID := LocationOrderID(loc)
	if brokerID == "" {
		brokerID = loc
	}
	if aerr := store.AckIntent(ctx, clientOrderID, brokerID); aerr != nil {
		return loc, fmt.Errorf("placed order %s but AckIntent failed: %w", brokerID, aerr)
	}
	return loc, nil
}

func (c *Client) placeOrderHTTP(ctx context.Context, hashValue string, order *Order) (string, error) {
	return c.placeOrderHTTPRaw(ctx, hashValue, order)
}

// LocationOrderID returns the order id, the last path segment of the Location header Schwab
// returns when it accepts an order.
func LocationOrderID(location string) string {
	loc := strings.TrimRight(strings.TrimSpace(location), "/")
	if i := strings.LastIndex(loc, "/"); i >= 0 {
		return loc[i+1:]
	}
	return loc
}

func orderFingerprint(order *Order) (symbol, instruction string, qty float64) {
	if order == nil {
		return "", "", 0
	}
	qty = order.Quantity
	var legs []struct {
		Instruction string  `json:"instruction"`
		Quantity    float64 `json:"quantity"`
		Instrument  struct {
			Symbol string `json:"symbol"`
		} `json:"instrument"`
	}
	if len(order.OrderLegCollection) > 0 {
		_ = json.Unmarshal(order.OrderLegCollection, &legs)
	}
	if len(legs) > 0 {
		symbol = strings.ToUpper(strings.TrimSpace(legs[0].Instrument.Symbol))
		instruction = strings.ToUpper(strings.TrimSpace(legs[0].Instruction))
		if qty <= 0 {
			qty = legs[0].Quantity
		}
	}
	return symbol, instruction, qty
}

func (c *Client) findExistingBrokerOrder(ctx context.Context, hashValue string, in *orderintent.Intent) (string, bool) {
	if in == nil {
		return "", false
	}
	from := in.CreatedAt.Add(-2 * time.Minute)
	if from.IsZero() {
		from = time.Now().UTC().Add(-24 * time.Hour)
	}
	to := time.Now().UTC().Add(time.Minute)
	orders, err := c.ListAccountOrders(ctx, hashValue, OrderListRequest{From: from, To: to})
	if err != nil {
		return "", false
	}
	broker := ordersToBroker(orders)
	// Reuse matching helper via a one-off reconcile-style search.
	for _, o := range broker {
		if strings.ToUpper(o.Symbol) != strings.ToUpper(in.Symbol) {
			continue
		}
		if in.Instruction != "" && strings.ToUpper(o.Instruction) != strings.ToUpper(in.Instruction) {
			continue
		}
		if o.Quantity != in.Quantity && (o.Quantity-in.Quantity > 1e-6 || in.Quantity-o.Quantity > 1e-6) {
			continue
		}
		delta := o.EnteredAt.Sub(in.CreatedAt)
		if delta < 0 {
			delta = -delta
		}
		if !in.CreatedAt.IsZero() && delta > 30*time.Minute {
			continue
		}
		if o.OrderID != "" {
			return o.OrderID, true
		}
	}
	return "", false
}

func ordersToBroker(orders []Order) []orderintent.BrokerOrder {
	var out []orderintent.BrokerOrder
	for _, o := range orders {
		sym, instr, qty := orderFingerprint(&o)
		id := ""
		if o.OrderID != nil {
			id = fmt.Sprintf("%d", *o.OrderID)
		}
		entered := time.Time{}
		if o.EnteredTime != nil {
			entered = o.EnteredTime.Time
		}
		out = append(out, orderintent.BrokerOrder{
			OrderID: id, Symbol: sym, Instruction: instr, Quantity: qty,
			EnteredAt: entered, Status: o.Status,
		})
		// Flatten child strategies lightly: fingerprint already uses parent legs.
		_ = o
	}
	return out
}

func isAmbiguousPlaceError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	// A structured Schwab answer is decisive: 4xx means Schwab refused the
	// order (definitely not placed); only 5xx leaves it unknown. Matching the
	// response text instead would misread a 400 whose message contains a word
	// like "unknown".
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status >= 500
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"timeout", "temporary", "connection reset", "connection refused",
		"eof", "broken pipe", "tls handshake", "i/o timeout",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	// HTTP 5xx from our doRequest wrappers often look like "API error: status 5.."
	if strings.Contains(msg, "status 5") {
		return true
	}
	return false
}

// IntentStoreLister adapts *Client to orderintent.OrderLister.
type IntentStoreLister struct{ Client *Client }

func (l IntentStoreLister) ListAccountOrdersForReconcile(ctx context.Context, accountHash string, from, to time.Time) ([]orderintent.BrokerOrder, error) {
	if l.Client == nil {
		return nil, fmt.Errorf("nil client")
	}
	orders, err := l.Client.ListAccountOrders(ctx, accountHash, OrderListRequest{From: from, To: to})
	if err != nil {
		return nil, err
	}
	return ordersToBroker(orders), nil
}

// ReconcilePendingIntents resolves pending/unknown intents via broker list matching.
func ReconcilePendingIntents(ctx context.Context, client *Client) (*orderintent.ReconcileResult, error) {
	if client == nil || client.intentStore == nil {
		return nil, fmt.Errorf("client or intent store nil")
	}
	return orderintent.ReconcilePendingIntents(ctx, client.intentStore, IntentStoreLister{Client: client})
}
