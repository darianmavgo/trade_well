package charting

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

func TestReportShowsRunParameters(t *testing.T) {
	g := &models.ParamGroup{Title: "Grid search axes"}
	g.Add("Profit taker", "5.00%, 8.00%")
	g.Add("Symbols", "TECL <x>")
	out := filepath.Join(t.TempDir(), "r.html")
	if err := GenerateHTML(out, ReportView{Title: "t", Params: []models.ParamGroup{*g}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	html := string(b)
	for _, want := range []string{"Run Parameters", "Grid search axes", "5.00%, 8.00%", "TECL &lt;x&gt;"} {
		if !strings.Contains(html, want) {
			t.Errorf("report missing %q", want)
		}
	}
}
