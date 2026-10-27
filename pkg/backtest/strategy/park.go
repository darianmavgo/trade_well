package strategy

import "fmt"

// ResidualProvider is implemented by a stack member that is not a sleeve. It
// holds the account's leftover cash in one symbol instead of emitting
// signals, so the runner takes its symbol and passes it to the simulator as
// the default asset.
type ResidualProvider interface {
	ParkSymbol() string
}

// SplitResidual removes park members from members and returns the remaining
// sleeves plus the park symbol ("" when there is none). A park cannot be the
// first member (the primary needs signals) and a stack may hold only one.
func SplitResidual(members []Strategy) (sleeves []Strategy, parkSymbol string, err error) {
	for i, m := range members {
		rp, ok := m.(ResidualProvider)
		if !ok {
			sleeves = append(sleeves, m)
			continue
		}
		if i == 0 {
			return nil, "", fmt.Errorf("%s cannot be the primary: a park emits no signals", m.ID())
		}
		if parkSymbol != "" {
			return nil, "", fmt.Errorf("a stack holds one park, got %s and %s", parkSymbol, rp.ParkSymbol())
		}
		parkSymbol = rp.ParkSymbol()
	}
	return sleeves, parkSymbol, nil
}
