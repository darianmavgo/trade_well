package ibkr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/darianmavgo/schwaber/pkg/orderintent"
	"github.com/darianmavgo/schwaber/pkg/schwab"
)

// ---- schwaber order -> IBKR wire ---------------------------------------------------

type wireOrder struct {
	AcctID        string   `json:"acctId"`
	Conid         int64    `json:"conid"`
	SecType       string   `json:"secType"`
	COID          string   `json:"cOID,omitempty"`
	ParentID      string   `json:"parentId,omitempty"`
	OrderType     string   `json:"orderType"`
	OutsideRTH    bool     `json:"outsideRTH"`
	Price         *float64 `json:"price,omitempty"`
	AuxPrice      *float64 `json:"auxPrice,omitempty"`
	Side          string   `json:"side"`
	Ticker        string   `json:"ticker"`
	TIF           string   `json:"tif"`
	Quantity      float64  `json:"quantity"`
	UseAdaptive   bool     `json:"useAdaptive"`
	IsSingleGroup bool     `json:"isSingleGroup,omitempty"`
}

type leg struct {
	Instruction string  `json:"instruction"`
	Quantity    float64 `json:"quantity"`
	Instrument  struct {
		Symbol string `json:"symbol"`
	} `json:"instrument"`
}

var orderTypeWire = map[string]string{"MARKET": "MKT", "LIMIT": "LMT", "STOP": "STP", "STOP_LIMIT": "STP_LMT"}

func firstLeg(o *Order) (leg, error) {
	var legs []leg
	if len(o.OrderLegCollection) == 0 {
		return leg{}, fmt.Errorf("order has no legs")
	}
	if err := json.Unmarshal(o.OrderLegCollection, &legs); err != nil || len(legs) == 0 {
		return leg{}, fmt.Errorf("order legs unreadable: %v", err)
	}
	return legs[0], nil
}

