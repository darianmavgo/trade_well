package sim

import (
	"math"
	"sort"
)

// Sleeve is one strategy trading in the shared account: its signals, its setup and
// its rank. Priority 0 is the primary: it sizes against the whole account and, when it
// lacks cash or wants a symbol a subordinate holds, sells subordinate positions at
// market to get it. Subordinates (Priority > 0) trade the cash that is left and never
// push each other out.
type Sleeve struct {
	ID       string
	Priority int
	Cfg      Config
	Sigs     []Signal
}

// sleeve is a Sleeve ready to run: defaults filled in, one per ID.
type sleeve struct {
	id   string
	prio int
	cfg  Config
}

type position struct {
	sl           *sleeve
	symbol       string
	short        bool
	shares       int
	entryPrice   float64 // booked, after slippage
	entryT       string
	entrySession int // index into the calendar
	current      float64
	target, stop float64
	trail        float64
	maxHigh      float64
	lastT        string // hour of the last price seen
	sessionsHeld int
	holdOverride int
}

type account struct {
	hour        string // the hour being walked: nothing opened in it may be sold in it
	cash        float64
	pos         map[string]*position
	trades      []Trade
	lastExit    map[string]int    // sleeve|symbol -> session index of its last exit
	filled      map[string]string // sleeve|symbol -> YYYY-MM of its last fill
	dailyYield  float64
	dailyBorrow float64
	allowNeg    bool

	park         *Park
	parkSlip     float64
	parkComm     float64
	parkShares   int
	parkLast     float64 // last park-symbol close seen
	parkDivCash  float64
	parkWeight   float64 // sum of the park's share of equity over the sessions
	parkSessions int
	parkEmpty    int // sessions that ended with no park shares
}

// Run plays one strategy's signals over hourly bars: a portfolio of one sleeve. See
// RunPortfolio for the rules.
func Run(sigs []Signal, bars map[string][]Bar, sessions []string, cfg Config) *Result {
	return RunPortfolio([]Sleeve{{Cfg: cfg, Sigs: sigs}}, bars, sessions)
}

// RunPortfolio plays the sleeves' signals over hourly bars in one shared account (one
// cash balance, one position per symbol). bars maps symbol -> regular-hours hourly bars
// in time order; sessions is the sorted calendar of trading days to simulate. The first
// sleeve's InitialCapital is the account's; the best cash yield and short borrow rate of
// any sleeve apply to the account. Each session: accrue cash yield, then walk its hours
// in time order. On every hour, open positions are judged first (trailing stop, stop,
// target in that order; a stop wins a bar that spans both), then orders resting on that
// hour fill, primary sleeve first. A fill is judged on the rest of its own hour using only
// what can follow it (a limit fill keeps the hour's low but not its high). At a session's
// last hour a position held long enough is sold at the close, and exit signals sell the
// sleeve's own position at the close. Positions still open on the last session are
// closed at its last close.
func RunPortfolio(sleeves []Sleeve, bars map[string][]Bar, sessions []string) *Result {
	return RunPortfolioWith(sleeves, bars, sessions, nil)
}

