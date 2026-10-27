package manage_exits

import (
	"fmt"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/calendar"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
)

// Where a trade's exit date came from.
const (
	// ExitDateProvisional: computed before the entry filled (from when it was
	// submitted) or from a fill time the broker did not give. It is corrected as
	// soon as the entry order's real fill date is known.
	ExitDateProvisional = "provisional"
	// ExitDateFill: computed from the entry order's actual fill date.
	ExitDateFill = "fill"
)

const dateLayout = "2006-01-02"

// sessionDate is the America/New_York calendar date of t, as midnight UTC (the
// form the calendar package counts in). A fill at 20:00 ET is that ET day, not
// the next UTC day.
func sessionDate(t time.Time) time.Time {
	et := t.In(job_runs.EasternTime())
	return time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, time.UTC)
}

// TodayET is the current America/New_York date, "2006-01-02".
func TodayET(now time.Time) string { return sessionDate(now).Format(dateLayout) }

func exitDateFromSession(day time.Time, maxDays int) string {
	return calendar.AddTradingDays(day, maxDays).Format(dateLayout)
}

// ExitDateFor is the date a position filled at fill must be exited by: maxDays
// trading days after the New York date of the fill.
func ExitDateFor(fill time.Time, maxDays int) string {
	return exitDateFromSession(sessionDate(fill), maxDays)
}

// ExpectedFillDate is the session an order submitted at submitted should fill
// on: that day when it is a session and before the 16:00 ET close, otherwise
// the next session. Used only for a provisional exit date.
func ExpectedFillDate(submitted time.Time) time.Time {
	et := submitted.In(job_runs.EasternTime())
	day := sessionDate(submitted)
	if calendar.IsSession(day) && et.Hour() < 16 {
		return day
	}
	return calendar.AddTradingDays(day, 1)
}

// EntryFill finds the entry order entryOrderID among orders (children included)
// and returns its upper-cased broker status ("" when it is not there) and, for a
// FILLED order, the time it filled. The fill time is the broker's, never the
// time a job happened to notice the fill.
func EntryFill(orders []schwab.Order, entryOrderID string) (status string, filledAt time.Time) {
	if strings.TrimSpace(entryOrderID) == "" {
		return "", time.Time{}
	}
	for _, o := range schwab.FlattenOrders(orders) {
		if o.OrderID == nil || fmt.Sprintf("%d", *o.OrderID) != entryOrderID {
			continue
		}
		status = strings.ToUpper(o.Status)
		if status == "FILLED" && o.CloseTime != nil {
			filledAt = o.CloseTime.Time
		}
		return status, filledAt
	}
	return "", time.Time{}
}