// toWire translates one Schwab-shaped order, and its bracket children, into IBKR order
// tickets. TRIGGER (entry + OCO of take-profit and stop) becomes IBKR's bracket: the
// children carry parentId = the parent's cOID and no cOID of their own. A bare OCO
// becomes an OCA group (isSingleGroup). conids resolves a symbol to its contract id.
func toWire(ctx context.Context, accountID string, o *Order, conid func(context.Context, string) (int64, error)) ([]wireOrder, error) {
	one := func(x *Order, cOID, parent string) (wireOrder, error) {
		l, err := firstLeg(x)
		if err != nil {
			return wireOrder{}, err
		}
		sym := strings.ToUpper(strings.TrimSpace(l.Instrument.Symbol))
		id, err := conid(ctx, sym)
		if err != nil {
			return wireOrder{}, err
		}
		ot, ok := orderTypeWire[strings.ToUpper(x.OrderType)]
		if !ok {
			return wireOrder{}, fmt.Errorf("unsupported order type %q", x.OrderType)
		}
		side := "BUY"
		if strings.Contains(strings.ToUpper(l.Instruction), "SELL") {
			side = "SELL"
		}
		tif := "DAY"
		if strings.Contains(strings.ToUpper(x.Duration), "GOOD_TILL") || strings.EqualFold(x.Duration, "GTC") {
			tif = "GTC"
		}
		w := wireOrder{AcctID: accountID, Conid: id, SecType: fmt.Sprintf("%d:STK", id), COID: cOID, ParentID: parent,
			OrderType: ot, OutsideRTH: x.Session != "" && !strings.EqualFold(x.Session, "NORMAL"),
			Side: side, Ticker: sym, TIF: tif, Quantity: l.Quantity}
		switch ot {
		case "LMT":
			w.Price = x.Price
		case "STP":
			w.AuxPrice = x.StopPrice
			if w.AuxPrice == nil {
				w.AuxPrice = x.Price
			}
		case "STP_LMT":
			w.Price, w.AuxPrice = x.Price, x.StopPrice
		}
		if (ot == "LMT" && w.Price == nil) || (ot == "STP" && w.AuxPrice == nil) {
			return wireOrder{}, fmt.Errorf("%s order on %s has no price", x.OrderType, sym)
		}
		return w, nil
	}

	strategy := strings.ToUpper(o.OrderStrategyType)
	var leaves []*Order
	var collect func(raw json.RawMessage)
	collect = func(raw json.RawMessage) {
		var kids []Order
		if len(raw) == 0 || json.Unmarshal(raw, &kids) != nil {
			return
		}
		for i := range kids {
			if strings.EqualFold(kids[i].OrderStrategyType, "OCO") || len(kids[i].OrderLegCollection) == 0 {
				collect(kids[i].ChildOrderStrategies)
				continue
			}
			leaves = append(leaves, &kids[i])
		}
	}
	switch strategy {
	case "OCO":
		collect(json.RawMessage("[" + mustJSON(o) + "]"))
		var out []wireOrder
		for _, lf := range leaves {
			w, err := one(lf, "", "")
			if err != nil {
				return nil, err
			}
			w.IsSingleGroup = true
			out = append(out, w)
		}
		return out, nil
	default:
		parent, err := one(o, o.ClientOrderID, "")
		if err != nil {
			return nil, err
		}
		out := []wireOrder{parent}
		if strategy == "TRIGGER" {
			if o.ClientOrderID == "" {
				return nil, fmt.Errorf("a bracket needs a ClientOrderID (its children reference it)")
			}
			collect(o.ChildOrderStrategies)
			for _, lf := range leaves {
				w, err := one(lf, "", o.ClientOrderID)
				if err != nil {
					return nil, err
				}
				out = append(out, w)
			}
		}
		return out, nil
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// ---- IBKR live order -> schwaber order ---------------------------------------------

// mapStatus turns IBKR order status text into schwaber's vocabulary so status checks
// (IsTerminalReject, IsWorkingOrFilled, "WORKING", "FILLED") read the same.
func mapStatus(s string) string {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", "")) {
	case "submitted", "presubmitted", "apipending":
		return "WORKING"
	case "pendingsubmit":
		return "PENDING_ACTIVATION"
	case "filled":
		return "FILLED"
	case "cancelled", "canceled", "apicancelled":
		return "CANCELED"
	case "pendingcancel":
		return "PENDING_CANCEL"
	case "inactive", "rejected":
		return "REJECTED"
	default:
		return strings.ToUpper(strings.TrimSpace(s))
	}
}

func mapOrderType(s string) string {
	l := strings.ToLower(s)
	switch {
	case strings.Contains(l, "stop") && strings.Contains(l, "limit"), l == "stp lmt", l == "stp_lmt":
		return "STOP_LIMIT"
	case strings.Contains(l, "stop"), l == "stp":
		return "STOP"
	case strings.Contains(l, "limit"), l == "lmt":
		return "LIMIT"
	case strings.Contains(l, "market"), l == "mkt":
		return "MARKET"
	}
	return strings.ToUpper(s)
}

func mapDuration(tif string) string {
	if strings.EqualFold(tif, "GTC") {
		return "GOOD_TILL_CANCEL"
	}
	return "DAY"
}

type liveOrder struct {
	Acct        string     `json:"acct"`
	OrderID     FlexString `json:"orderId"`
	Ticker      string     `json:"ticker"`
	Side        string     `json:"side"`
	OrderType   string     `json:"orderType"`
	OrigType    string     `json:"origOrderType"`
	Price       FlexNumber `json:"price"`
	AuxPrice    FlexNumber `json:"auxPrice"`
	TIF         string     `json:"timeInForce"`
	Status      string     `json:"status"`
	TotalSize   FlexNumber `json:"totalSize"`
	OrderRef    string     `json:"order_ref"`
	LastExec    FlexTime   `json:"lastExecutionTime_r"`
	Description string     `json:"orderDesc"`
}

func (l liveOrder) toOrder() Order {
	side := "BUY"
	if strings.HasPrefix(strings.ToUpper(l.Side), "S") {
		side = "SELL"
	}
	legs, _ := json.Marshal([]map[string]any{{"instruction": side, "quantity": float64(l.TotalSize),
		"instrument": map[string]any{"symbol": strings.ToUpper(l.Ticker), "assetType": "EQUITY"}}})
	ot := mapOrderType(firstNonEmpty(l.OrderType, l.OrigType))
	o := Order{OrderType: ot, Duration: mapDuration(l.TIF), Session: "NORMAL", OrderStrategyType: "SINGLE",
		OrderLegCollection: legs, Status: mapStatus(l.Status), Quantity: float64(l.TotalSize),
		AccountNumber: schwab.FlexString(l.Acct), ClientOrderID: l.OrderRef, StatusDescription: l.Status}
	var id int64
	if _, err := fmt.Sscan(string(l.OrderID), &id); err == nil && id > 0 {
		o.OrderID = &id
	}
	switch ot {
	case "LIMIT":
		if p := float64(l.Price); p > 0 {
			o.Price = &p
		}
	case "STOP":
		if p := firstPositive(float64(l.AuxPrice), float64(l.Price)); p > 0 {
			o.StopPrice = &p
		}
	case "STOP_LIMIT":
		if p := float64(l.Price); p > 0 {
			o.Price = &p
		}
		if p := float64(l.AuxPrice); p > 0 {
			o.StopPrice = &p
		}
	}
	if !l.LastExec.IsZero() {
		if o.Status == "FILLED" {
			o.CloseTime = &schwab.FlexTime{Time: l.LastExec.Time}
		}
		o.EnteredTime = &schwab.FlexTime{Time: l.LastExec.Time}
	}
	cancelable := o.Status == "WORKING" || o.Status == "PENDING_ACTIVATION"
	o.Cancelable = &cancelable
	return o
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstPositive(vs ...float64) float64 {
	for _, v := range vs {
		if v > 0 {
			return v
		}
	}
	return 0
}

// ---- schwaber-shaped order API ------------------------------------------------------

// ListAccountOrders is schwab.Client.ListAccountOrders. IBKR only lists the current
// session's orders (live, filled today, cancelled today); From/To and Status filter
// what came back. IBKR wants the list requested once with force=true before it answers.
func (c *Client) ListAccountOrders(ctx context.Context, accountID string, req OrderListRequest) ([]Order, error) {
	all, err := c.liveOrders(ctx)
	if err != nil {
		return nil, err
	}
	var out []Order
	for _, l := range all {
		if accountID != "" && l.Acct != accountID {
			continue
		}
		o := l.toOrder()
		if req.Status != "" && !strings.EqualFold(o.Status, req.Status) {
			continue
		}
		if o.EnteredTime != nil {
			t := o.EnteredTime.Time
			if (!req.From.IsZero() && t.Before(req.From)) || (!req.To.IsZero() && t.After(req.To)) {
				continue
			}
		}
		out = append(out, o)
	}
	return out, nil
}

// ListOrders is schwab.Client.ListOrders: every account's orders.
func (c *Client) ListOrders(ctx context.Context, req OrderListRequest) ([]Order, error) {
	return c.ListAccountOrders(ctx, "", req)
}

func (c *Client) liveOrders(ctx context.Context) ([]liveOrder, error) {
	_, _ = c.TradingAccountsGetBrokerageAccounts(ctx)
	_, _ = c.TradingOrdersGetOpenOrders(ctx, url.Values{"force": {"true"}})
	raw, err := c.TradingOrdersGetOpenOrders(ctx, nil)
	if err != nil {
		return nil, err
	}
	var env struct {
		Orders []liveOrder `json:"orders"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decoding live orders: %w", err)
	}
	return env.Orders, nil
}

// GetOrder is schwab.Client.GetOrder (an order of this session).
func (c *Client) GetOrder(ctx context.Context, accountID, orderID string) (*Order, error) {
	raw, err := c.TradingOrdersGetOrderStatus(ctx, orderID)
	if err != nil {
		return nil, err
	}
	var st struct {
		Account   string     `json:"account"`
		Symbol    string     `json:"symbol"`
		Side      string     `json:"side"`
		OrderType string     `json:"order_type"`
		Status    string     `json:"order_status"`
		Desc      string     `json:"order_status_description"`
		Size      FlexNumber `json:"total_size"`
		TIF       string     `json:"tif"`
		Price     FlexNumber `json:"price"`
		OrderTime FlexTime   `json:"order_time"`
		OrderID   FlexString `json:"order_id"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("decoding order status: %w", err)
	}
	if st.Status == "" && st.OrderID == "" {
		return nil, fmt.Errorf("order %s not found (IBKR only reports orders of the current session)", orderID)
	}
	lo := liveOrder{Acct: st.Account, OrderID: FlexString(orderID), Ticker: st.Symbol, Side: st.Side, OrderType: st.OrderType,
		Price: st.Price, TIF: st.TIF, Status: st.Status, TotalSize: st.Size, LastExec: st.OrderTime}
	o := lo.toOrder()
	if st.Desc != "" {
		o.StatusDescription = st.Desc
	}
	return &o, nil
}

// CancelOrder is schwab.Client.CancelOrder.
func (c *Client) CancelOrder(ctx context.Context, accountID, orderID string) error {
	_, err := c.TradingOrdersCancelOpenOrder(ctx, accountID, orderID, nil)
	return err
}

// PreviewOrder is schwab.Client.PreviewOrder: IBKR's what-if (margin impact) for the
// order, without placing it.
func (c *Client) PreviewOrder(ctx context.Context, accountID string, order *Order) (json.RawMessage, error) {
	if order.ClientOrderID == "" {
		EnsureClientOrderID(accountID, order)
	}
	wire, err := toWire(ctx, accountID, order, c.ResolveConid)
	if err != nil {
		return nil, err
	}
	return c.TradingOrdersPreviewMarginImpact(ctx, accountID, map[string]any{"orders": wire})
}

// ReplyRequiredError means IBKR answered an order with a confirmation prompt (a warning
// such as price far from market) and auto-confirm is off. The order was NOT placed.
type ReplyRequiredError struct{ Messages []string }

func (e *ReplyRequiredError) Error() string {
	return "IBKR needs confirmation before accepting the order (not placed): " + strings.Join(e.Messages, " | ")
}

// placeOrderHTTPRaw sends the order with no intent bookkeeping and returns the broker's
// order id (the parent's, for a bracket).
func (c *Client) placeOrderHTTPRaw(ctx context.Context, accountID string, order *Order) (string, error) {
	wire, err := toWire(ctx, accountID, order, c.ResolveConid)
	if err != nil {
		return "", err
	}
	body := map[string]any{"orders": wire}
	raw, err := c.TradingOrdersSubmitNewOrder(ctx, accountID, body)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.RequestJSON == "" {
			ae.RequestJSON = mustJSON(body)
		}
		return "", err
	}
	for attempt := 0; attempt < 6; attempt++ {
		var items []struct {
			OrderID  FlexString `json:"order_id"`
			Status   string     `json:"order_status"`
			ReplyID  string     `json:"id"`
			Messages []string   `json:"message"`
			Error    string     `json:"error"`
			Suppress bool       `json:"isSuppressed"`
		}
		if err := json.Unmarshal(raw, &items); err != nil {
			var one struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(raw, &one) == nil && one.Error != "" {
				return "", &APIError{Method: "POST", Endpoint: "/v1/api/iserver/account/{account}/orders", Status: 200, Body: one.Error, RequestJSON: mustJSON(body)}
			}
			return "", fmt.Errorf("unreadable order response: %.200s", raw)
		}
		for _, it := range items {
			if it.Error != "" {
				return "", &APIError{Method: "POST", Endpoint: "/v1/api/iserver/account/{account}/orders", Status: 200, Body: it.Error, RequestJSON: mustJSON(body)}
			}
			if it.OrderID != "" {
				return string(it.OrderID), nil
			}
		}
		if len(items) == 0 || items[0].ReplyID == "" {
			return "", fmt.Errorf("IBKR answered the order with neither an order id nor a prompt: %.200s", raw)
		}
		if !c.autoConfirm {
			return "", &ReplyRequiredError{Messages: items[0].Messages}
		}
		raw, err = c.TradingOrdersConfirmOrderReply(ctx, items[0].ReplyID, map[string]any{"confirmed": true})
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("IBKR kept asking for confirmation")
}

// ConfirmPlacedOrder reads the broker's real status of a placed order (as
// schwab.ConfirmPlacedOrder does): a returned id is not success until its status says so.
func (c *Client) ConfirmPlacedOrder(ctx context.Context, accountID, locationOrID string) (id, status, desc string, err error) {
	id = strings.TrimSpace(locationOrID)
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	if id == "" {
		return "", "", "", fmt.Errorf("empty order id from PlaceOrder")
	}
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(400 * time.Millisecond)
		}
		o, gerr := c.GetOrder(ctx, accountID, id)
		if gerr != nil {
			last = gerr
			continue
		}
		return id, strings.ToUpper(o.Status), o.StatusDescription, nil
	}
	return id, "", "", fmt.Errorf("could not read order %s status: %w", id, last)
}

