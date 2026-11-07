package review

import (
	"strings"
	"testing"
	"time"
)

func TestSummaryRendersGithubMarkdown(t *testing.T) {
	in := SummaryInput{
		Meta:     Meta{Number: 7, Repo: "o/r", Title: "Add widget", Author: "octocat", ChangedFiles: 2, Additions: 10, Deletions: 3},
		Findings: []Finding{{File: "app/main.go", Line: 2, Severity: High, Category: "security", Title: "Unsanitised input"}},
		Verdict:  Decide([]Finding{{Severity: High}}, "high"),
		Model:    "deepseek-api/deepseek-flash",
		Handle:   "ekravo",
		Commit:   "abcdef1234567890",
		Duration: 1400 * time.Millisecond,
		RepoURL:  "https://example.com/ekravo-review",
	}
	out := Summary(in)
	t.Logf("\n%s", out)

	for _, want := range []string{
		"## 🔎 Ekravo Review — 🔴 Changes requested",
		"> [!WARNING]",
		"| Files | Lines | Findings | Reviewed in |",
		"| 2 | +10 −3 | 1 | 1.4s |",
		"| 🟠 High | `app/main.go:2` |",
		"commit `abcdef1`",
		"@ekravo review",
		"[Ekravo Review](https://example.com/ekravo-review)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Review Review") {
		t.Errorf("product name is repeated in the title:\n%s", out)
	}
}

func TestSummaryApprovalCallout(t *testing.T) {
	out := Summary(SummaryInput{
		Meta:    Meta{Number: 1, Repo: "o/r", Title: "Docs"},
		Verdict: Decide(nil, "high"),
		Model:   "m",
		Handle:  "ekravo",
	})
	if !strings.Contains(out, "> [!TIP]") {
		t.Errorf("approval should use a TIP callout:\n%s", out)
	}
	if !strings.Contains(out, "**Approved**") {
		t.Errorf("approval is not stated in the callout:\n%s", out)
	}
	if !strings.Contains(out, "[Ekravo Review](https://github.com/ekravo/Ekravo-review)") {
		t.Errorf("footer should fall back to the canonical repository:\n%s", out)
	}
}

func TestInlineBodyKeepsFixVisible(t *testing.T) {
	out := InlineBody(Finding{
		File: "a.go", Line: 4, Severity: High, Category: "bug",
		Title: "Nil deref", Detail: "The map can be nil.", Fix: "Initialise it first.",
	})
	for _, want := range []string{"> 🟠 High · Bug", "**Nil deref**", "**Suggested fix**", "Initialise it first."} {
		if !strings.Contains(out, want) {
			t.Errorf("inline body missing %q:\n%s", want, out)
		}
	}
}