// RunPortfolioWith is RunPortfolio with a parked leftover-cash book (see Park); nil parks nothing.
func RunPortfolioWith(sleeves []Sleeve, bars map[string][]Bar, sessions []string, park *Park) *Result {
	sls := make([]*sleeve, len(sleeves))
	var initial, yield, borrow float64
	for i, sv := range sleeves {
		cfg := sv.Cfg
		if cfg.InitialCapital <= 0 {
			cfg.InitialCapital = DefaultInitialCapital
		}
		if cfg.PositionCap <= 0 {
			cfg.PositionCap = 1
		}
		if cfg.HoldingWindow <= 0 {
			cfg.HoldingWindow = NoHoldLimit
		}
		sls[i] = &sleeve{id: sv.ID, prio: sv.Priority, cfg: cfg}
		if i == 0 {
			initial = cfg.InitialCapital
		}
		yield, borrow = math.Max(yield, cfg.CashYield), math.Max(borrow, cfg.ShortBorrowAnnual)
	}
	if len(sls) == 0 {
		return &Result{Initial: DefaultInitialCapital}
	}
	a := &account{cash: initial, pos: map[string]*position{},
		lastExit: map[string]int{}, filled: map[string]string{}}
	if park != nil && park.Symbol != "" && bars[park.Symbol] != nil {
		a.park = park
		a.parkSlip, a.parkComm = sls[0].cfg.Slippage, sls[0].cfg.Commission
	}
	if yield > 0 {
		a.dailyYield = math.Pow(1+yield, 1.0/252) - 1
	}
	if borrow > 0 {
		a.dailyBorrow = math.Pow(1+borrow, 1.0/252) - 1
	}

	ix := map[string]*index{}
	for sym, bs := range bars {
		ix[sym] = buildIndex(bs)
	}
	type exitSig struct {
		sig Signal
		sl  *sleeve
	}
	orders := map[string][]order{}  // session -> entry orders
	exits := map[string][]exitSig{} // session -> exit signals
	for k, sv := range sleeves {
		for _, s := range sv.Sigs {
			x := ix[s.Symbol]
			if x == nil {
				continue
			}
			if s.Entry < 0 {
				exits[s.Date] = append(exits[s.Date], exitSig{s, sls[k]})
			} else if o, ok := plan(s, sls[k].cfg, x); ok {
				o.sl = sls[k]
				orders[o.session] = append(orders[o.session], o)
			}
		}
	}

	res := &Result{Initial: initial}
	for si, session := range sessions {
		if a.dailyYield > 0 && a.cash > 0 {
			a.cash += a.cash * a.dailyYield
		}
		a.payParkDividend(session)
		// positions held into this session age by one
		for _, p := range a.pos {
			if _, ok := ix[p.symbol].span[session]; ok {
				p.sessionsHeld++
			}
		}

		// the hours to walk: every held symbol and every symbol with an order or exit today
		active := map[string]bool{}
		for sym := range a.pos {
			active[sym] = true
		}
		if a.park != nil {
			active[a.park.Symbol] = true
		}
		todays := orders[session]
		sort.SliceStable(todays, func(i, j int) bool {
			if todays[i].sl.prio != todays[j].sl.prio {
				return todays[i].sl.prio < todays[j].sl.prio
			}
			return todays[i].sig.Symbol < todays[j].sig.Symbol
		})
		for _, o := range todays {
			active[o.sig.Symbol] = true
		}
		filled := make([]bool, len(todays)) // orders spent today
		for _, e := range exits[session] {
			active[e.sig.Symbol] = true
		}
		syms := make([]string, 0, len(active))
		stamps := map[string]bool{}
		for sym := range active {
			sp, ok := ix[sym].span[session]
			if !ok {
				continue
			}
			syms = append(syms, sym)
			for _, b := range ix[sym].bars[sp[0]:sp[1]] {
				stamps[b.T] = true
			}
		}
		sort.Strings(syms)
		ts := make([]string, 0, len(stamps))
		for t := range stamps {
			ts = append(ts, t)
		}
		sort.Strings(ts)

		barAt := func(sym, t string) (Bar, bool, bool, bool) { // bar, ok, first of session, last of session
			x := ix[sym]
			sp, has := x.span[session]
			if !has {
				return Bar{}, false, false, false
			}
			i := sort.Search(sp[1]-sp[0], func(k int) bool { return x.bars[sp[0]+k].T >= t })
			if sp[0]+i >= sp[1] || x.bars[sp[0]+i].T != t {
				return Bar{}, false, false, false
			}
			return x.bars[sp[0]+i], true, i == 0, sp[0]+i == sp[1]-1
		}

		for _, t := range ts {
			a.hour = t
			if a.park != nil {
				if b, ok, _, _ := barAt(a.park.Symbol, t); ok {
					a.parkLast = b.Close
				}
			}
			// 1. every open position is judged on this hour and exit signals sell at the
			//    session close, before any order fills (cash from a sale is spendable)
			for _, sym := range syms {
				b, ok, _, last := barAt(sym, t)
				if !ok {
					continue
				}
				if p, held := a.pos[sym]; held && p.entryT < t {
					a.judge(p, b, last, si)
				}
				if p, held := a.pos[sym]; held {
					if last {
						for _, e := range exits[session] {
							if e.sig.Symbol == sym && e.sl == p.sl {
								a.close(p, b.T, slip(b.Close, p.sl.cfg.Slippage, p.short), "signal", si)
								break
							}
						}
					}
				}
				if p, held := a.pos[sym]; held {
					p.current, p.lastT = b.Close, b.T
				}
			}
			// 2. orders resting on this hour, alphabetically. An order is one entry: once filled it
			//    is spent, so a position that exits does not buy again from the same signal.
			for i, o := range todays {
				if filled[i] {
					continue
				}
				b, ok, first, last := barAt(o.sig.Symbol, t)
				if !ok {
					continue
				}
				filled[i] = a.try(o, b, first, last, si, session)
				if p, held := a.pos[o.sig.Symbol]; held {
					p.current, p.lastT = b.Close, b.T
				}
			}
		}

		if a.park != nil {
			if _, traded := ix[a.park.Symbol].span[session]; traded {
				a.sweepIntoPark()
			}
		}
		eq := a.equity()
		a.recordParkSession(eq)
		res.Curve = append(res.Curve, Point{Date: session, Equity: eq, Cash: a.cash, OpenPositions: a.openCount()})
	}

	a.sellPark(a.parkShares) // the lot is turned back into cash, and is not a trade
	// still open on the last session: closed at the last close seen
	for _, sym := range a.sortedHeld() {
		p := a.pos[sym]
		if p.entryT == p.lastT {
			// opened in the final hour: no later bar to exit on, so it is still open, not a trade
			dir := "LONG"
			if p.short {
				dir = "SHORT"
			}
			res.Open = append(res.Open, OpenPosition{Strategy: p.sl.id, Symbol: p.symbol, Direction: dir,
				Entry: p.entryT, EntryPrice: p.entryPrice, Shares: p.shares})
			continue
		}
		a.close(p, p.lastT, slip(p.current, p.sl.cfg.Slippage, p.short), "end", len(sessions)-1)
	}
	res.Trades = a.trades
	res.Park = a.parkResult()
	return res
}

