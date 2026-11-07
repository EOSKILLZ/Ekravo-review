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

	counts, err := s.ReviewCounts(ctx, "")
	if err != nil {
		t.Fatalf("ReviewCounts: %v", err)
	}
	if len(counts) != 3 || counts["approve"] != 1 || counts["changes"] != 1 || counts["suggestions"] != 1 {
		t.Fatalf("counts = %+v", counts)
	}
	scoped, err := s.ReviewCounts(ctx, "parser")
	if err != nil {
		t.Fatalf("ReviewCounts search: %v", err)
	}
	if len(scoped) != 1 || scoped["changes"] != 1 {
		t.Fatalf("scoped counts = %+v", scoped)
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

func TestPRStateTracking(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	rows := []Review{
		{Repo: "o/a", PR: 1, HeadSHA: "a1", Verdict: "approve", State: "open"},
		{Repo: "o/a", PR: 2, HeadSHA: "a2", Verdict: "approve", State: "open"},
		{Repo: "o/a", PR: 3, HeadSHA: "a3", Verdict: "changes", State: "open"},
		{Repo: "o/b", PR: 4, HeadSHA: "b4", Verdict: "approve"}, // legacy row
	}
	for i := range rows {
		if err := s.SaveReview(ctx, &rows[i]); err != nil {
			t.Fatalf("SaveReview: %v", err)
		}
	}

	unknown, err := s.UnknownPRStates(ctx, 10)
	if err != nil || len(unknown) != 1 || unknown[0].Repo != "o/b" || unknown[0].PR != 4 {
		t.Fatalf("unknown = %+v err=%v", unknown, err)
	}

	if err := s.SetPRState(ctx, "o/a", 2, "merged"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPRState(ctx, "o/a", 3, "closed"); err != nil {
		t.Fatal(err)
	}

	active, err := s.ActiveReviewCounts(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	// The legacy row (o/b #4) has no state yet, so it stays active until the
	// startup reconciler fills it in.
	if len(active) != 1 || active["approve"] != 2 {
		t.Fatalf("active counts = %+v", active)
	}

	states, err := s.ReviewStateCounts(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if states["open"] != 1 || states["merged"] != 1 || states["closed"] != 1 || states[""] != 1 {
		t.Fatalf("state counts = %+v", states)
	}

	list, _, err := s.ListReviews(ctx, ReviewFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	byPR := map[int]Review{}
	for _, r := range list {
		byPR[r.PR] = r
	}
	if byPR[2].State != "merged" || byPR[3].State != "closed" || byPR[1].State != "open" {
		t.Fatalf("states = %+v", byPR)
	}

	if err := s.MarkMerged(ctx, "o/a", 1, "a1"); err != nil {
		t.Fatal(err)
	}
	merged, found, err := s.LatestReview(ctx, "o/a", 1)
	if err != nil || !found || merged.State != "merged" {
		t.Fatalf("MarkMerged state = %+v found=%v err=%v", merged, found, err)
	}
}
