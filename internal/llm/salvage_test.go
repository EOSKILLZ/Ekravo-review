package llm

import (
	"encoding/json"
	"testing"
)

func TestExtractJSONSalvagesTruncatedArray(t *testing.T) {
	raw := `{
  "findings": [
    {"file": "a.go", "line": 12, "title": "SQL injection", "severity": "critical"},
    {"file": "b.go", "line": 7, "title": "off-by-one", "severity": "high"},
    {"file": "c.go", "line": 3, "title": "cut off here`

	got, err := ExtractJSON(raw)
	if err != nil {
		t.Fatalf("expected salvage, got error: %v", err)
	}
	var out struct {
		Findings []struct {
			File string `json:"file"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("salvaged JSON invalid: %v\n%s", err, got)
	}
	if len(out.Findings) != 2 {
		t.Fatalf("expected 2 complete findings, got %d: %s", len(out.Findings), got)
	}
	if out.Findings[0].File != "a.go" || out.Findings[1].File != "b.go" {
		t.Fatalf("wrong findings: %s", got)
	}
}

func TestExtractJSONDoesNotSalvageProse(t *testing.T) {
	if _, err := ExtractJSON("I could not find any issues."); err == nil {
		t.Fatal("expected an error for plain prose")
	}
}