// IsTerminalReject and IsWorkingOrFilled are schwaber's (same statuses).
func IsTerminalReject(status string) bool  { return schwab.IsTerminalReject(status) }
func IsWorkingOrFilled(status string) bool { return schwab.IsWorkingOrFilled(status) }

// ---- idempotent placement (schwab.Client.PlaceOrder) -------------------------------

// EnsureClientOrderID sets order.ClientOrderID (the IBKR cOID) if empty: a hash of the
// serialized order plus the account id. Caller-supplied ids are kept.
func EnsureClientOrderID(accountID string, order *Order) string {
	if order == nil {
		return ""
	}
	if id := strings.TrimSpace(order.ClientOrderID); id != "" {
		order.ClientOrderID = id
		return id
	}
	raw, _ := json.Marshal(order)
	sum := sha256.Sum256([]byte(strings.TrimSpace(accountID) + "|" + string(raw)))
	order.ClientOrderID = "co_" + hex.EncodeToString(sum[:16])
	return order.ClientOrderID
}

// PlaceOrder places an order with durable intent protection when an order-intent store
// is attached, exactly as schwab.Client.PlaceOrder: record "about to place" before HTTP;
// an already-acked intent returns its broker id with no request; an unresolved one looks
// for the order at the broker (IBKR carries our cOID back as order_ref, so the match is
// exact) before ever re-placing; ambiguous network failures leave the intent unknown.
func (c *Client) PlaceOrder(ctx context.Context, accountID string, order *Order) (string, error) {
	if order == nil {
		return "", fmt.Errorf("nil order")
	}
	cid := EnsureClientOrderID(accountID, order)
	store := c.intentStore
	if store == nil {
		return c.placeOrderHTTPRaw(ctx, accountID, order)
	}
	sym, instr, qty := orderFingerprint(order)
	beginErr := store.BeginIntent(ctx, orderintent.Intent{ClientOrderID: cid, AccountHash: accountID, Symbol: sym,
		Instruction: instr, Quantity: qty, RequestJSON: mustJSON(order)})
	if beginErr != nil {
		if !errors.Is(beginErr, orderintent.ErrAlreadyExists) {
			return "", beginErr
		}
		existing, gerr := store.GetIntent(ctx, cid)
		if gerr != nil {
			return "", fmt.Errorf("intent exists but get failed: %w", gerr)
		}
		if existing != nil && existing.Status == orderintent.StatusAcked && existing.BrokerOrderID != "" {
			return existing.BrokerOrderID, nil
		}
		if existing != nil && (existing.Status == orderintent.StatusPending || existing.Status == orderintent.StatusUnknown) {
			if id, found := c.findExistingBrokerOrder(ctx, accountID, existing); found {
				_ = store.AckIntent(ctx, cid, id)
				return id, nil
			}
			return "", fmt.Errorf("order intent %s still %s with no broker match; run ReconcilePendingIntents before retrying: %w",
				cid, existing.Status, orderintent.ErrAlreadyExists)
		}
		if existing == nil || existing.Status != orderintent.StatusFailed {
			return "", beginErr
		}
	}
	id, err := c.placeOrderHTTPRaw(ctx, accountID, order)
	if err != nil {
		if isAmbiguousPlaceError(err) {
			_ = store.MarkUnknown(ctx, cid, err.Error())
			return "", fmt.Errorf("ambiguous place error (intent left unknown for reconcile): %w", err)
		}
		_ = store.FailIntent(ctx, cid, err.Error())
		return "", err
	}
	if aerr := store.AckIntent(ctx, cid, id); aerr != nil {
		return id, fmt.Errorf("placed order %s but AckIntent failed: %w", id, aerr)
	}
	return id, nil
}

