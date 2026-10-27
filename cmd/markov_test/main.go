package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

// State constants
const (
	StateBear     = -1
	StateSideways = 0
	StateBull     = 1
)

type Bar struct {
	Date  string
	Close float64
}

func stateName(state int) string {
	switch state {
	case StateBear:
		return "Bear"
	case StateSideways:
		return "Sideways"
	case StateBull:
		return "Bull"
	default:
		return "Unknown"
	}
}

func main() {
	dbPath := "data/market_history.db"
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		log.Fatalf("Database not found at %s", dbPath)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("Failed to open db: %v", err)
	}
	defer db.Close()

	// 1. Fetch GOOGL daily closes ordered by date
	rows, err := db.Query(`
		SELECT Date, close 
		FROM backtest_start 
		WHERE symbol = 'GOOGL' AND timeframe = '1d' 
		ORDER BY Date ASC
	`)
	if err != nil {
		log.Fatalf("Query failed: %v", err)
	}
	defer rows.Close()

	var bars []Bar
	for rows.Next() {
		var b Bar
		if err := rows.Scan(&b.Date, &b.Close); err != nil {
			log.Fatal(err)
		}
		// Some dates might have time attached, just take the prefix if needed, but for now it's fine
		bars = append(bars, b)
	}

	if len(bars) < 21 {
		log.Fatalf("Not enough bars to calculate 20-day returns. Found %d", len(bars))
	}

	fmt.Printf("Loaded %d daily bars for GOOGL\n", len(bars))

	// 2. Define regimes based on 20-day trailing return
	// State[i] is the regime for bars[i]
	states := make([]int, len(bars))
	for i := 20; i < len(bars); i++ {
		ret := (bars[i].Close - bars[i-20].Close) / bars[i-20].Close
		if ret >= 0.05 {
			states[i] = StateBull
		} else if ret <= -0.05 {
			states[i] = StateBear
		} else {
			states[i] = StateSideways
		}
	}

	// 3. Generate Transition Counts and Matrix
	// We map states to array indices: Bear -> 0, Sideways -> 1, Bull -> 2
	counts := [3][3]int{}
	totals := [3]int{}

	for i := 20; i < len(bars)-1; i++ {
		todayState := states[i]
		tomorrowState := states[i+1]

		fromIdx := todayState + 1
		toIdx := tomorrowState + 1

		counts[fromIdx][toIdx]++
		totals[fromIdx]++
	}

	// 4. Print the Transition Matrix
	fmt.Println("\n--- Empirical Transition Matrix ---")
	fmt.Println("From \\ To | Bear (-5%) | Sideways | Bull (+5%)")

	matrix := [3][3]float64{}
	for i := 0; i < 3; i++ {
		fmt.Printf("%-9s |", stateName(i-1))
		for j := 0; j < 3; j++ {
			if totals[i] > 0 {
				matrix[i][j] = float64(counts[i][j]) / float64(totals[i])
			}
			fmt.Printf(" %8.4f |", matrix[i][j])
		}
		fmt.Printf(" (N=%d)\n", totals[i])
	}

	// 5. Predict the next state for the most recent day
	lastIdx := len(bars) - 1
	currentState := states[lastIdx]
	currentReturn := (bars[lastIdx].Close - bars[lastIdx-20].Close) / bars[lastIdx-20].Close

	fmt.Printf("\n--- Current State (As of %s) ---\n", bars[lastIdx].Date[:10])
	fmt.Printf("20-Day Return: %.2f%%\n", currentReturn*100)
	fmt.Printf("Current Regime: %s\n", stateName(currentState))

	// Create current state vector [Bear, Sideways, Bull]
	stateVector := [3]float64{0, 0, 0}
	stateVector[currentState+1] = 1.0

	// Multiply by transition matrix
	nextProb := [3]float64{}
	for j := 0; j < 3; j++ {
		for i := 0; i < 3; i++ {
			nextProb[j] += stateVector[i] * matrix[i][j]
		}
	}

	fmt.Println("\n--- Tomorrow's Probabilities (T+1) ---")
	fmt.Printf("Bear:     %.2f%%\n", nextProb[0]*100)
	fmt.Printf("Sideways: %.2f%%\n", nextProb[1]*100)
	fmt.Printf("Bull:     %.2f%%\n", nextProb[2]*100)

	// Calculate Trading Signal (Bull Prob - Bear Prob)
	signal := nextProb[2] - nextProb[0]
	fmt.Printf("\nGenerated Markov Signal (Bull - Bear): %+.4f\n", signal)
}
