// trymksqlite is an example of using github.com/darianmavgo/mksqlite as a Go
// module (not as a binary): it downloads a URL and lands whatever it is in a
// SQLite file. The command knows nothing about the data's format or structure:
// mksqlite detects the format (converters.AutoOpen), parses the stream and
// derives tables and columns.
//
// It only reads from the URL and writes the -out file (created fresh, any
// existing file at that path is replaced). It touches no broker or trading DB.
//
//	go run ./cmd/trymksqlite -out data/trymksqlite.db \
//	  'https://query1.finance.yahoo.com/v8/finance/chart/SPY?range=1y&interval=1d'
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/darianmavgo/mksqlite/converters"
	_ "github.com/darianmavgo/mksqlite/converters/all"
)

const defaultURL = "https://query1.finance.yahoo.com/v8/finance/chart/SPY?range=1y&interval=1d"

func main() {
	out := flag.String("out", "data/trymksqlite.db", "SQLite file to create (replaced if it exists)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: trymksqlite [-out file.db] [url]\n\nDownloads url (default: Yahoo daily bars for SPY) and writes it to a new SQLite file. Read-only against the URL.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	src := defaultURL
	if flag.NArg() > 0 {
		src = flag.Arg(0)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, src, *out); err != nil {
		log.Fatalf("[trymksqlite] %v", err)
	}
}

func run(ctx context.Context, src, out string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return err
	}
	// Yahoo rejects Go's default user agent.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", src, resp.StatusCode)
	}

	provider, err := converters.AutoOpen(resp.Body, nil)
	if err != nil {
		return err
	}

	// mksqlite writes straight into an *os.File, so a partial run still leaves data.
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := converters.ImportToSQLite(provider, f, &converters.ImportOptions{Verbose: true}); err != nil {
		return err
	}
	log.Printf("[trymksqlite] wrote %s", out)
	return nil
}
