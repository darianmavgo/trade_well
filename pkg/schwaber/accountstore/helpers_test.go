package accountstore

import "testing"

func TestIDFromAcceptsNumberOrString(t *testing.T) {
	if got := idFrom("25474194"); got != "25474194" {
		t.Fatalf("string: got %q", got)
	}
	if got := idFrom(float64(74234603)); got != "74234603" {
		t.Fatalf("float: got %q", got)
	}
}

// TestIDFromAvoidsScientificNotation reproduces the /schwabstate render bug: a
// render in scientific notation, in either a displayed table cell or the
// value written to account_state_order.order_id / account_state_transaction.activity_id.
func TestIDFromAvoidsScientificNotation(t *testing.T) {
	if got := idFrom(float64(1007974038446)); got != "1007974038446" {
		t.Fatalf("expected the full integer, got %q", got)
	}
	if got := idFrom(float64(1.007941386747e+12)); got != "1007941386747" {
		t.Fatalf("expected the full integer, got %q", got)
	}
}

func TestSortTableByAccountNumber(t *testing.T) {
	tbl := &Table{
		Columns: []string{"account_number", "symbol"},
		Rows: [][]string{
			{"74234603", "VOO"},
			{"25474194", "TECL"},
			{"25474194", "QQQ"},
			{"75124906", "CASH"},
		},
	}
	SortByColumn(tbl, "account_number")
	got := []string{tbl.Rows[0][0], tbl.Rows[1][0], tbl.Rows[2][0], tbl.Rows[3][0]}
	want := []string{"25474194", "25474194", "74234603", "75124906"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
