package schwab

import (
	"fmt"
	"testing"
)

func TestIsPermanentAuthError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{fmt.Errorf("timeout talking to schwab"), false},
		{fmt.Errorf(`authentication expired or invalid -- re-authenticate with schwaber auth: schwab re-authentication required: {"error":"unsupported_token_type","error_description":"Refresh token is invalid, expired or revoked","error":"invalid_grant"}`), true},
		{fmt.Errorf("token refresh failed: invalid_grant"), true},
		{fmt.Errorf("no token available"), true},
	}
	for _, c := range cases {
		if got := IsPermanentAuthError(c.err); got != c.want {
			t.Fatalf("IsPermanentAuthError(%v)=%v want %v", c.err, got, c.want)
		}
	}
}
