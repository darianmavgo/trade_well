package models

// ParamRow is one name/value line in a report's run-parameters block.
type ParamRow struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ParamGroup is a titled set of parameters ("Run", "Exits", "Grid search axes").
type ParamGroup struct {
	Title string     `json:"title"`
	Rows  []ParamRow `json:"rows"`
}

// Add appends a row and returns the group so calls chain.
func (g *ParamGroup) Add(name, value string) *ParamGroup {
	g.Rows = append(g.Rows, ParamRow{Name: name, Value: value})
	return g
}
