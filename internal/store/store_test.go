package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestReviewLifecycle(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	ok, err := s.IsReviewed(ctx, "o/r", 1, "sha1")
	if err != nil || ok {
		t.Fatalf("IsReviewed before save = %v, %v", ok, err)
	}

	if err := s.SaveReview(ctx, &Review{
		Repo: "o/r", PR: 1, HeadSHA: "sha1", ReviewID: 22,
		Verdict: "changes", Findings: 2, Model: "m",
	}); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}

	ok, err = s.IsReviewed(ctx, "o/r", 1, "sha1")
	if err != nil || !ok {
		t.Fatalf("IsReviewed after save = %v, %v", ok, err)
	}

	latest, found, err := s.LatestReview(ctx, "o/r", 1)
	if err != nil || !found {
		t.Fatalf("LatestReview = %v, %v", found, err)
	}
	if latest.ReviewID != 22 || latest.Verdict != "changes" || latest.Findings != 2 {
		t.Fatalf("latest = %+v", latest)
	}
}

func TestSaveReviewIsIdempotentPerCommit(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, id := range []int64{1, 2} {
		if err := s.SaveReview(ctx, &Review{Repo: "o/r", PR: 1, HeadSHA: "sha", ReviewID: id}); err != nil {
			t.Fatal(err)
		}
	}
	latest, _, err := s.LatestReview(ctx, "o/r", 1)
	if err != nil {
		t.Fatal(err)
	}
	if latest.ReviewID != 2 {
		t.Fatalf("review id = %d, want 2", latest.ReviewID)
	}
}

func TestImportLegacy(t *testing.T) {
	s := open(t)
	path := filepath.Join(t.TempDir(), ".processed_prs.json")
	data := `{"o/r":{"1":"abc","2":"def"},"o/old":[5,6]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	n, err := s.ImportLegacy(context.Background(), path)
	if err != nil {
		t.Fatalf("ImportLegacy: %v", err)
	}
	if n != 4 {
		t.Fatalf("imported = %d, want 4", n)
	}

	ok, err := s.IsReviewed(context.Background(), "o/r", 1, "abc")
	if err != nil || !ok {
		t.Fatalf("legacy sha not imported: %v, %v", ok, err)
	}
}