func orderFingerprint(o *Order) (symbol, instruction string, qty float64) {
	if l, err := firstLeg(o); err == nil {
		return strings.ToUpper(l.Instrument.Symbol), strings.ToUpper(l.Instruction), l.Quantity
	}
	return "", "", o.Quantity
}

// findExistingBrokerOrder looks for an order this intent placed: by cOID (IBKR echoes it
// as order_ref) first, then by symbol/side/quantity.
func (c *Client) findExistingBrokerOrder(ctx context.Context, accountID string, in *orderintent.Intent) (string, bool) {
	orders, err := c.ListAccountOrders(ctx, accountID, OrderListRequest{})
	if err != nil {
		return "", false
	}
	for _, o := range orders {
		if o.ClientOrderID == in.ClientOrderID && o.OrderID != nil {
			return fmt.Sprint(*o.OrderID), true
		}
	}
	for _, o := range orders {
		sym, instr, qty := orderFingerprint(&o)
		if o.OrderID != nil && sym == strings.ToUpper(in.Symbol) && instr == strings.ToUpper(in.Instruction) && qty == in.Quantity && !IsTerminalReject(o.Status) {
			return fmt.Sprint(*o.OrderID), true
		}
	}
	return "", false
}

// isAmbiguousPlaceError: only a network failure or a 5xx leaves an order's fate unknown.
// An IBKR refusal (4xx, an error entry, a confirmation prompt) is decisive: not placed.
func isAmbiguousPlaceError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status >= 500
	}
	var rr *ReplyRequiredError
	if errors.As(err, &rr) {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, n := range []string{"timeout", "connection reset", "connection refused", "eof", "broken pipe", "i/o timeout"} {
		if strings.Contains(msg, n) {
			return true
		}
	}
	return false
}

