// Package k6report turns a k6 summary export (summary.json) into the results
// table in loadtest/RESULTS.md. Every value comes from the input. A cell that
// the input does not contain renders as "n/a" and is never estimated.
package k6report

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// NA is the cell text for a value that is absent from the input.
const NA = "n/a"

// Header is the markdown header line of the results table.
const Header = "| Endpoint | Requests | RPS | p50 ms | p95 ms | p99 ms | Error % | 429 % |"

// knownOrder lists the endpoint names produced by loadtest/k6/tenantly.js. They render first,
// in this order. Any other endpoint name in the input follows, sorted.
var knownOrder = []string{
	"list_projects",
	"get_project",
	"create_project",
	"graphql_projects",
	"cross_tenant_get",
}

// Row holds one line of the results table. A nil pointer means the input did not contain that value.
type Row struct {
	Endpoint   string
	Requests   *float64 // count of http_reqs
	RPS        *float64 // rate of http_reqs, per second
	P50        *float64 // ms, http_req_duration p(50)
	P95        *float64 // ms, http_req_duration p(95)
	P99        *float64 // ms, http_req_duration p(99)
	ErrorPct   *float64 // percent, http_req_failed rate * 100
	Rate429Pct *float64 // percent, rate_limited rate * 100
}

// Summary is the parsed content of a k6 summary export.
type Summary struct {
	// Total holds the untagged (all endpoints) metrics. Endpoint is "All endpoints".
	Total Row
	// Endpoints holds one row per distinct name tag, ordered as described in knownOrder.
	Endpoints []Row
	// ChecksPassPct is the checks rate in percent, or nil when absent.
	ChecksPassPct *float64
}

type metricJSON struct {
	Type   string             `json:"type"`
	Values map[string]float64 `json:"values"`
}

type summaryJSON struct {
	Metrics map[string]metricJSON `json:"metrics"`
}

// Parse reads a k6 summary export. It returns an error for invalid JSON or when the
// "metrics" object is missing or empty, because such input is not a k6 summary.
func Parse(r io.Reader) (Summary, error) {
	var raw summaryJSON
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return Summary{}, fmt.Errorf("k6report: decode summary: %w", err)
	}
	if len(raw.Metrics) == 0 {
		return Summary{}, errors.New("k6report: input has no metrics object; is this a k6 summary export?")
	}

	// Group submetrics by endpoint name. Keys look like "http_reqs{name:list_projects}".
	byName := map[string]map[string]metricJSON{}
	global := map[string]metricJSON{}
	for key, metric := range raw.Metrics {
		base, tags := splitKey(key)
		if tags == "" {
			global[base] = metric
			continue
		}
		name, ok := tagValue(tags, "name")
		if !ok || strings.HasPrefix(name, "setup_") {
			// setup_* names come from the setup() phase, which is not part of the results table.
			continue
		}
		if byName[name] == nil {
			byName[name] = map[string]metricJSON{}
		}
		byName[name][base] = metric
	}

	s := Summary{Total: rowFrom("All endpoints", global)}
	if m, ok := global["checks"]; ok {
		if v, ok := m.Values["rate"]; ok {
			pct := v * 100
			s.ChecksPassPct = &pct
		}
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	s.Endpoints = make([]Row, 0, len(names))
	for _, name := range orderNames(names) {
		s.Endpoints = append(s.Endpoints, rowFrom(name, byName[name]))
	}
	return s, nil
}

// rowFrom builds a row from the metrics of one endpoint. Missing metrics stay nil.
func rowFrom(endpoint string, metrics map[string]metricJSON) Row {
	row := Row{Endpoint: endpoint}
	if m, ok := metrics["http_reqs"]; ok {
		row.Requests = value(m, "count")
		row.RPS = value(m, "rate")
	}
	if m, ok := metrics["http_req_duration"]; ok {
		row.P50 = value(m, "p(50)")
		row.P95 = value(m, "p(95)")
		row.P99 = value(m, "p(99)")
	}
	if m, ok := metrics["http_req_failed"]; ok {
		row.ErrorPct = scaled(m, "rate", 100)
	}
	if m, ok := metrics["rate_limited"]; ok {
		row.Rate429Pct = scaled(m, "rate", 100)
	}
	return row
}

func value(m metricJSON, field string) *float64 {
	v, ok := m.Values[field]
	if !ok {
		return nil
	}
	return &v
}

func scaled(m metricJSON, field string, factor float64) *float64 {
	v, ok := m.Values[field]
	if !ok {
		return nil
	}
	out := v * factor
	return &out
}

// splitKey splits "metric{tags}" into the metric name and the tag text.
func splitKey(key string) (string, string) {
	i := strings.IndexByte(key, '{')
	if i < 0 || !strings.HasSuffix(key, "}") {
		return key, ""
	}
	return key[:i], key[i+1 : len(key)-1]
}

// tagValue reads one key from a tag list such as "name:list_projects,expected_response:true".
func tagValue(tags, want string) (string, bool) {
	for _, pair := range strings.Split(tags, ",") {
		k, v, ok := strings.Cut(pair, ":")
		if ok && k == want {
			return v, true
		}
	}
	return "", false
}

// orderNames puts the known endpoint names first, in knownOrder, and sorts the rest.
func orderNames(names []string) []string {
	present := map[string]bool{}
	for _, n := range names {
		present[n] = true
	}
	out := make([]string, 0, len(names))
	for _, k := range knownOrder {
		if present[k] {
			out = append(out, k)
			delete(present, k)
		}
	}
	rest := make([]string, 0, len(present))
	for n := range present {
		rest = append(rest, n)
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// RenderMarkdown renders the results table. Endpoint rows come first, then the all-endpoints row.
// Cells that the input did not contain show "n/a".
func RenderMarkdown(s Summary) string {
	var b strings.Builder
	b.WriteString(Header + "\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, r := range s.Endpoints {
		b.WriteString(renderRow(r) + "\n")
	}
	b.WriteString(renderRow(s.Total) + "\n")
	b.WriteString("\nChecks pass rate: " + fmtPct(s.ChecksPassPct) + "\n")
	return b.String()
}

func renderRow(r Row) string {
	cells := []string{
		r.Endpoint,
		fmtInt(r.Requests),
		fmtNum(r.RPS),
		fmtNum(r.P50),
		fmtNum(r.P95),
		fmtNum(r.P99),
		fmtPct(r.ErrorPct),
		fmtPct(r.Rate429Pct),
	}
	return "| " + strings.Join(cells, " | ") + " |"
}

func fmtInt(v *float64) string {
	if v == nil {
		return NA
	}
	return fmt.Sprintf("%.0f", *v)
}

func fmtNum(v *float64) string {
	if v == nil {
		return NA
	}
	return fmt.Sprintf("%.2f", *v)
}

func fmtPct(v *float64) string {
	if v == nil {
		return NA
	}
	return fmt.Sprintf("%.2f", *v)
}
