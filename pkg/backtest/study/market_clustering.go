package study

type MarketClusteringStudy struct {
	marketDBPath  string
	resultsDBPath string
}

func init() {
	Register(&MarketClusteringStudy{})
}

func (s *MarketClusteringStudy) ID() string {
	return "market_clustering"
}

func (s *MarketClusteringStudy) Name() string {
	return "Market Context Clustering"
}

func (s *MarketClusteringStudy) Description() string {
	return "Extracts features and builds market context clusters across the universe."
}

func (s *MarketClusteringStudy) SetDatabases(marketDB, resultsDB string) {
	s.marketDBPath = marketDB
	s.resultsDBPath = resultsDB
}

func (s *MarketClusteringStudy) Run() error {
	// TODO: Extract features (volatility, returns, etc) from marketDB
	// TODO: Implement or call clustering algorithm
	// TODO: Write cluster_assignments to resultsDB
	return nil
}
