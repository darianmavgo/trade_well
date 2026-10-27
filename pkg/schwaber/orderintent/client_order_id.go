package orderintent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// DeterministicClientOrderID builds a stable client order id from trade
// identity fields. Schwab may ignore the field; we use it as our durable key.
// Format: "co_" + first 32 hex chars of sha256(account|symbol|side|qty|strategy|day).
func DeterministicClientOrderID(account, symbol, side string, qty float64, strategy, day string) string {
	account = strings.TrimSpace(account)
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	side = strings.ToUpper(strings.TrimSpace(side))
	strategy = strings.TrimSpace(strategy)
	day = strings.TrimSpace(day)
	if day == "" {
		day = time.Now().UTC().Format("2006-01-02")
	}
	raw := fmt.Sprintf("%s|%s|%s|%.8f|%s|%s", account, symbol, side, qty, strategy, day)
	sum := sha256.Sum256([]byte(raw))
	return "co_" + hex.EncodeToString(sum[:16])
}

// FromStagedKey builds a client order id that includes the staged row id once
// claimed, so two different staged rows never share a key even on the same day.
func FromStagedKey(account, symbol, instruction, tradeDate string, stagedID int64) string {
	account = strings.TrimSpace(account)
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	instruction = strings.ToUpper(strings.TrimSpace(instruction))
	tradeDate = strings.TrimSpace(tradeDate)
	if tradeDate == "" {
		tradeDate = time.Now().UTC().Format("2006-01-02")
	}
	raw := fmt.Sprintf("%s|%s|%s|%s|%d", account, symbol, instruction, tradeDate, stagedID)
	sum := sha256.Sum256([]byte(raw))
	return "co_" + hex.EncodeToString(sum[:16])
}
