package app

import (
	"testing"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/githubx"
	"github.com/EOSKILLZ/ekravo-review/v2/internal/review"
)

func TestAutoMergeEligible(t *testing.T) {
	approve := review.Verdict{Decision: "approve"}
	changes := review.Verdict{Decision: "changes"}
	cases := []struct {
		name    string
		verdict review.Verdict
		partial bool
		want    bool
	}{
		{"complete approval", approve, false, true},
		{"partial approval", approve, true, false},
		{"findings", changes, false, false},
		{"partial findings", changes, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := autoMergeEligible(tc.verdict, tc.partial); got != tc.want {
				t.Fatalf("autoMergeEligible = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCanAutoMerge(t *testing.T) {
	yes, no := true, false
	newPR := func() *githubx.PullRequest {
		pr := &githubx.PullRequest{State: "open", Mergeable: &yes, MergeableState: "clean"}
		pr.Head.SHA = "abc"
		return pr
	}

	cases := []struct {
		name   string
		mutate func(*githubx.PullRequest)
		sha    string
		want   bool
	}{
		{"clean approved PR", func(*githubx.PullRequest) {}, "abc", true},
		{"closed", func(p *githubx.PullRequest) { p.State = "closed" }, "abc", false},
		{"draft", func(p *githubx.PullRequest) { p.Draft = true }, "abc", false},
		{"sha changed", func(*githubx.PullRequest) {}, "def", false},
		{"mergeable false", func(p *githubx.PullRequest) { p.Mergeable = &no }, "abc", false},
		{"mergeability unknown", func(p *githubx.PullRequest) { p.Mergeable = nil }, "abc", false},
		{"checks pending", func(p *githubx.PullRequest) { p.MergeableState = "unstable" }, "abc", false},
		{"blocked", func(p *githubx.PullRequest) { p.MergeableState = "blocked" }, "abc", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr := newPR()
			tc.mutate(pr)
			if got := canAutoMerge(pr, tc.sha); got != tc.want {
				t.Fatalf("canAutoMerge = %v, want %v", got, tc.want)
			}
		})
	}
}
