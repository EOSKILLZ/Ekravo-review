package review

import (
	"testing"

	"github.com/ekravo/Ekravo-review/v2/internal/diff"
)

const patch = "@@ -1,2 +1,4 @@\n context\n+added one\n+added two\n context two\n"

func files(t *testing.T) []*diff.File {
	t.Helper()
	f, err := diff.ParseFilePatch("app/main.go", "modified", patch)
	if err != nil {
		t.Fatal(err)
	}
	return []*diff.File{f}
}

func TestParseFindingsAcceptsModelVariants(t *testing.T) {
	cases := map[string]string{
		"envelope":      `{"findings":[{"file":"app/main.go","line":2,"severity":"high","category":"security","title":"t","fix":"f"}]}`,
		"bare array":    `[{"file":"app/main.go","line":2,"severity":"high","category":"security","title":"t","fix":"f"}]`,
		"issues key":    `{"issues":[{"path":"app/main.go","line":"2","severity":"HIGH","category":"BUG","summary":"t","suggestion":"f"}]}`,
		"nested review": `{"review":{"findings":[{"file":"app/main.go","line":2,"severity":"high","category":"bug","title":"t","fix":"f"}]}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseFindings([]byte(raw))
			if err != nil {
				t.Fatalf("ParseFindings: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("findings = %d, want 1", len(got))
			}
			if got[0].File != "app/main.go" || got[0].Line != 2 || got[0].Title == "" {
				t.Fatalf("parsed = %+v", got[0])
			}
		})
	}
}

func TestValidateDropsUnanchoredFindings(t *testing.T) {
	found := []Finding{
		{File: "app/main.go", Line: 2, Severity: High, Category: "security", Title: "real", Fix: "fix", Confidence: 0.9},
		{File: "app/main.go", Line: 999, Severity: Critical, Category: "bug", Title: "fabricated", Fix: "fix"},
		{File: "other/file.go", Line: 2, Severity: Critical, Category: "bug", Title: "wrong file", Fix: "fix"},
	}
	kept, dropped := Validate(found, files(t), 10)
	if len(kept) != 1 || kept[0].Title != "real" {
		t.Fatalf("kept = %+v", kept)
	}
	if len(dropped) != 2 {
		t.Fatalf("dropped = %+v", dropped)
	}
}

func TestValidateAnchorsContextLineToAddedLine(t *testing.T) {
	found := []Finding{{File: "app/main.go", Line: 4, Severity: Medium, Category: "bug", Title: "context line", Fix: "f"}}
	kept, _ := Validate(found, files(t), 10)
	if len(kept) != 1 {
		t.Fatalf("kept = %d", len(kept))
	}
	if kept[0].Line != 3 {
		t.Fatalf("line = %d, want anchored to 3", kept[0].Line)
	}
}

func TestValidateDeduplicates(t *testing.T) {
	found := []Finding{
		{File: "app/main.go", Line: 2, Severity: High, Category: "bug", Title: "same", Fix: "f"},
		{File: "app/main.go", Line: 2, Severity: High, Category: "bug", Title: "SAME", Fix: "f"},
	}
	kept, _ := Validate(found, files(t), 10)
	if len(kept) != 1 {
		t.Fatalf("kept = %d, want 1", len(kept))
	}
}

func TestDecideRespectsBlockOn(t *testing.T) {
	critical := []Finding{{Severity: Critical}, {Severity: Low}}

	if v := Decide(critical, "high"); v.Decision != "changes" || v.Blocking != 1 {
		t.Fatalf("high policy: %+v", v)
	}
	if v := Decide(critical, "none"); v.Decision != "suggestions" {
		t.Fatalf("none policy: %+v", v)
	}
	if v := Decide(nil, "high"); v.Decision != "approve" {
		t.Fatalf("clean: %+v", v)
	}
	low := []Finding{{Severity: Low}}
	if v := Decide(low, "high"); v.Decision != "suggestions" {
		t.Fatalf("low only: %+v", v)
	}
}

func TestReviewEventGating(t *testing.T) {
	if got := ReviewEvent("approve", false); got != "COMMENT" {
		t.Errorf("auto-approve off = %s", got)
	}
	if got := ReviewEvent("approve", true); got != "APPROVE" {
		t.Errorf("approve = %s", got)
	}
	if got := ReviewEvent("changes", true); got != "REQUEST_CHANGES" {
		t.Errorf("changes = %s", got)
	}
	if got := ReviewEvent("suggestions", true); got != "COMMENT" {
		t.Errorf("suggestions = %s", got)
	}
}

func TestSummaryIncludesVerdictAndFindings(t *testing.T) {
	in := SummaryInput{
		Meta:     Meta{Number: 3, Repo: "o/r", Title: "t", Base: "main", Head: "feat"},
		Findings: []Finding{{File: "app/main.go", Line: 2, Severity: High, Category: "security", Title: "Injection", Fix: "parameterise"}},
		Verdict:  Decide([]Finding{{Severity: High}}, "high"),
		Handle:   "owlbot",
		Model:    "cohere/north-mini-code:free",
	}
	out := Summary(in)
	for _, want := range []string{"o/r/pull/3", "Changes requested", "app/main.go:2", "@owlbot review", "cohere/north-mini-code:free"} {
		if !contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
