package store

import (
	"context"
	"testing"
)

func TestReviewContextColumnsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	in := &Review{
		Repo: "o/r", PR: 7, HeadSHA: "abc", Verdict: "approve",
		PromptTokens: 1234, CompletionTokens: 56,
		ContextFiles: 4, ContextChars: 3210, ContextMS: 87,
	}
	if err := s.SaveReview(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.ReviewByID(ctx, 1)
	if err != nil || !found {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	if got.PromptTokens != 1234 || got.CompletionTokens != 56 ||
		got.ContextFiles != 4 || got.ContextChars != 3210 || got.ContextMS != 87 {
		t.Fatalf("round trip = %+v", got)
	}

	in.HeadSHA = "def"
	in.ContextChars = 99
	if err := s.SaveReview(ctx, in); err != nil {
		t.Fatal(err)
	}
	latest, _, err := s.LatestReview(ctx, "o/r", 7)
	if err != nil {
		t.Fatal(err)
	}
	if latest.ContextChars != 99 || latest.HeadSHA != "def" {
		t.Fatalf("latest = %+v", latest)
	}
}
