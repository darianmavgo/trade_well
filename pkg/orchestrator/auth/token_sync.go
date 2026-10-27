package auth

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/schwaber/tokensync"
)

// SyncToken pulls or pushes the Schwab token file through schwaber's
// tokensync package (GCS object gs://$GCS_BUCKET/schwab-token/<env>.json),
// logging the attempt to the auth table when db is non-nil.
//
//   - "pull" overwrites tokenPath only when the remote copy's
//     refresh_token_issued is strictly newer (tokensync never regresses a
//     fresher local token).
//   - "push" uploads tokenPath unconditionally.
func SyncToken(ctx context.Context, db *sql.DB, tokenPath, direction string) error {
	if direction != "pull" && direction != "push" {
		return fmt.Errorf("direction must be \"pull\" or \"push\", got %q", direction)
	}
	if strings.TrimSpace(os.Getenv("GCS_BUCKET")) == "" {
		err := fmt.Errorf("GCS_BUCKET is not set; token sync needs the bucket that holds schwab-token/")
		logEvent(db, "sync_failed", err.Error())
		return err
	}

	var err error
	if direction == "pull" {
		err = tokensync.SyncBeforeUse(ctx, tokenPath)
	} else {
		err = tokensync.Push(ctx, tokenPath)
	}
	if err != nil {
		logEvent(db, "sync_failed", fmt.Sprintf("token %s: %v", direction, err))
		return fmt.Errorf("token %s failed: %w", direction, err)
	}
	logEvent(db, "synced", direction)
	return nil
}
