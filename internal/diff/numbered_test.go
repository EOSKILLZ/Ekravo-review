package diff

import (
	"strings"
	"testing"
)

func TestRenderNumberedHunks(t *testing.T) {
	f, err := ParseFilePatch("a.go", "modified", "@@ -1,3 +1,3 @@\n a\n-b\n+c\n d")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := RenderNumberedHunks(f, f.Hunks)
	for _, want := range []string{
		"### a.go (+1/-1)",
		"    1 | a",
		"-     | b",
		"+   2 | c",
		"    3 | d",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}
