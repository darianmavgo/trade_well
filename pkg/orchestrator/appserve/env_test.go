package appserve

import (
	"strings"
	"testing"
)

func TestEnvEntriesMaskSecrets(t *testing.T) {
	got := envEntries([]string{
		"SCHWAB_APP_SECRET=s1", "DASHBOARD_PASSWORD=p1", "SMTP_PASS=p2", "SCHWAB_APP_KEY=k1",
		"POLYGON_API_KEY=k2", "SCHWAB_CLIENT_ID=c1", "SCHWAB_ACCOUNT_NUMBERS=123",
		"STRATEGY_ALLOWLIST=a+b,c", "EMAIL_RECIPIENT=x@y.z", "TZ=UTC", "WITH_EQUALS=a=b",
	})
	by := map[string]envEntry{}
	for i, e := range got {
		by[e.Key] = e
		if i > 0 && got[i-1].Group == e.Group && got[i-1].Key > e.Key {
			t.Errorf("not sorted by key within group %s at %s", e.Group, e.Key)
		}
	}
	for _, k := range []string{"SCHWAB_APP_SECRET", "DASHBOARD_PASSWORD", "SMTP_PASS", "SCHWAB_APP_KEY", "POLYGON_API_KEY", "SCHWAB_CLIENT_ID", "SCHWAB_ACCOUNT_NUMBERS"} {
		if !by[k].Masked || strings.Contains(by[k].Value, "1") || by[k].Value != maskedValue {
			t.Errorf("%s not masked: %+v", k, by[k])
		}
	}
	for k, v := range map[string]string{"STRATEGY_ALLOWLIST": "a+b,c", "EMAIL_RECIPIENT": "x@y.z", "TZ": "UTC", "WITH_EQUALS": "a=b"} {
		if by[k].Masked || by[k].Value != v {
			t.Errorf("%s = %+v, want visible %q", k, by[k], v)
		}
	}
}

func TestEnvGroupsAndHidesThePlatform(t *testing.T) {
	entries := envEntries([]string{"GAE_VERSION=1", "CNB_USER_ID=33", "HOME=/root", "PATH=/bin", "STRATEGY_ALLOWLIST=a", "SCHWAB_ENVIRONMENT=production", "SMTP_HOST=h", "ALPACA_ENDPOINT=e", "CRON_ALLOWED_EMAILS=x@y.z"})
	groups := map[string]string{}
	for _, e := range entries {
		groups[e.Key] = e.Group
	}
	want := map[string]string{"GAE_VERSION": "platform", "CNB_USER_ID": "platform", "HOME": "platform", "PATH": "platform",
		"STRATEGY_ALLOWLIST": "app", "SCHWAB_ENVIRONMENT": "schwab", "SMTP_HOST": "email", "ALPACA_ENDPOINT": "market data", "CRON_ALLOWED_EMAILS": "access"}
	for k, g := range want {
		if groups[k] != g {
			t.Errorf("%s grouped %q, want %q", k, groups[k], g)
		}
	}
	if shown := visibleEnv(entries, false); len(shown) != len(entries)-4 {
		t.Errorf("default view shows %d of %d, want the 4 platform variables hidden", len(shown), len(entries))
	}
	if shown := visibleEnv(entries, true); len(shown) != len(entries) {
		t.Errorf("?all=1 shows %d of %d, want all", len(shown), len(entries))
	}
}
