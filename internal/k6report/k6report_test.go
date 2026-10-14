package k6report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadFixture(t *testing.T) Summary {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "summary_fixture.json"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	s, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse(fixture): %v", err)
	}
	return s
}

// Expected output for testdata/summary_fixture.json. The values are synthetic.
var fixtureLines = []string{
	"| Endpoint | Requests | RPS | p50 ms | p95 ms | p99 ms | Error % | 429 % |",
	"|---|---|---|---|---|---|---|---|",
	"| list_projects | 700 | 23.30 | 10.00 | 150.00 | 210.00 | 0.00 | 0.00 |",
	"| get_project | 200 | 6.67 | 8.00 | 120.00 | 190.00 | 0.50 | 0.00 |",
	"| create_project | 100 | 3.33 | 20.00 | 280.00 | 295.00 | 0.00 | 2.00 |",
	"| graphql_projects | n/a | n/a | n/a | n/a | n/a | 0.00 | n/a |",
	"| cross_tenant_get | 1000 | 33.33 | 7.00 | 60.00 | 90.00 | 0.00 | 0.00 |",
	"| All endpoints | 2200 | 73.33 | 9.00 | 210.00 | 280.00 | 0.23 | 0.91 |",
	"",
	"Checks pass rate: 99.50",
	"",
}

func TestRenderMarkdownFixtureRows(t *testing.T) {
	got := RenderMarkdown(loadFixture(t))
	want := strings.Join(fixtureLines, "\n")
	if got != want {
		t.Fatalf("rendered markdown mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestParseEndpointNamesAndOrder(t *testing.T) {
	s := loadFixture(t)
	var names []string
	for _, r := range s.Endpoints {
		names = append(names, r.Endpoint)
	}
	want := []string{"list_projects", "get_project", "create_project", "graphql_projects", "cross_tenant_get"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("endpoint order = %v, want %v", names, want)
	}
	for _, r := range s.Endpoints {
		if r.Endpoint == "setup_dev_token" {
			t.Fatal("setup_* names must not appear as endpoint rows")
		}
	}
}

func TestMissingCellsRenderNA(t *testing.T) {
	in := `{"metrics":{"http_reqs":{"type":"counter","values":{"count":5,"rate":1}}}}`
	s, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(s.Endpoints) != 0 {
		t.Fatalf("expected no endpoint rows, got %d", len(s.Endpoints))
	}
	got := RenderMarkdown(s)
	wantRow := "| All endpoints | 5 | 1.00 | n/a | n/a | n/a | n/a | n/a |"
	if !strings.Contains(got, wantRow+"\n") {
		t.Fatalf("missing row %q in:\n%s", wantRow, got)
	}
	if !strings.HasSuffix(got, "Checks pass rate: n/a\n") {
		t.Fatalf("checks line should be n/a, got:\n%s", got)
	}
}

func TestParseRejectsInvalidInput(t *testing.T) {
	cases := map[string]string{
		"invalid json":  `{"metrics":`,
		"no metrics":    `{}`,
		"empty metrics": `{"metrics":{}}`,
		"not an object": `[1,2,3]`,
	}
	for name, in := range cases {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}
