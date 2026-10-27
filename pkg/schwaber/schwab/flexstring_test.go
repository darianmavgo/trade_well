package schwab

import (
	"encoding/json"
	"testing"
)

func TestFlexStringNumberOrString(t *testing.T) {
	var a, b FlexString
	if err := json.Unmarshal([]byte(`25474194`), &a); err != nil {
		t.Fatal(err)
	}
	if a.String() != "25474194" {
		t.Fatalf("got %q", a)
	}
	if err := json.Unmarshal([]byte(`"25474194"`), &b); err != nil {
		t.Fatal(err)
	}
	if b.String() != "25474194" {
		t.Fatalf("got %q", b)
	}
}

func TestOrderAccountNumberNumeric(t *testing.T) {
	raw := []byte(`{"orderId":1007993365661,"status":"REJECTED","statusDescription":"nope","accountNumber":25474194}`)
	var o Order
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatal(err)
	}
	if o.Status != "REJECTED" {
		t.Fatalf("status %s", o.Status)
	}
	if o.AccountNumber.String() != "25474194" {
		t.Fatalf("acct %q", o.AccountNumber)
	}
}