// IntentStore returns the attached order-intent store (may be nil).
func (c *Client) IntentStore() *orderintent.Store {
	if c == nil {
		return nil
	}
	return c.intentStore
}

// EnsureIntentStore opens the default intents DB (next to the token file) when none is attached.
func (c *Client) EnsureIntentStore() error {
	if c == nil {
		return fmt.Errorf("nil client")
	}
	if c.intentStore != nil {
		return nil
	}
	store, err := orderintent.OpenDefault(c.tokenPath)
	if err != nil {
		return err
	}
	c.intentStore = store
	return nil
}

// IntentStoreLister adapts *Client to orderintent.OrderLister.
type IntentStoreLister struct{ Client *Client }

func (l IntentStoreLister) ListAccountOrdersForReconcile(ctx context.Context, accountID string, from, to time.Time) ([]orderintent.BrokerOrder, error) {
	orders, err := l.Client.ListAccountOrders(ctx, accountID, OrderListRequest{From: from, To: to})
	if err != nil {
		return nil, err
	}
	var out []orderintent.BrokerOrder
	for _, o := range orders {
		sym, instr, qty := orderFingerprint(&o)
		id, entered := "", time.Time{}
		if o.OrderID != nil {
			id = fmt.Sprint(*o.OrderID)
		}
		if o.EnteredTime != nil {
			entered = o.EnteredTime.Time
		}
		out = append(out, orderintent.BrokerOrder{OrderID: id, Symbol: sym, Instruction: instr, Quantity: qty, EnteredAt: entered, Status: o.Status})
	}
	return out, nil
}

// ReconcilePendingIntents resolves pending/unknown intents against the broker's orders.
func ReconcilePendingIntents(ctx context.Context, c *Client) (*orderintent.ReconcileResult, error) {
	if c == nil || c.intentStore == nil {
		return nil, fmt.Errorf("client or intent store nil")
	}
	return orderintent.ReconcilePendingIntents(ctx, c.intentStore, IntentStoreLister{Client: c})
}
