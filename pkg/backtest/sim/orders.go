package sim

import "sort"

// kind is how an order trades inside its session.
type kind int

const (
	atOpen  kind = iota // fill at the first hour's open
	atClose             // fill at the last hour's close
	limit               // rests all session; fills on the first hour that trades down to it
)

// order is an entry signal turned into something that trades on one session.
type order struct {
	sl      *sleeve
	sig     Signal
	session string
	kind    kind
	limit   float64
	// anchored: take profit and stop are measured from the limit/open price the
	// signal set up (as a staged bracket order is) and the crisis stop fills in
	// for a missing stop. Otherwise they come from the booked entry price.
	anchored bool
}

// index holds one symbol's bars by session.
type index struct {
	bars     []Bar
	sessions []string          // sorted
	span     map[string][2]int // session -> [lo, hi) into bars
}

func buildIndex(bars []Bar) *index {
	ix := &index{bars: bars, span: map[string][2]int{}}
	for i, b := range bars {
		s := b.Session()
		if sp, ok := ix.span[s]; ok {
			ix.span[s] = [2]int{sp[0], i + 1}
		} else {
			ix.span[s] = [2]int{i, i + 1}
			ix.sessions = append(ix.sessions, s)
		}
	}
	return ix
}

// after returns the first session strictly after date.
func (ix *index) after(date string) (string, bool) {
	i := sort.SearchStrings(ix.sessions, date)
	if i < len(ix.sessions) && ix.sessions[i] == date {
		i++
	}
	if i >= len(ix.sessions) {
		return "", false
	}
	return ix.sessions[i], true
}

// prevClose is the last hour's close of the session before date.
func (ix *index) prevClose(date string) (float64, bool) {
	i := sort.SearchStrings(ix.sessions, date)
	if i == 0 {
		return 0, false
	}
	sp := ix.span[ix.sessions[i-1]]
	return ix.bars[sp[1]-1].Close, true
}

// plan turns an entry signal into an order, or drops it (no next session in the
// data, or no previous close to hang a limit on).
func plan(sig Signal, cfg Config, ix *index) (order, bool) {
	switch {
	case cfg.EntryLimitPct != 0:
		session, anchored := sig.Date, false
		if !cfg.EntrySameDay {
			next, ok := ix.after(sig.Date)
			if !ok {
				return order{}, false
			}
			session, anchored = next, true
		}
		if _, has := ix.span[session]; !has {
			return order{}, false
		}
		if cfg.EntryLimitPct < 0 {
			return order{sig: sig, session: session, kind: atOpen, anchored: anchored}, true
		}
		prev, ok := ix.prevClose(session)
		if !ok || prev <= 0 {
			return order{}, false
		}
		return order{sig: sig, session: session, kind: limit, limit: prev * cfg.EntryLimitPct, anchored: anchored}, true
	case cfg.NextDayOpenEntry:
		next, ok := ix.after(sig.Date)
		if !ok {
			return order{}, false
		}
		return order{sig: sig, session: next, kind: atOpen, anchored: true}, true
	case cfg.NextDayLimitEntry:
		next, ok := ix.after(sig.Date)
		l := sig.BuyLimit
		if l <= 0 {
			l = sig.Close
		}
		if !ok || l <= 0 {
			return order{}, false
		}
		return order{sig: sig, session: next, kind: limit, limit: l, anchored: true}, true
	}
	if _, has := ix.span[sig.Date]; !has {
		return order{}, false
	}
	return order{sig: sig, session: sig.Date, kind: atClose}, true
}

// crisisFraction is the stop level (as a fraction of the anchor) given to a
// strategy with no stop: anchor * (1 - CrisisStop).
func (c Config) crisisFraction() float64 {
	if c.CrisisStop > 0 && c.CrisisStop < 1 {
		return 1 - c.CrisisStop
	}
	return 0.80
}
