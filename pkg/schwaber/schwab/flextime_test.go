package schwab

import "testing"

func TestFlexTimePlus0000(t *testing.T) {
	var f FlexTime
	if err := f.UnmarshalJSON([]byte(`"2026-09-21T13:59:00+0000"`)); err != nil {
		t.Fatal(err)
	}
	if f.Year() != 2026 || f.Month() != 9 || f.Day() != 21 {
		t.Fatalf("bad time %#v", f)
	}
}
