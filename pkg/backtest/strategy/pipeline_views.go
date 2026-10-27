package strategy

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// insertRe finds the INSERT clause of a stage statement and captures its target table and columns.
var insertRe = regexp.MustCompile(`(?is)\bINSERT\s+INTO\s+(\w+)\s*\(([^)]*)\)`)

// PipelineViews turns the pipeline directory of strategy id into a SQL script of views over the
// market table. Each stage statement of the form INSERT INTO t (cols) <select> becomes
// CREATE VIEW v_<id>_<t>(cols) AS <select>, with the strategy's own parameters substituted, and
// later stages read the earlier views in place of the slice tables. The SQL is the pipeline's own,
// so the views return what a run would put in the slice tables. Table-creating and index
// statements are dropped. A pipeline that attaches another database (a trained model) cannot be a
// view and is refused. It returns the script and the view names in creation order.
func PipelineViews(id, dir string, cfg StrategyConfig) (script string, views []string, err error) {
	entries, err := readPipelineDir(dir)
	if err != nil {
		return "", nil, err
	}
	var files []string
	for _, e := range entries {
		lower := strings.ToLower(e.Name())
		if e.IsDir() || !strings.HasSuffix(lower, ".sql") {
			continue
		}
		if strings.Contains(lower, "audit") || strings.Contains(lower, "annual") || strings.Contains(lower, "report") || strings.Contains(lower, "compare") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)

	params := make(map[string]string, len(cfg.SQLParams)+2)
	for k, v := range cfg.SQLParams {
		params[k] = v
	}
	if _, ok := params["START_DATE"]; !ok {
		params["START_DATE"] = "0000-00-00"
	}
	if _, ok := params["END_DATE"]; !ok {
		params["END_DATE"] = "9999-12-31"
	}
	cfg.SQLParams = params

	type stage struct{ file, table, cols, rest string }
	var stages []stage
	for _, f := range files {
		content, err := readPipelineFile(dir, f)
		if err != nil {
			return "", nil, err
		}
		text := substitutePlaceholders(string(content), id, f, cfg)
		for _, q := range strings.Split(text, ";") {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			if regexp.MustCompile(`(?i)\bATTACH\b`).MatchString(stripComments(q)) {
				return "", nil, fmt.Errorf("%s attaches another database (a trained model), which a view cannot read", f)
			}
			m := insertRe.FindStringSubmatchIndex(q)
			if m == nil || !regexp.MustCompile(`(?i)^(WITH|INSERT)\b`).MatchString(strings.TrimSpace(stripComments(q))) {
				continue // CREATE, DROP, INDEX and the like
			}
			stages = append(stages, stage{
				file:  f,
				table: q[m[2]:m[3]],
				cols:  strings.Join(strings.Fields(q[m[4]:m[5]]), " "),
				rest:  q[:m[0]] + q[m[1]:],
			})
		}
	}
	if len(stages) == 0 {
		return "", nil, fmt.Errorf("pipeline %s has no INSERT INTO stage to turn into a view", dir)
	}

	prefix := filepath.Base(dir) + "_"
	name := make(map[string]string, len(stages))
	clean := strings.ReplaceAll(strings.ToLower(id), "-", "_")
	for _, s := range stages {
		name[s.table] = "v_" + clean + "_" + strings.TrimPrefix(s.table, prefix)
	}
	var b strings.Builder
	for _, s := range stages {
		body := s.rest
		for t, v := range name {
			body = regexp.MustCompile(`\b`+regexp.QuoteMeta(t)+`\b`).ReplaceAllString(body, v)
		}
		v := name[s.table]
		views = append(views, v)
		fmt.Fprintf(&b, "-- %s\nDROP VIEW IF EXISTS %s;\nCREATE VIEW %s(%s) AS\n%s;\n\n", s.file, v, v, s.cols, strings.TrimSpace(body))
	}
	return b.String(), views, nil
}

func stripComments(q string) string {
	var out []string
	for _, l := range strings.Split(q, "\n") {
		if i := strings.Index(l, "--"); i >= 0 {
			l = l[:i]
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}
