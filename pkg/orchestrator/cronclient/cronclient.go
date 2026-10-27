// Package cronclient triggers a deployed orchestrator's /cron/<job> route by
// hand, the same request App Engine cron makes on schedule, authenticated with
// a Google ID token instead of the cron header. The server side (appserve)
// only accepts it from accounts listed in CRON_ALLOWED_EMAILS.
//
//	curl -H "Authorization: Bearer $(gcloud auth print-identity-token)" \
//	  https://<project>.uc.r.appspot.com/cron/scan_market
//
// is exactly what Trigger does.
package cronclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// EnvURL is the deployed service's base URL, e.g. https://<project>.uc.r.appspot.com.
	EnvURL = "ORCHESTRATOR_URL"

	// jobTimeout matches the server's 12-minute per-job limit plus headroom.
	jobTimeout = 13 * time.Minute
)

// Result is the deployed service's answer.
type Result struct {
	Status int
	Body   string
}

// OK reports a 2xx answer.
func (r *Result) OK() bool { return r.Status >= 200 && r.Status < 300 }

// Options configures Trigger. Zero values use the environment.
type Options struct {
	// BaseURL overrides ORCHESTRATOR_URL / the project-derived default.
	BaseURL string
	// Token overrides `gcloud auth print-identity-token`.
	Token string
	// Query is appended to the request (e.g. dry_run=1 for stage_orders).
	Query url.Values
	HTTP  *http.Client
}

// BaseURL resolves the service URL: ORCHESTRATOR_URL, else
// https://<project>.uc.r.appspot.com where project is GOOGLE_CLOUD_PROJECT
// or the project `gcloud` is set to (the same login the ID token
// comes from, so the URL and the identity always refer to the same project).
func BaseURL(ctx context.Context) (string, error) {
	if u := strings.TrimSpace(os.Getenv(EnvURL)); u != "" {
		return strings.TrimRight(u, "/"), nil
	}
	if p := strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT")); p != "" {
		return "https://" + p + ".uc.r.appspot.com", nil
	}
	out, err := exec.CommandContext(ctx, "gcloud", "config", "get-value", "project", "--quiet").Output()
	if p, ok := gcloudProject(string(out)); err == nil && ok {
		return "https://" + p + ".uc.r.appspot.com", nil
	}
	return "", fmt.Errorf("no service URL: pass --url, set %s, or run `gcloud config set project <id>`", EnvURL)
}

// gcloudProject reads the project out of `gcloud config get-value project` output: empty and
// "(unset)" mean no project is set.
func gcloudProject(out string) (string, bool) {
	p := strings.TrimSpace(out)
	return p, p != "" && p != "(unset)"
}

// IdentityToken returns a Google ID token for the signed-in user from
// `gcloud auth print-identity-token`. gcloud is the one external
// process here on purpose: Google's Go libraries mint ID tokens only for
// service accounts, not for the user login this is meant to use.
func IdentityToken(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "gcloud", "auth", "print-identity-token").Output()
	if err != nil {
		return "", fmt.Errorf("gcloud auth print-identity-token failed (run `gcloud auth login`): %w", err)
	}
	t := strings.TrimSpace(string(out))
	if t == "" {
		return "", fmt.Errorf("gcloud returned an empty identity token")
	}
	return t, nil
}

// Trigger calls GET <base>/cron/<job> and returns the status and body. It does
// not treat a non-2xx answer as an error: the caller decides.
func Trigger(ctx context.Context, job string, opts Options) (*Result, error) {
	job = strings.Trim(strings.TrimSpace(job), "/")
	if job == "" || strings.ContainsAny(job, "/?# ") {
		return nil, fmt.Errorf("invalid job name %q", job)
	}
	base := strings.TrimRight(opts.BaseURL, "/")
	if base == "" {
		var err error
		if base, err = BaseURL(ctx); err != nil {
			return nil, err
		}
	}
	token := opts.Token
	if token == "" {
		var err error
		if token, err = IdentityToken(ctx); err != nil {
			return nil, err
		}
	}
	target := base + "/cron/" + job
	if len(opts.Query) > 0 {
		target += "?" + opts.Query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := opts.HTTP
	if client == nil {
		client = &http.Client{Timeout: jobTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return &Result{Status: resp.StatusCode, Body: strings.TrimSpace(string(body))}, nil
}
