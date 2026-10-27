// Package tokensync is the single place that syncs the local Schwab OAuth
// token.json with GCS so every environment (local Mac, GAE, Cloud Run, ...)
// shares one refresh-token lineage.
//
// Schwab rotates the refresh token on every access-token refresh, so there
// must be exactly one place deciding when to pull a remote copy over the
// local one, and it must never overwrite a local token with an older one:
// doing so re-arms an already-consumed refresh_token and gets it rejected
// with invalid_grant even though it looked "fresh" moments earlier. Both
// entry points here are called from schwaber/pkg/schwab.Client under its
// token mutex + file lock, so this package itself does no locking.
package tokensync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2/google"
)

// Default env is "shared": all environments deliberately use one token
// lineage unless SCHWAB_TOKEN_ENV says two deployments must not share one.
const defaultTokenGCSEnv = "shared"

func tokenGCSEnv() string {
	if v := strings.TrimSpace(os.Getenv("SCHWAB_TOKEN_ENV")); v != "" {
		return v
	}
	return defaultTokenGCSEnv
}

func tokenGCSObject() string {
	return "schwab-token/" + tokenGCSEnv() + ".json"
}

// issuedOnly is the only field this package needs to compare freshness,
// decoded independently of schwab.TokenStore to avoid an import cycle
// (schwab imports tokensync, not the other way around).
type issuedOnly struct {
	RefreshTokenIssued time.Time `json:"refresh_token_issued"`
}

func readIssued(data []byte) (time.Time, error) {
	var v issuedOnly
	if err := json.Unmarshal(data, &v); err != nil {
		return time.Time{}, err
	}
	return v.RefreshTokenIssued, nil
}

// SyncBeforeUse pulls gs://$GCS_BUCKET/schwab-token/<env>.json over
// tokenPath, but ONLY when the remote copy's refresh_token_issued is
// strictly newer than the local copy's (or the local file doesn't exist
// yet). No-op, no error, when GCS_BUCKET is unset. Call this before
// deciding whether the in-memory token needs a refresh.
func SyncBeforeUse(ctx context.Context, tokenPath string) error {
	return PullIfNewer(ctx, tokenPath, tokenGCSObject(), readIssued)
}

// PullIfNewer is SyncBeforeUse for any token object: it pulls
// gs://$GCS_BUCKET/<object> over tokenPath only when the remote copy's freshness
// (issued reads it from the JSON) is strictly newer than the local file's, or the
// local file does not exist. No-op, no error, when GCS_BUCKET is unset. Other
// brokers' clients (ibkr_api) use it with their own object name and freshness field.
func PullIfNewer(ctx context.Context, tokenPath, object string, issued func([]byte) (time.Time, error)) error {
	bucket := strings.TrimSpace(os.Getenv("GCS_BUCKET"))
	if bucket == "" {
		return nil
	}
	remote, err := getGCSObject(ctx, bucket, object)
	if err != nil {
		return err
	}
	remoteIssued, err := issued(remote)
	if err != nil {
		return fmt.Errorf("gcs token pull: parse remote: %w", err)
	}
	if local, err := os.ReadFile(tokenPath); err == nil {
		if localIssued, err := issued(local); err == nil && !remoteIssued.After(localIssued) {
			return nil // local is at least as fresh; never regress it
		}
	}
	if err := os.MkdirAll(dirOf(tokenPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tokenPath, remote, 0o600); err != nil {
		return err
	}
	log.Printf("[tokensync] pulled newer token from gs://%s/%s (issued=%s)",
		bucket, object, remoteIssued.Format(time.RFC3339))
	return nil
}

// Push uploads tokenPath to gs://$GCS_BUCKET/schwab-token/<env>.json
// unconditionally and returns any error. Unlike SyncAfterSave it fails
// loudly when GCS_BUCKET is unset, for callers (CLI "token push") that
// asked for the upload explicitly.
func Push(ctx context.Context, tokenPath string) error {
	return PushObject(ctx, tokenPath, tokenGCSObject())
}

// PushObject is Push for any token object name.
func PushObject(ctx context.Context, tokenPath, object string) error {
	bucket := strings.TrimSpace(os.Getenv("GCS_BUCKET"))
	if bucket == "" {
		return fmt.Errorf("tokensync: GCS_BUCKET is not set")
	}
	if tokenPath == "" {
		return fmt.Errorf("tokensync: empty token path")
	}
	return putGCSObject(ctx, bucket, object, tokenPath)
}

// SyncAfterSave pushes tokenPath to GCS whenever GCS_BUCKET is set. Never
// returns an error to the caller — logs loud on failure only, since a
// failed push after a refresh can orphan other environments until the next
// successful one. Call this every time the token is saved to disk.
func SyncAfterSave(tokenPath string) {
	bucket := strings.TrimSpace(os.Getenv("GCS_BUCKET"))
	if tokenPath == "" || bucket == "" {
		return
	}
	if err := Push(context.Background(), tokenPath); err != nil {
		log.Printf("[tokensync] WARN: failed to publish %s to gs://%s/%s after token save — other environments may be orphaned until the next successful push: %v",
			tokenPath, bucket, tokenGCSObject(), err)
		return
	}
	log.Printf("[tokensync] published %s to gs://%s/%s", tokenPath, bucket, tokenGCSObject())
}

func getGCSObject(ctx context.Context, bucket, object string) ([]byte, error) {
	tok, err := GCSAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("https://storage.googleapis.com/storage/v1/b/%s/o/%s?alt=media",
		url.PathEscape(bucket), url.PathEscape(object))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("gs://%s/%s not found", bucket, object)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gcs get: HTTP %d: %s", resp.StatusCode, body)
	}
	return body, nil
}

func putGCSObject(ctx context.Context, bucket, object, srcPath string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	return PutObject(ctx, bucket, object, data, "application/json")
}

// GetObject reads gs://bucket/object with the Application Default Credentials.
func GetObject(ctx context.Context, bucket, object string) ([]byte, error) {
	return getGCSObject(ctx, bucket, object)
}

// PutObject writes data to gs://bucket/object with the Application Default Credentials.
func PutObject(ctx context.Context, bucket, object string, data []byte, contentType string) error {
	tok, err := GCSAccessToken(ctx)
	if err != nil {
		return err
	}
	u := fmt.Sprintf("https://storage.googleapis.com/upload/storage/v1/b/%s/o?uploadType=media&name=%s",
		url.PathEscape(bucket), url.QueryEscape(object))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gcs put: HTTP %d: %s", resp.StatusCode, body)
	}
	return nil
}

// GCSAccessToken returns an ADC access token scoped for GCS read/write.
func GCSAccessToken(ctx context.Context) (string, error) {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/devstorage.read_write")
	if err != nil {
		return "", fmt.Errorf("GCS ADC: %w", err)
	}
	tok, err := creds.TokenSource.Token()
	if err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("GCS ADC returned empty access token")
	}
	return tok.AccessToken, nil
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			if i == 0 {
				return "/"
			}
			return p[:i]
		}
	}
	return "."
}
