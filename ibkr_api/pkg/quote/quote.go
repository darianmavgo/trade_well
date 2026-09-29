// Package quote is the IBKR twin of schwaber/pkg/quote.
package quote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/darianmavgo/ibkr_api/pkg/trader"
)

// Run prints a quote for ticker.
func Run(ctx context.Context, out io.Writer, tc *trader.TraderClient, ticker string) error {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	q, err := tc.Client.GetQuote(ctx, ticker)
	if err != nil {
		return err
	}
	var f map[string]float64
	_ = json.Unmarshal(q.Quote, &f)
	fmt.Fprintf(out, "%s  last %.2f  bid %.2f  ask %.2f  open %.2f  high %.2f  low %.2f  prev close %.2f\n",
		ticker, f["lastPrice"], f["bidPrice"], f["askPrice"], f["openPrice"], f["highPrice"], f["lowPrice"], f["closePrice"])
	return nil
}
