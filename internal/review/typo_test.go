package review

import (
	"testing"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/diff"
)

func TestValidateDropsSelfContradictoryTypo(t *testing.T) {
	f, err := diff.ParseFilePatch("package.json", "modified", "@@ -1,1 +1,1 @@\n-  \"x\": 1\n+  \"x\": 2")
	if err != nil {
		t.Fatal(err)
	}
	findings := []Finding{{
		File: "package.json", Line: 1, Severity: High, Category: "bug",
		Title: "Typo in package name: 'puppeteer' instead of 'puppeteer'",
	}}
	kept, dropped := Validate(findings, []*diff.File{f}, 25)
	if len(kept) != 0 {
		t.Fatalf("expected the finding to be dropped, got %d", len(kept))
	}
	if len(dropped) != 1 || dropped[0].Reason != "claim compares identical names" {
		t.Fatalf("unexpected drop: %+v", dropped)
	}
}
