package steps

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	yaml "gopkg.in/yaml.v3"
)

// promQLKeywords is the set of PromQL function names and reserved words that must
// not be treated as metric names when extracting identifiers from expressions.
var promQLKeywords = map[string]bool{
	"sum": true, "avg": true, "min": true, "max": true, "count": true,
	"rate": true, "irate": true, "increase": true, "delta": true, "idelta": true,
	"by": true, "without": true, "on": true, "ignoring": true,
	"group_left": true, "group_right": true, "offset": true, "bool": true,
	"and": true, "or": true, "unless": true,
	"abs": true, "ceil": true, "floor": true, "round": true, "sqrt": true,
	"exp": true, "ln": true, "log2": true, "log10": true,
	"sort": true, "sort_desc": true,
	"topk": true, "bottomk": true, "quantile": true,
	"stddev": true, "stdvar": true, "count_values": true,
	"absent": true, "changes": true, "resets": true,
	"scalar": true, "vector": true, "time": true, "timestamp": true,
	"histogram_quantile": true, "label_join": true, "label_replace": true,
	"predict_linear": true, "holt_winters": true, "deriv": true,
	"clamp": true, "clamp_max": true, "clamp_min": true,
	"day_of_month": true, "day_of_week": true, "days_in_month": true,
	"hour": true, "minute": true, "month": true, "year": true,
	"avg_over_time": true, "min_over_time": true, "max_over_time": true,
	"sum_over_time": true, "count_over_time": true, "last_over_time": true,
	"quantile_over_time": true, "stddev_over_time": true, "stdvar_over_time": true,
	"present_over_time": true, "absent_over_time": true,
	"group": true, "inf": true, "nan": true,
}