func (a *account) sortedHeld() []string {
	out := make([]string, 0, len(a.pos))
	for s := range a.pos {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// judge tests one open position against one hour: trailing stop, stop, target,
// then (on the session's last hour) the holding window. It is only ever called for an hour
// after the one the position was entered in: no exit, stop or target included, can trigger in
// the entry hour. That rule is what makes a same-hour trade impossible.
func (a *account) judge(p *position, b Bar, lastHour bool, si int) {
	cfg := p.sl.cfg
	high := b.High
	p.maxHigh = math.Max(p.maxHigh, high)
	if cfg.TrailingStop > 0 {
		p.trail = math.Max(p.trail, p.maxHigh*(1-cfg.TrailingStop))
		if p.trail > 0 && b.Low <= p.trail {
			px := p.trail * (1 - cfg.Slippage)
			if b.Open < p.trail {
				px = b.Open * (1 - cfg.Slippage)
			}
			a.close(p, b.T, px, "trailing_stop", si)
			return
		}
	}
	if b.Low <= p.stop { // a stop of 0 never trips: lows are positive
		px := p.stop * (1 - cfg.Slippage)
		if b.Open < p.stop {
			px = b.Open * (1 - cfg.Slippage)
		}
		a.close(p, b.T, px, "stop_loss", si)
		return
	}
	if p.target > 0 && high >= p.target {
		px := p.target * (1 - cfg.Slippage)
		if b.Open > p.target {
			px = b.Open * (1 - cfg.Slippage)
		}
		a.close(p, b.T, px, "profit_target", si)
		return
	}
	if lastHour {
		if p.short && a.dailyBorrow > 0 {
			a.cash -= float64(p.shares) * b.Close * a.dailyBorrow
		}
		hold := cfg.HoldingWindow
		if p.holdOverride > 0 {
			hold = p.holdOverride
		}
		if p.sessionsHeld >= hold {
			a.close(p, b.T, slip(b.Close, cfg.Slippage, p.short), "time_up", si)
		}
	}
}

// try fills an order if this hour allows it. The new position is not judged on the hour it
// was filled in: its stop, target and holding window are first tested on the next hour bar.
func (a *account) try(o order, b Bar, first, last bool, si int, session string) bool {
	cfg := o.sl.cfg
	sym := o.sig.Symbol
	if held, ok := a.pos[sym]; ok {
		// the primary may take a symbol a subordinate holds; nobody else gets one that is held
		if held.sl == o.sl || o.sl.prio != 0 || held.sl.prio == 0 || held.entryT == b.T {
			return false
		}
	}
	n := 0
	for _, p := range a.pos {
		if p.sl == o.sl {
			n++
		}
	}
	if n >= cfg.PositionCap {
		return false
	}
	var px float64
	switch o.kind {
	case atOpen:
		if !first {
			return false
		}
		px = b.Open
	case atClose:
		if !last {
			return false
		}
		px = b.Close
	case limit:
		if b.Low > o.limit {
			return false
		}
		px = o.limit
		if b.Open < o.limit {
			px = b.Open // the hour opened under the limit: filled at the open
		}
	}
	if px <= 0 {
		return false
	}
	key := o.sl.id + "|" + sym
	if cfg.OneFillPerMonth && a.filled[key] == session[:7] {
		return false // filled earlier this month: out of the rotation until the next
	}
	if cfg.ReentryCooldownDays > 0 {
		if ex, ok := a.lastExit[key]; ok && si-ex <= cfg.ReentryCooldownDays {
			return false
		}
	}
	if _, stop := levels(o, px, slip(px, cfg.Slippage, !o.sig.Short)); stopAboveFill(o, stop, slip(px, cfg.Slippage, !o.sig.Short)) {
		return false // checked before anything is sold to make room
	}
	if held, ok := a.pos[sym]; ok { // preempted: the primary sells the subordinate's position at its last price
		a.close(held, held.lastT, slip(held.current, held.sl.cfg.Slippage, held.short), "preempted", si)
	}
	return a.enter(o, px, b, si) != nil
}

// levels gives the target and stop prices of an entry: the signal's absolute prices win; else the
// row's percentages from the anchor (the order's price for a staged bracket, else the booked entry).
func levels(o order, fill, entry float64) (target, stop float64) {
	cfg, sig := o.sl.cfg, o.sig
	base := entry
	if o.anchored {
		base = fill
		if o.kind == limit {
			base = o.limit
		}
	}
	switch {
	case cfg.TargetPct > 1:
		target = base * cfg.TargetPct
	case cfg.TakeProfitPct > 0:
		target = base * (1 + cfg.TakeProfitPct)
	}
	if sig.TakeProfit > 0 {
		target = sig.TakeProfit
	}
	if cfg.StopLossPct > 0 {
		if cfg.StopLossPct < 1 {
			stop = base * cfg.StopLossPct
		} else {
			stop = base * (1 - cfg.StopLossPct)
		}
	} else if o.anchored {
		stop = base * cfg.crisisFraction()
	}
	if sig.StopLoss > 0 {
		stop = sig.StopLoss
	}
	return target, stop
}

// stopAboveFill: a long whose stop is at or above its fill price would exit on the next bar at a
// price it never traded at, a gain nobody could book. Such an order is not filled; it stays resting.
func stopAboveFill(o order, stop, entry float64) bool {
	return !o.sig.Short && stop > 0 && stop >= entry
}

// enter books the position if it can be afforded and returns it (nil if not).
func (a *account) enter(o order, fill float64, b Bar, si int) *position {
	cfg := o.sl.cfg
	sig := o.sig
	// A long pays the offer (slippage up); a short sells the bid (down).
	entry := slip(fill, cfg.Slippage, !sig.Short)
	target, stop := levels(o, fill, entry)
	if stopAboveFill(o, stop, entry) {
		return nil
	}
	trail := 0.0
	if cfg.TrailingStop > 0 {
		trail = entry * (1 - cfg.TrailingStop)
	}
	equity := a.equity()
	// The primary sizes against the whole account; if cash falls short it sells
	// subordinate positions, longest held first. Everyone else sizes to the cash left.
	subs := o.sl.prio == 0 && a.hasSubordinates()
	budget := a.cash
	switch {
	case o.sl.prio == 0 && (subs || a.parkShares > 0):
		budget = equity
	case a.parkShares > 0: // a subordinate may spend the park too
		budget = a.cash + a.parkBuyingPower()
	}
	shares := cfg.shares(budget, equity, entry, sig.Alloc)
	if cfg.MaxBarVolumeShare > 0 {
		shares = min(shares, int(cfg.MaxBarVolumeShare*b.Volume))
	}
	if shares <= 0 {
		return nil
	}
	cost := float64(shares) * entry
	commission := float64(shares) * cfg.Commission
	if cost+commission > a.cash && a.parkShares > 0 { // the park is sold before anyone's position is
		a.raiseFromPark(cost + commission)
	}
	if subs && cost+commission > a.cash {
		a.raiseCash(cost+commission, si)
	}
	if cost+commission > a.cash { // still short: take what the cash buys
		shares = min(shares, int(a.cash/(entry+cfg.Commission)))
		if shares <= 0 {
			return nil
		}
		cost, commission = float64(shares)*entry, float64(shares)*cfg.Commission
	}
	if cost+commission > a.cash {
		return nil
	}

	a.cash -= cost + commission
	a.filled[o.sl.id+"|"+sig.Symbol] = o.session[:7]
	if sig.Short {
		a.allowNeg = true
	}
	p := &position{sl: o.sl, symbol: sig.Symbol, short: sig.Short, shares: shares, entryPrice: entry, entryT: b.T,
		entrySession: si, current: entry, lastT: b.T, target: target, stop: stop, trail: trail, maxHigh: entry,
		holdOverride: sig.HoldDays}
	a.pos[sig.Symbol] = p
	return p
}

func (a *account) close(p *position, t string, exit float64, reason string, si int) {
	commission := float64(p.shares) * p.sl.cfg.Commission
	var proceeds, pnl, ret float64
	dir := "LONG"
	if p.short {
		// collateral of shares*entry was locked at the open: return it plus (entry-exit)
		dir = "SHORT"
		proceeds = float64(p.shares)*(2*p.entryPrice-exit) - commission
		pnl = float64(p.shares)*(p.entryPrice-exit) - commission
		ret = (p.entryPrice - exit) / p.entryPrice
	} else {
		proceeds = float64(p.shares)*exit - commission
		pnl = proceeds - float64(p.shares)*p.entryPrice
		ret = (exit - p.entryPrice) / p.entryPrice
	}
	a.cash += proceeds
	a.trades = append(a.trades, Trade{Strategy: p.sl.id, Symbol: p.symbol, Direction: dir, Entry: p.entryT, Exit: t,
		EntryPrice: p.entryPrice, ExitPrice: exit, Shares: p.shares, HoldDays: p.sessionsHeld,
		NetPnL: pnl, ReturnPct: ret, Reason: reason, Target: p.target, Stop: p.stop})
	delete(a.pos, p.symbol)
	a.lastExit[p.sl.id+"|"+p.symbol] = si
}

// hasSubordinates says whether any open position belongs to a non-primary sleeve.
func (a *account) hasSubordinates() bool {
	for _, p := range a.pos {
		if p.sl.prio > 0 {
			return true
		}
	}
	return false
}

// raiseCash sells subordinate positions at their last price, longest held first (ties by
// symbol), until the cash covers need.
func (a *account) raiseCash(need float64, si int) {
	var subs []*position
	for _, sym := range a.sortedHeld() {
		if p := a.pos[sym]; p.sl.prio > 0 && p.entryT != a.hour {
			subs = append(subs, p)
		}
	}
	sort.SliceStable(subs, func(i, j int) bool { return subs[i].sessionsHeld > subs[j].sessionsHeld })
	for _, p := range subs {
		if a.cash >= need {
			return
		}
		a.close(p, p.lastT, slip(p.current, p.sl.cfg.Slippage, p.short), "preempted", si)
	}
}

// equity is cash plus every position at its last seen close (a short as its
// locked collateral marked by entry - price). Summed in symbol order so a run
// repeats exactly. It floors at zero unless a short has been opened.
func (a *account) equity() float64 {
	eq := a.cash
	for _, sym := range a.sortedHeld() {
		p := a.pos[sym]
		if p.short {
			eq += float64(p.shares) * (2*p.entryPrice - p.current)
		} else {
			eq += float64(p.shares) * p.current
		}
	}
	eq += float64(a.parkShares) * a.parkLast
	if eq < 0 && a.allowNeg {
		return eq
	}
	return math.Max(0, eq)
}

// shares sizes a position as a fixed share of equity: the signal's allocation
// override (unless FlatAlloc), else the config's, else DefaultAlloc (20%). More than 100% (no margin here) also
// falls back to 20%. A target above the cash is cut to 95% of the cash.
func (c Config) shares(cash, equity, entry, override float64) int {
	if entry <= 0 || equity <= 0 {
		return 0
	}
	if c.FixedShares > 0 {
		if float64(c.FixedShares)*entry > cash {
			return int(cash * 0.95 / entry)
		}
		return c.FixedShares
	}
	if c.FixedDollar > 0 {
		target := c.FixedDollar
		if target > cash {
			target = cash * 0.95
		}
		return int(target / entry)
	}
	alloc := c.Allocation
	if override > 0 && !c.FlatAlloc {
		alloc = override
	}
	if alloc <= 0 || alloc > 1 {
		alloc = c.DefaultAlloc
		if alloc <= 0 {
			alloc = DefaultAllocation
		}
	}
	target := equity * alloc
	if target > cash {
		target = cash * 0.95 // buffer for fees and slippage
	}
	if target <= 0 {
		return 0
	}
	return int(target / entry)
}

// slip moves a fill against the trader; adverseUp is true for a purchase.
func slip(raw, slippage float64, adverseUp bool) float64 {
	if adverseUp {
		return raw * (1 + slippage)
	}
	return raw * (1 - slippage)
}

// openCount is the positions held plus the park lot, which counts as one.
func (a *account) openCount() int {
	n := len(a.pos)
	if a.parkShares > 0 {
		n++
	}
	return n
}

// parkUnitSell is the cash one park share raises at the last park price, after slippage and commission.
func (a *account) parkUnitSell() float64 {
	return a.parkLast*(1-a.parkSlip) - a.parkComm
}

// parkBuyingPower is the cash the whole park lot would raise now.
func (a *account) parkBuyingPower() float64 {
	if a.parkShares <= 0 || a.parkUnitSell() <= 0 {
		return 0
	}
	return float64(a.parkShares) * a.parkUnitSell()
}

// raiseFromPark sells just enough park shares for the cash to cover need.
func (a *account) raiseFromPark(need float64) {
	unit := a.parkUnitSell()
	if a.parkShares <= 0 || unit <= 0 || need <= a.cash {
		return
	}
	a.sellPark(int(math.Ceil((need - a.cash) / unit)))
}

// sellPark sells shares (at most the lot) at the last park price.
func (a *account) sellPark(shares int) {
	shares = min(shares, a.parkShares)
	if shares <= 0 || a.parkLast <= 0 {
		return
	}
	a.cash += float64(shares)*a.parkLast*(1-a.parkSlip) - float64(shares)*a.parkComm
	a.parkShares -= shares
}

// sweepIntoPark buys as many park shares as the cash holds, at the park's last close.
func (a *account) sweepIntoPark() {
	unit := a.parkLast*(1+a.parkSlip) + a.parkComm
	if unit <= 0 {
		return
	}
	n := int(a.cash / unit)
	if n <= 0 {
		return
	}
	a.cash -= float64(n) * unit
	a.parkShares += n
}

// payParkDividend pays the dividend of the session on the shares held coming into it.
func (a *account) payParkDividend(session string) {
	if a.park == nil || a.parkShares <= 0 {
		return
	}
	if d := a.park.Dividends[session]; d > 0 {
		pay := float64(a.parkShares) * d
		a.cash += pay
		a.parkDivCash += pay
	}
}

// recordParkSession adds the session to the park's averages.
func (a *account) recordParkSession(equity float64) {
	if a.park == nil {
		return
	}
	a.parkSessions++
	if equity > 0 {
		a.parkWeight += float64(a.parkShares) * a.parkLast / equity
	}
	if a.parkShares == 0 {
		a.parkEmpty++
	}
}

func (a *account) parkResult() ParkResult {
	if a.park == nil {
		return ParkResult{}
	}
	r := ParkResult{Symbol: a.park.Symbol, Dividends: a.parkDivCash, DaysUnparked: a.parkEmpty}
	if a.parkSessions > 0 {
		r.AvgWeight = a.parkWeight / float64(a.parkSessions)
	}
	return r
}
