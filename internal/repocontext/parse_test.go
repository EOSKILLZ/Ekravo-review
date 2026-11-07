package repocontext

import (
	"strings"
	"testing"
)

func TestParseGoExtractsDefinitionsRefsAndImports(t *testing.T) {
	src := `package demo

import (
	"fmt"
	"strings"
)

const Limit = 10

var count = 1

type Store struct {
	name string
}

func Add(a, b int) int {
	fmt.Println(strings.TrimSpace("x"))
	return a + b + count
}

func (s *Store) Get(id int) string {
	return fmt.Sprint(id, Add(1, 2), Limit)
}
`
	fs, err := Parse("demo.go", []byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	if fs.Lang != "go" {
		t.Fatalf("lang = %q", fs.Lang)
	}
	want := map[string]string{"Add": "function", "Store": "type", "Get": "method", "Limit": "const", "count": "var"}
	for name, kind := range want {
		found := false
		for _, d := range fs.Defs {
			if d.Name == name {
				found = true
				if d.Kind != kind {
					t.Errorf("%s kind = %q, want %q", name, d.Kind, kind)
				}
				if d.Line == 0 || d.EndLine < d.Line {
					t.Errorf("%s has bad range %d-%d", name, d.Line, d.EndLine)
				}
			}
		}
		if !found {
			t.Errorf("missing definition %s", name)
		}
	}
	for _, imp := range []string{"fmt", "strings"} {
		found := false
		for _, got := range fs.Imports {
			if got == imp {
				found = true
			}
		}
		if !found {
			t.Errorf("missing import %s in %v", imp, fs.Imports)
		}
	}
	refs := 0
	for _, r := range fs.Refs {
		if r.Name == "TrimSpace" || r.Name == "Sprint" {
			refs++
		}
	}
	if refs < 2 {
		t.Fatalf("expected qualified refs, got %d refs", len(fs.Refs))
	}
}

func TestParseTypeScriptExtractsSymbols(t *testing.T) {
	src := `import { helper } from "./helper";

export function add(a: number, b: number): number {
  return helper(a) + b;
}

export class Box {
  value = 1;
  get(): number { return this.value; }
}
`
	fs, err := Parse("box.ts", []byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, d := range fs.Defs {
		names[d.Name] = true
	}
	if !names["add"] {
		t.Fatalf("missing add in %+v", fs.Defs)
	}
	if !names["Box"] {
		t.Fatalf("missing Box in %+v", fs.Defs)
	}
	if len(fs.Refs) == 0 {
		t.Fatal("expected references")
	}
}

func TestParseRegexFallback(t *testing.T) {
	fs, err := Parse("script.zzz", []byte("function HelloWorld() { return Helper(); }\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs.Defs) == 0 || fs.Defs[0].Name != "HelloWorld" {
		t.Fatalf("defs = %+v", fs.Defs)
	}
}

func TestSimHashGroupsSimilarBodies(t *testing.T) {
	a := SimHash("func Add(a int, b int) int { return a + b }")
	b := SimHash("func Add(a int, b int) int { return a + b }")
	c := SimHash("class Renderer { paint(canvas) { canvas.fillRect(0, 0, 10, 10) } }")
	if Hamming(a, b) != 0 {
		t.Fatalf("identical bodies differ: %d", Hamming(a, b))
	}
	if Hamming(a, c) <= 6 {
		t.Fatalf("unrelated bodies too close: %d", Hamming(a, c))
	}
}

func TestCleanSignatureCollapsesWhitespace(t *testing.T) {
	got := cleanSignature("func Add(a, b int)\n\tint {")
	if strings.Contains(got, "\n") || !strings.Contains(got, "func Add") {
		t.Fatalf("signature = %q", got)
	}
}