// identRe matches bare PromQL identifiers (metric names, function calls, keywords).
var identRe = regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*`)

// labelBlockRe matches label matcher blocks {...} so we can strip them before
// extracting metric names (label names live inside those blocks).
var labelBlockRe = regexp.MustCompile(`\{[^}]*\}`)

// aggregationGroupingRe matches by(...), without(...), on(...), ignoring(...)
// clauses in PromQL aggregations / binary operators. The label names inside
// these clauses must not be treated as metric names.
var aggregationGroupingRe = regexp.MustCompile(`(?i)\b(?:by|without|on|ignoring)\s*\([^)]*\)`)

// extractMetricNamesFromExpr extracts bare metric name identifiers from a PromQL expression.
// It strips label matcher blocks, then finds all identifiers, filtering out function calls,
// reserved keywords, single-char identifiers, and Grafana template variables ($foo).
func extractMetricNamesFromExpr(expr string) []string {
	// Strip label selector blocks so we don't pick up label names as metrics.
	stripped := labelBlockRe.ReplaceAllString(expr, "")
	// Strip aggregation grouping clauses (by(...), without(...), etc.) so label
	// names used there are not mistaken for metric names.
	stripped = aggregationGroupingRe.ReplaceAllString(stripped, "")

	// Find all identifier positions in the stripped expression.
	matches := identRe.FindAllStringIndex(stripped, -1)

	var metrics []string
	for _, m := range matches {
		start, end := m[0], m[1]
		ident := stripped[start:end]

		// Skip single-character identifiers (too short to be a real metric).
		if len(ident) <= 1 {
			continue
		}

		// Skip known PromQL keywords and function names.
		if promQLKeywords[ident] {
			continue
		}

		// Skip identifiers that are immediately followed by '(' — those are function calls.
		if end < len(stripped) && stripped[end] == '(' {
			continue
		}

		// Skip identifiers that look like Grafana template variables: they are
		// always preceded by '$' in the original expression. After stripping we
		// can detect this by checking whether the character before the match is '$'.
		if start > 0 && stripped[start-1] == '$' {
			continue
		}

		metrics = append(metrics, ident)
	}
	return metrics
}

// walkYAMLNode recursively visits a yaml.Node tree, collecting string values
// of map keys named "expr" or "expression".
func walkYAMLNode(node *yaml.Node, out *[]string) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		// Children of a mapping alternate key/value.
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			val := node.Content[i+1]
			if key.Kind == yaml.ScalarNode &&
				(key.Value == "expr" || key.Value == "expression") &&
				val.Kind == yaml.ScalarNode {
				*out = append(*out, val.Value)
			} else {
				walkYAMLNode(val, out)
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			walkYAMLNode(child, out)
		}
	case yaml.DocumentNode:
		for _, child := range node.Content {
			walkYAMLNode(child, out)
		}
	}
}

// extractExprsFromAlertYAML parses a Grafana alert-rule YAML file and returns
// all PromQL expressions found in "expr" fields.
func extractExprsFromAlertYAML(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	var exprs []string
	walkYAMLNode(&root, &exprs)
	return exprs, nil
}

// walkJSONValue recursively walks an interface{} decoded from JSON, collecting
// string values of map keys named "expr".
func walkJSONValue(v interface{}, out *[]string) {
	switch val := v.(type) {
	case map[string]interface{}:
		for k, child := range val {
			if k == "expr" {
				if s, ok := child.(string); ok {
					*out = append(*out, s)
				}
			} else {
				walkJSONValue(child, out)
			}
		}
	case []interface{}:
		for _, item := range val {
			walkJSONValue(item, out)
		}
	}
}

// extractExprsFromDashboardJSON parses a Grafana dashboard JSON file and returns
// all PromQL expressions found in "expr" string fields.
func extractExprsFromDashboardJSON(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var root interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	var exprs []string
	walkJSONValue(root, &exprs)
	return exprs, nil
}

// BuildControlRoomMetricFilter parses grafana alert rules and dashboard definitions
// rooted at ptdRoot to extract all metric names that must be forwarded to the
// control room Mimir. Returns a pipe-separated regex string suitable for use in
// a prometheus.relabel keep rule, e.g. "metric_a|metric_b|metric_c".
//
// Alert YAML files are read from {ptdRoot}/python-pulumi/src/ptd/grafana_alerts/*.yaml.
// Dashboard JSON files are read from {ptdRoot}/python-pulumi/src/ptd/grafana_dashboards/*.json.
func BuildControlRoomMetricFilter(ptdRoot string) (string, error) {
	alertDir := filepath.Join(ptdRoot, "python-pulumi", "src", "ptd", "grafana_alerts")
	dashDir := filepath.Join(ptdRoot, "python-pulumi", "src", "ptd", "grafana_dashboards")

	var allExprs []string

	// Collect expressions from alert YAML files.
	alertFiles, err := filepath.Glob(filepath.Join(alertDir, "*.yaml"))
	if err != nil {
		return "", fmt.Errorf("globbing alert files: %w", err)
	}
	for _, f := range alertFiles {
		exprs, fErr := extractExprsFromAlertYAML(f)
		if fErr != nil {
			return "", fErr
		}
		allExprs = append(allExprs, exprs...)
	}

	// Collect expressions from dashboard JSON files.
	dashFiles, err := filepath.Glob(filepath.Join(dashDir, "*.json"))
	if err != nil {
		return "", fmt.Errorf("globbing dashboard files: %w", err)
	}
	for _, f := range dashFiles {
		exprs, fErr := extractExprsFromDashboardJSON(f)
		if fErr != nil {
			return "", fErr
		}
		allExprs = append(allExprs, exprs...)
	}

	if len(allExprs) == 0 {
		return "", fmt.Errorf("no PromQL expressions found in %s or %s", alertDir, dashDir)
	}

	// Extract metric names from all expressions and deduplicate.
	seen := make(map[string]bool)
	for _, expr := range allExprs {
		for _, m := range extractMetricNamesFromExpr(expr) {
			seen[m] = true
		}
	}

	if len(seen) == 0 {
		return "", fmt.Errorf("no metric names could be extracted from PromQL expressions")
	}

	// Sort and join with pipe for use as a Prometheus regex.
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, "|"), nil
}
