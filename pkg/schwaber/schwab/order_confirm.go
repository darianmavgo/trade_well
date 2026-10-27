package schwab

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ConfirmPlacedOrder follows PlaceOrder's Location (or bare id) and reads the
// broker's real status / statusDescription. HTTP 201 Accepted is not success —
// REJECTED and friends must be treated as failure by the caller.
func ConfirmPlacedOrder(ctx context.Context, client *Client, accountHash, locationOrID string) (id, status, desc string, err error) {
	if client == nil {
		return "", "", "", fmt.Errorf("nil schwab client")
	}
	id = locationOrID
	if i := strings.LastIndex(locationOrID, "/"); i >= 0 {
		id = locationOrID[i+1:]
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", "", fmt.Errorf("empty order id from PlaceOrder")
	}

	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(400 * time.Millisecond)
		}
		got, gerr := client.GetOrder(ctx, accountHash, id)
		if gerr != nil {
			lastErr = gerr
			continue
		}
		status = strings.ToUpper(strings.TrimSpace(got.Status))
		desc = strings.TrimSpace(got.StatusDescription)
		return id, status, desc, nil
	}
	return id, "", "", fmt.Errorf("could not read order %s status: %w", id, lastErr)
}

// IsTerminalReject reports broker statuses that mean the order is dead and
// must not be treated as working/filled.
func IsTerminalReject(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "REJECTED", "CANCELED", "CANCELLED", "EXPIRED", "IGNORED":
		return true
	default:
		return false
	}
}

// IsWorkingOrFilled reports statuses that mean the order is live or done.
func IsWorkingOrFilled(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "FILLED", "WORKING", "QUEUED", "ACCEPTED", "PENDING_ACTIVATION", "AWAITING_PARENT_ORDER", "AWAITING_CONDITION":
		return true
	default:
		return false
	}
}
