package study

type SP500LeadLagStudy struct {
	marketDBPath  string
	resultsDBPath string
}

func init() {
	Register(&SP500LeadLagStudy{})
}

func (s *SP500LeadLagStudy) ID() string {
	return "sp500_lead_lag"
}

func (s *SP500LeadLagStudy) Name() string {
	return "S&P 500 Minute Lead/Lag Prediction"
}

func (s *SP500LeadLagStudy) Description() string {
	return "Analyzes which S&P 500 stocks predict VOO movements using minute bars."
}

func (s *SP500LeadLagStudy) SetDatabases(marketDB, resultsDB string) {
	s.marketDBPath = marketDB
	s.resultsDBPath = resultsDB
}

func (s *SP500LeadLagStudy) Run() error {
	// TODO: Load 1m bars for VOO and S&P500 constituents from marketDB
	// TODO: Calculate cross-correlation or Granger causality
	// TODO: Write leaderboard to resultsDB
	return nil
}
