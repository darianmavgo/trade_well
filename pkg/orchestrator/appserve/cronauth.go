package appserve

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/runtimeenv"
	"google.golang.org/api/idtoken"
)

const (
	// EnvCronAllowedEmails is a comma-separated list of Google account emails
	// allowed to trigger /cron/* by hand with a Google ID token (the
	// `orchestrator cron <job>` subcommand, or curl with
	// `gcloud auth print-identity-token`). Empty (the default) disables manual
	// triggering entirely: only App Engine's own cron header is accepted.
	EnvCronAllowedEmails = "CRON_ALLOWED_EMAILS"

	// EnvCronTokenAudience overrides the accepted ID-token audience(s),
	// comma-separated. The defaults are the OAuth client IDs gcloud puts in the
	// tokens `gcloud auth print-identity-token` issues for a user account: the
	// current one (seen in a real token on 2026-09-23) and the older one.
	EnvCronTokenAudience = "CRON_ID_TOKEN_AUDIENCE"

	gcloudTokenAudience    = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	gcloudOldTokenAudience = "32555940559.apps.googleusercontent.com"
)

// authorizeCron decides whether r may run a cron job. It accepts, and only:
//
//  1. App Engine cron's own header (set by App Engine, stripped from outside
//     requests), or
//  2. a Bearer Google ID token that verifies (signature, expiry, audience),
//     whose email is verified and is listed in CRON_ALLOWED_EMAILS.
//
// caller names who was let in (for the log); reason says why not.
func authorizeCron(r *http.Request) (caller string, ok bool, reason string) {
	if runtimeenv.IsGAECronHeader(map[string][]string(r.Header)) {
		return "app engine cron", true, ""
	}
	allowed := parseList(os.Getenv(EnvCronAllowedEmails))
	if len(allowed) == 0 {
		return "", false, "no cron header, and manual triggering is off (" + EnvCronAllowedEmails + " unset)"
	}
	authz := r.Header.Get("Authorization")
	token, found := strings.CutPrefix(authz, "Bearer ")
	if !found || strings.TrimSpace(token) == "" {
		return "", false, "no cron header and no bearer token"
	}
	audiences := parseList(os.Getenv(EnvCronTokenAudience))
	if len(audiences) == 0 {
		audiences = []string{gcloudTokenAudience, gcloudOldTokenAudience}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var claims map[string]any
	var lastErr error
	for _, aud := range audiences {
		// idtoken.Validate checks the Google signature, expiry and audience.
		p, err := idtoken.Validate(ctx, strings.TrimSpace(token), aud)
		if err == nil {
			claims, lastErr = p.Claims, nil
			break
		}
		lastErr = err
	}
	if claims == nil {
		return "", false, fmt.Sprintf("bearer token rejected: %v", lastErr)
	}
	return emailAllowed(claims, allowed)
}

// emailAllowed decides from a validated token's claims: the email must be verified and listed.
func emailAllowed(claims map[string]any, allowed []string) (email string, ok bool, reason string) {
	email, _ = claims["email"].(string)
	email = strings.ToLower(strings.TrimSpace(email))
	if verified, _ := claims["email_verified"].(bool); !verified || email == "" {
		return "", false, "bearer token has no verified email"
	}
	for _, a := range allowed {
		if strings.EqualFold(a, email) {
			return email, true, ""
		}
	}
	return "", false, "bearer token email " + email + " is not in " + EnvCronAllowedEmails
}

func parseList(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
