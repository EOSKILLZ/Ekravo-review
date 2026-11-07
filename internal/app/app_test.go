package app

import (
	"testing"

	"github.com/ekravo/Ekravo-review/v2/internal/diff"
	"github.com/ekravo/Ekravo-review/v2/internal/githubx"
)

func TestIgnoredMatchesPathsAndBasenames(t *testing.T) {
	patterns := []string{"dist/*", "package-lock.json", "*.min.js", "*.lock"}
	cases := map[string]bool{
		"dist/app.js":              true,
		"package-lock.json":        true,
		"vendor/package-lock.json": true,
		"src/app.min.js":           true,
		"yarn.lock":                true,
		"src/app.js":               false,
		"src/dist/app.js":          false,
	}
	for name, want := range cases {
		if got := ignored(name, patterns); got != want {
			t.Errorf("ignored(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestTrimLinesCapsAndRecomputesCounts(t *testing.T) {
	f, err := diff.ParseFilePatch("a.go", "modified",
		"@@ -1,1 +1,6 @@\n ctx\n+a\n+b\n+c\n+d\n+e\n")
	if err != nil {
		t.Fatal(err)
	}
	trimLines(f, 4)

	total := 0
	for _, h := range f.Hunks {
		total += len(h.Lines)
		if h.NewCount != len(h.Lines) {
			t.Errorf("NewCount = %d, lines = %d", h.NewCount, len(h.Lines))
		}
	}
	if total != 4 {
		t.Fatalf("kept %d lines, want 4", total)
	}
}

func TestMetaOfMapsPullRequest(t *testing.T) {
	pr := &githubx.PullRequest{
		Number: 12, Title: "Add cache", Body: "body", Commits: 3,
		Additions: 10, Deletions: 2, ChangedFiles: 4,
	}
	pr.User.Login = "dev"
	pr.Base.Ref = "main"
	pr.Base.Repo.FullName = "o/r"
	pr.Head.Ref = "feat"
	pr.Labels = append(pr.Labels, struct {
		Name string `json:"name"`
	}{Name: "backend"})

	meta := metaOf(pr)
	if meta.Number != 12 || meta.Repo != "o/r" || meta.Base != "main" || meta.Head != "feat" {
		t.Fatalf("meta = %+v", meta)
	}
	if len(meta.Labels) != 1 || meta.Labels[0] != "backend" {
		t.Fatalf("labels = %v", meta.Labels)
	}
}
