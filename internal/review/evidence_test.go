package review

import (
	"strings"
	"testing"
)

func TestNeedsEvidenceOnlyForExternalHighImpactClaims(t *testing.T) {
	cases := []struct {
		name string
		f    Finding
		want bool
	}{
		{"missing api", Finding{Severity: High, Title: "lodash.get does not exist in lodash 3"}, true},
		{"cve", Finding{Severity: Critical, Detail: "This version has CVE-2025-1234"}, true},
		{"deprecated", Finding{Severity: High, Detail: "the option is deprecated since v2"}, true},
		{"local nil deref", Finding{Severity: High, Title: "nil map write panics"}, false},
		{"low severity", Finding{Severity: Low, Title: "method does not exist"}, false},
		{"style", Finding{Severity: Medium, Title: "rename this variable"}, false},
	}
	for _, c := range cases {
		if got := NeedsEvidence(c.f); got != c.want {
			t.Errorf("%s: NeedsEvidence = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestParseResearchPlanFiltersAndDeduplicates(t *testing.T) {
	raw := []byte(`{"queries":[
		{"index":1,"file":"a.go","line":4,"query":"node 20 fs.readFileSync exists"},
		{"index":2,"file":"a.go","line":5,"query":"Node 20 FS.READFILESYNC EXISTS"},
		{"index":3,"file":"a.go","line":6,"query":"short"},
		{"index":4,"file":"a.go","line":7,"query":""}
	]}`)
	queries, err := ParseResearchPlan(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 {
		t.Fatalf("queries = %+v, want 1", queries)
	}
	if queries[0].Index != 1 {
		t.Errorf("index = %d", queries[0].Index)
	}
}

func TestParseEvidenceVerdict(t *testing.T) {
	v, err := ParseEvidenceVerdict([]byte(`{"verdict":"CONFIRM","note":"Docs list it."}`))
	if err != nil {
		t.Fatal(err)
	}
	if v.Verdict != "confirm" || v.Note != "Docs list it." {
		t.Fatalf("verdict = %+v", v)
	}

	if _, err := ParseEvidenceVerdict([]byte(`{"verdict":"maybe"}`)); err == nil {
		t.Fatal("expected unknown verdict error")
	}
}

func TestMarkVerifiedCapsSourcesAndRendersCitations(t *testing.T) {
	f := Finding{
		File: "a.go", Line: 3, Severity: High, Category: "bug",
		Title: "API missing", Detail: "detail", Fix: "fix",
	}
	MarkVerified(&f, Evidence{
		Query: "q",
		Sources: []Source{
			{Title: "One", URL: "https://example.com/1"},
			{Title: "Two", URL: "https://example.com/2"},
			{Title: "Three", URL: "https://example.com/3"},
			{Title: "Four", URL: "https://example.com/4"},
		},
	}, "The docs confirm it.")

	if len(f.Evidence.Sources) != 3 {
		t.Fatalf("sources = %d, want 3", len(f.Evidence.Sources))
	}
	body := InlineBody(f)
	for _, want := range []string{"🔗 Sources", "The docs confirm it.", "https://example.com/3"} {
		if !strings.Contains(body, want) {
			t.Errorf("inline body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "example.com/4") {
		t.Error("inline body leaked more than three sources")
	}
}
