package schwab

import "testing"

func TestLocationOrderID(t *testing.T) {
	cases := map[string]string{
		"https://api.schwabapi.com/trader/v1/accounts/HASH/orders/1007974038446":  "1007974038446",
		"https://api.schwabapi.com/trader/v1/accounts/HASH/orders/1007974038446/": "1007974038446",
		"1007974038446": "1007974038446",
		"":              "",
	}
	for in, want := range cases {
		if got := LocationOrderID(in); got != want {
			t.Errorf("LocationOrderID(%q) = %q, want %q", in, got, want)
		}
	}
}
