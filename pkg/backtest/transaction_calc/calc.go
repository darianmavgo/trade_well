package transaction_calc

import (
	"encoding/csv"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

type TradeRecord struct {
	Symbol   string
	Date     string
	Quantity float64 // positive for buy, negative for sell
	Price    float64
	Comm     float64
}

// ParseIBKR parses an IBKR Transaction History CSV.
func ParseIBKR(path string) ([]models.Trade, []models.DailyEquityPoint, float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, 0, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	var tradeRecords []TradeRecord

	var thHeader map[string]int

	for {
		rec, err := r.Read()
		if err != nil {
			break // EOF or error
		}

		if len(rec) < 3 {
			continue
		}

		section := rec[0]
		rowType := rec[1]

		if section == "Transaction History" {
			if rowType == "Header" {
				thHeader = make(map[string]int)
				for i, col := range rec {
					thHeader[strings.TrimSpace(col)] = i
				}
			} else if rowType == "Data" {
				if thHeader == nil {
					continue
				}

				ttIdx := thHeader["Transaction Type"]
				if ttIdx == 0 || ttIdx >= len(rec) {
					continue
				}
				tType := rec[ttIdx]
				if tType != "Buy" && tType != "Sell" {
					continue // Ignore Dividends, Disbursements, etc.
				}

				symIdx, ok1 := thHeader["Symbol"]
				dateIdx, ok2 := thHeader["Date"]
				qtyIdx, ok3 := thHeader["Quantity"]
				priceIdx, ok4 := thHeader["Price"]
				commIdx, ok5 := thHeader["Commission"]

				if !(ok1 && ok2 && ok3 && ok4 && ok5) {
					continue
				}
				if symIdx >= len(rec) || dateIdx >= len(rec) || qtyIdx >= len(rec) || priceIdx >= len(rec) || commIdx >= len(rec) {
					continue
				}

				qtyStr := strings.ReplaceAll(rec[qtyIdx], ",", "")
				qty, err := strconv.ParseFloat(qtyStr, 64)
				if err != nil || qty == 0 {
					continue
				}

				// Make sure Buy is positive and Sell is negative quantity
				if tType == "Sell" && qty > 0 {
					qty = -qty
				} else if tType == "Buy" && qty < 0 {
					qty = -qty
				}

				price, _ := strconv.ParseFloat(strings.ReplaceAll(rec[priceIdx], ",", ""), 64)
				comm, _ := strconv.ParseFloat(strings.ReplaceAll(rec[commIdx], ",", ""), 64)

				dateStr := strings.Split(rec[dateIdx], ",")[0]
				if len(dateStr) > 10 {
					dateStr = dateStr[:10]
				}

				tradeRecords = append(tradeRecords, TradeRecord{
					Symbol:   rec[symIdx],
					Date:     dateStr,
					Quantity: qty,
					Price:    price,
					Comm:     comm,
				})
			}
		}
	}

	// Match trades FIFO
	openLots := make(map[string][]TradeRecord)
	var closedTrades []models.Trade

	// Sort trade records by date ascending
	sort.Slice(tradeRecords, func(i, j int) bool {
		return tradeRecords[i].Date < tradeRecords[j].Date
	})

	for _, rec := range tradeRecords {
		qty := rec.Quantity
		lots := openLots[rec.Symbol]

		for math.Abs(qty) > 0.0001 && len(lots) > 0 {
			lot := lots[0]
			if (lot.Quantity > 0 && qty > 0) || (lot.Quantity < 0 && qty < 0) {
				break
			}

			matchedQty := math.Min(math.Abs(lot.Quantity), math.Abs(qty))

			entryPrice := lot.Price
			exitPrice := rec.Price
			direction := "LONG"
			if lot.Quantity < 0 {
				direction = "SHORT"
			}

			grossPnl := (exitPrice - entryPrice) * matchedQty
			if direction == "SHORT" {
				grossPnl = (entryPrice - exitPrice) * matchedQty
			}

			entryComm := lot.Comm * (matchedQty / math.Abs(lot.Quantity))
			exitComm := rec.Comm * (matchedQty / math.Abs(rec.Quantity))

			// If commission is provided as a negative cost from IBKR, we add it to PnL.
			// Or we just subtract its absolute value to be safe.
			commPaid := math.Abs(entryComm) + math.Abs(exitComm)
			netPnl := grossPnl - commPaid

			entryTime, _ := time.Parse("2006-01-02", lot.Date)
			exitTime, _ := time.Parse("2006-01-02", rec.Date)
			holdDays := int(exitTime.Sub(entryTime).Hours() / 24)
			if holdDays < 0 {
				holdDays = 0
			}

			trade := models.Trade{
				Symbol:          rec.Symbol,
				EntryDate:       lot.Date,
				ExitDate:        rec.Date,
				EntryPrice:      entryPrice,
				ExitPrice:       exitPrice,
				Shares:          int(matchedQty),
				InvestedCapital: entryPrice * matchedQty,
				GrossPnL:        grossPnl,
				NetPnL:          netPnl,
				CommissionPaid:  commPaid,
				HoldDays:        holdDays,
				ExitReason:      models.ExitReasonSignal,
			}
			if trade.InvestedCapital > 0 {
				trade.ReturnPct = (trade.NetPnL / trade.InvestedCapital)
			}
			closedTrades = append(closedTrades, trade)

			if qty > 0 {
				qty -= matchedQty
			} else {
				qty += matchedQty
			}

			if lot.Quantity > 0 {
				lot.Quantity -= matchedQty
			} else {
				lot.Quantity += matchedQty
			}

			if math.Abs(lot.Quantity) < 0.0001 {
				lots = lots[1:]
			} else {
				lots[0] = lot
			}
		}

		if math.Abs(qty) > 0.0001 {
			lots = append(lots, TradeRecord{
				Symbol:   rec.Symbol,
				Date:     rec.Date,
				Quantity: qty,
				Price:    rec.Price,
				Comm:     rec.Comm,
			})
		}

		openLots[rec.Symbol] = lots
	}

	sort.Slice(closedTrades, func(i, j int) bool {
		return closedTrades[i].ExitDate < closedTrades[j].ExitDate
	})

	// Generate a synthetic step-function equity curve
	initialCash := 100000.0 // Default arbitrary capital since we only have trades

	// Create a daily curve spanning from first trade to last trade
	var equityPoints []models.DailyEquityPoint
	if len(closedTrades) > 0 {
		startDate, _ := time.Parse("2006-01-02", closedTrades[0].EntryDate)
		endDate, _ := time.Parse("2006-01-02", closedTrades[len(closedTrades)-1].ExitDate)
		if startDate.After(endDate) {
			startDate = endDate
		}

		// Pre-compute daily PnL changes
		pnlByDate := make(map[string]float64)
		for _, t := range closedTrades {
			pnlByDate[t.ExitDate] += t.NetPnL
		}

		currentEquity := initialCash
		for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
			// Skip weekends for trading days
			if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
				continue
			}
			ds := d.Format("2006-01-02")
			currentEquity += pnlByDate[ds]
			equityPoints = append(equityPoints, models.DailyEquityPoint{
				Date:        ds,
				TotalEquity: currentEquity,
			})
		}
	}

	return closedTrades, equityPoints, initialCash, nil
}
