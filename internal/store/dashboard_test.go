package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestDashboardQueries(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	now := time.Now().UTC()

	rows := []Review{
		{Repo: "o/a", PR: 1, Title: "Add auth", URL: "https://github.com/o/a/pull/1", HeadSHA: "s1", Verdict: "approve", Findings: 0, Files: 3, Additions: 40, Deletions: 2, DurationMS: 2100, CreatedAt: now.Add(-2 * time.Hour)},
		{Repo: "o/a", PR: 2, Title: "Fix parser", HeadSHA: "s2", Verdict: "changes", Findings: 4, Files: 2, Additions: 9, Deletions: 9, DurationMS: 5400, CreatedAt: now.Add(-26 * time.Hour)},
		{Repo: "o/b", PR: 7, Title: "Docs", HeadSHA: "s3", Verdict: "suggestions", Findings: 1, Files: 1, CreatedAt: now.Add(-3 * 24 * time.Hour)},
	}
	findingsJSON, _ := json.Marshal([]map[string]any{{"file": "x.go", "line": 4, "title": "bug"}})
	rows[1].FindingsJSON = string(findingsJSON)

	for i := range rows {
		if err := s.SaveReview(ctx, &rows[i]); err != nil {
			t.Fatalf("SaveReview: %v", err)
		}
	}
	if err := s.MarkMerged(ctx, "o/a", 1, "s1"); err != nil {
		t.Fatalf("MarkMerged: %v", err)
	}

	ov, err := s.Overview(ctx, 7)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if ov.Totals.Reviews != 3 || ov.Totals.Approve != 1 || ov.Totals.Changes != 1 ||
		ov.Totals.Suggestions != 1 || ov.Totals.Findings != 5 || ov.Totals.Repos != 2 ||
		ov.Totals.Merged != 1 || ov.Totals.Files != 6 {
		t.Fatalf("totals = %+v", ov.Totals)
	}
	if len(ov.Series) != 7 {
		t.Fatalf("series length = %d, want 7", len(ov.Series))
	}
	if len(ov.Recent) != 3 || ov.Recent[0].PR != 7 {
		t.Fatalf("recent should be newest-first, got %+v", ov.Recent)
	}

	list, total, err := s.ListReviews(ctx, ReviewFilter{Verdict: "changes", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 || list[0].PR != 2 {
		t.Fatalf("filtered list = %+v total=%d", list, total)
	}

	search, total, err := s.ListReviews(ctx, ReviewFilter{Query: "parser", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || search[0].Title != "Fix parser" {
		t.Fatalf("search = %+v total=%d", search, total)
	}

	detail, found, err := s.ReviewByID(ctx, list[0].ID)
	if err != nil || !found {
		t.Fatalf("ReviewByID = %v, %v", found, err)
	}
	if len(detail.ParseFindings()) != 1 {
		t.Fatalf("stored findings = %s", detail.FindingsJSON)
	}
	if detail.Merged {
		t.Error("only the first review was merged")
	}

	var merged *Review
	for _, item := range ov.Recent {
		if item.PR == 1 {
			found, _, err := s.ReviewByID(ctx, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			merged = found
		}
	}
	if merged == nil || !merged.Merged {
		t.Error("merged flag did not persist")
	}

	repos, err := s.RepoStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 || repos[0].Repo != "o/a" || repos[0].Reviews != 2 {
		t.Fatalf("repos = %+v", repos)
	}
	if repos[0].LastVerdict != "changes" {
		t.Errorf("last verdict = %q", repos[0].LastVerdict)
	}
}
