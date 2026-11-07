package diff

import (
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		path      string
		language  string
		role      string
		generated bool
	}{
		{"internal/app/app.go", "Go", "source", false},
		{"internal/app/app_test.go", "Go", "test", false},
		{".github/workflows/ci.yml", "YAML", "ci", false},
		{"web/app.min.js", "JavaScript", "source", true},
		{"package-lock.json", "JSON", "lockfile", true},
		{"migrations/0002_add_users.sql", "SQL", "migration", false},
		{"README.md", "Markdown", "docs", false},
		{"styles/app.css", "CSS", "style", false},
		{"scripts/deploy.sh", "Shell", "script", false},
		{"Dockerfile", "Dockerfile", "ci", false},
		{"config/settings.yaml", "YAML", "config", false},
		{"src/main.tsx", "TypeScript", "source", false},
		{"assets/logo.png", "text", "source", false},
	}

	for _, c := range cases {
		got := Classify(c.path)
		if got.Language != c.language || got.Role != c.role || got.Generated != c.generated {
			t.Errorf("Classify(%q) = %+v, want %s/%s generated=%v",
				c.path, got, c.language, c.role, c.generated)
		}
	}
}

func TestRepoContextSummarisesFiles(t *testing.T) {
	files := []*File{
		{Path: "go.mod", Meta: Classify("go.mod")},
		{Path: "internal/app/app.go", Meta: Classify("internal/app/app.go")},
		{Path: "internal/app/app_test.go", Meta: Classify("internal/app/app_test.go")},
		{Path: ".github/workflows/ci.yml", Meta: Classify(".github/workflows/ci.yml")},
	}
	got := RepoContext(files)
	for _, want := range []string{
		"Repository context",
		"Go (3)",
		"source (2)",
		"ci (1)",
		"Go modules",
		"GitHub Actions",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("context missing %q:\n%s", want, got)
		}
	}
}

func TestParseFilePatchFillsMetadata(t *testing.T) {
	f, err := ParseFilePatch("internal/diff/classify_test.go", "added", "@@ -1 +1 @@\n+a\n")
	if err != nil {
		t.Fatal(err)
	}
	if f.Meta.Language != "Go" || f.Meta.Role != "test" {
		t.Fatalf("meta = %+v", f.Meta)
	}
	if !strings.Contains(RenderNumberedHunks(f, f.Hunks), "Go · test") {
		t.Fatalf("header missing metadata:\n%s", RenderNumberedHunks(f, f.Hunks))
	}
}
