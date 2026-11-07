package repocontext

import (
	"context"
	"strings"
	"testing"

	"github.com/ekravo/Ekravo-review/v2/internal/diff"
)

func buildTestIndex(t *testing.T) *Index {
	t.Helper()
	files := []FileInput{
		{Path: "changed.go", Source: []byte("package demo\n\nfunc Target(v int) int { return v + 1 }\n")},
		{Path: "caller.go", Source: []byte("package demo\n\nfunc Caller() int {\n\treturn Target(41)\n}\n")},
		{Path: "helper.go", Source: []byte("package demo\n\nfunc Helper() string { return \"h\" }\n")},
		{Path: "changed_test.go", Source: []byte("package demo\n\nfunc TestTarget(t *testing.T) { _ = Target(1) }\n")},
	}
	ix, _ := (&Builder{Opts: DefaultOptions()}).Build(context.Background(), "o/r", "sha", files, "test")
	if ix == nil || len(ix.Files) != 4 {
		t.Fatalf("index = %+v", ix)
	}
	return ix
}

func testSection(t *testing.T) []*diff.File {
	t.Helper()
	patch := "@@ -1,3 +1,4 @@\n package demo\n \n-func Target(v int) int { return v }\n+func Target(v int) int { return v + 1 }\n+// padding padding padding padding padding padding padding padding padding padding\n"
	f, err := diff.ParseFilePatch("changed.go", "modified", patch)
	if err != nil {
		t.Fatal(err)
	}
	return []*diff.File{f}
}

func TestRetrieveFindsCallerAndExcludesChangedFile(t *testing.T) {
	ix := buildTestIndex(t)
	opts := DefaultOptions()
	opts.MinDiffChars = 0
	text, stats := ix.Retrieve(testSection(t), opts)
	if text == "" {
		t.Fatal("expected context")
	}
	if !strings.Contains(text, "caller.go") {
		t.Fatalf("missing caller:\n%s", text)
	}
	if strings.Contains(text, "### reference: changed.go") {
		t.Fatalf("changed file leaked into context:\n%s", text)
	}
	if stats.Files == 0 || stats.Chars != len(text) {
		t.Fatalf("stats = %+v", stats)
	}
	if !strings.Contains(text, "reference:") {
		t.Fatalf("missing validated header:\n%s", text)
	}
}

func TestImpactsReportRemovedReferencedSymbol(t *testing.T) {
	ix := buildTestIndex(t)
	patch := "@@ -1,2 +1,1 @@\n package demo\n-func Target(v int) int { return v }\n"
	f, err := diff.ParseFilePatch("changed.go", "modified", patch)
	if err != nil {
		t.Fatal(err)
	}
	text := ix.Impacts([]*diff.File{f})
	if !strings.Contains(text, "PR impact") || !strings.Contains(text, "Target") ||
		!strings.Contains(text, "caller.go") {
		t.Fatalf("impact = %q", text)
	}
}

func TestImpactsDetectArityChange(t *testing.T) {
	ix := buildTestIndex(t)
	patch := "@@ -1,2 +1,2 @@\n package demo\n-func Target(v int) int { return v }\n+func Target(v int, extra string) int { return v }\n"
	f, err := diff.ParseFilePatch("changed.go", "modified", patch)
	if err != nil {
		t.Fatal(err)
	}
	text := ix.Impacts([]*diff.File{f})
	if strings.Contains(text, "was removed") {
		t.Fatalf("signature change misreported as removal: %q", text)
	}
	if !strings.Contains(text, "signature changed from 1 to 2") ||
		!strings.Contains(text, "caller.go:4 passes 1") {
		t.Fatalf("arity impact = %q", text)
	}
}

func TestRetrieveSkipsTinyDiff(t *testing.T) {
	ix := buildTestIndex(t)
	f, err := diff.ParseFilePatch("changed.go", "modified", "@@ -1,1 +1,1 @@\n-a\n+b\n")
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	text, _ := ix.Retrieve([]*diff.File{f}, opts)
	if text != "" {
		t.Fatalf("tiny diff should skip context, got %q", text)
	}
}

func TestRenderSnippetRejectsMissingFile(t *testing.T) {
	if _, ok := renderSnippet(nil, nil, 1, true, 20); ok {
		t.Fatal("nil file should not render")
	}
	f := &IndexFile{Path: "a.go", Source: []byte("one\ntwo\n")}
	if _, ok := renderSnippet(f, &Symbol{Name: "x", Line: 99}, 0, true, 20); ok {
		t.Fatal("out-of-range line should not render")
	}
}

func TestPageRankFavorsPersonalizedFile(t *testing.T) {
	ix := &Index{
		Files: []*IndexFile{{Path: "a"}, {Path: "b"}, {Path: "c"}},
		graph: map[int]map[int]float64{
			1: {0: 1},
			2: {0: 1},
			0: {1: 1},
		},
	}
	rank := ix.pageRank(map[int]float64{0: 100}, 20)
	if len(rank) != 3 {
		t.Fatalf("rank len = %d", len(rank))
	}
	if rank[0] <= rank[2] {
		t.Fatalf("personalized node not ranked first: %v", rank)
	}
}
