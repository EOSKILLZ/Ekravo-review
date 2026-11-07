package repocontext

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/ekravo/Ekravo-review/v2/internal/diff"
)

func newTestIndex(t *testing.T, files map[string]string) *Index {
	t.Helper()
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	inputs := make([]FileInput, 0, len(paths))
	for _, p := range paths {
		inputs = append(inputs, FileInput{Path: p, Source: []byte(files[p])})
	}
	ix, _ := (&Builder{Opts: DefaultOptions()}).Build(context.Background(), "acme/app", "base", inputs, "tarball")
	if ix == nil || len(ix.Files) != len(files) {
		t.Fatalf("index built %d of %d files", len(ix.Files), len(files))
	}
	return ix
}

func TestImpactsReportInterfaceImplementers(t *testing.T) {
	ix := newTestIndex(t, map[string]string{
		"store.go": "package app\n\ntype Store interface {\n\tGet(id string) error\n\tPut(id string) error\n}\n",
		"mem.go": "package app\n\ntype Mem struct{}\n\n" +
			"func (m *Mem) Get(id string) error { return nil }\n\n" +
			"func (m *Mem) Put(id string) error { return nil }\n",
	})

	section := []*diff.File{{
		Path: "store.go",
		Hunks: []diff.Hunk{{Lines: []diff.Line{
			{Kind: diff.Removed, Content: "\tPut(id string) error", OldNumber: 6},
			{Kind: diff.Added, Content: "\tDelete(id string) error", NewNumber: 6},
		}}},
	}}

	got := ix.Impacts(section)
	if !strings.Contains(got, "interface Store") {
		t.Fatalf("missing interface note:\n%s", got)
	}
	if !strings.Contains(got, "1 structural implementer") || !strings.Contains(got, "Mem (mem.go:3)") {
		t.Fatalf("missing implementer detail:\n%s", got)
	}
}

func TestImpactsReportBrokenNamedImport(t *testing.T) {
	ix := newTestIndex(t, map[string]string{
		"src/a.ts": "export function speak(name: string) {\n  return name;\n}\n\nexport const LIMIT = 3;\n",
		"src/b.ts": "import { speak } from './a';\n\nexport function greet() {\n  return speak('hi');\n}\n",
	})

	section := []*diff.File{{
		Path: "src/a.ts",
		Hunks: []diff.Hunk{{Lines: []diff.Line{
			{Kind: diff.Removed, Content: "export function speak(name: string) {", OldNumber: 1},
		}}},
	}}

	got := ix.Impacts(section)
	want := "speak was removed from src/a.ts but src/b.ts:1 imports it"
	if !strings.Contains(got, want) {
		t.Fatalf("missing import note %q:\n%s", want, got)
	}
	if strings.Count(got, "speak") != 1 {
		t.Fatalf("expected a single note per symbol:\n%s", got)
	}
}

func TestImpactsIgnorePackageImports(t *testing.T) {
	ix := newTestIndex(t, map[string]string{
		"src/a.ts": "export function speak() {}\n",
		"src/b.ts": "import { speak } from 'speech-lib';\n\nexport const x = speak;\n",
	})

	section := []*diff.File{{
		Path: "src/a.ts",
		Hunks: []diff.Hunk{{Lines: []diff.Line{
			{Kind: diff.Removed, Content: "export function speak() {}", OldNumber: 1},
		}}},
	}}

	got := ix.Impacts(section)
	if strings.Contains(got, "imports it") {
		t.Fatalf("package import should not be treated as a local import:\n%s", got)
	}
}

func TestImpactsUntouchedInterfaceStaysQuiet(t *testing.T) {
	ix := newTestIndex(t, map[string]string{
		"store.go": "package app\n\ntype Store interface {\n\tGet(id string) error\n}\n\nfunc Use(s Store) error { return s.Get(\"x\") }\n",
		"mem.go":   "package app\n\ntype Mem struct{}\n\nfunc (m *Mem) Get(id string) error { return nil }\n",
	})

	section := []*diff.File{{
		Path: "store.go",
		Hunks: []diff.Hunk{{Lines: []diff.Line{
			{Kind: diff.Added, Content: "func New() Store { return Mem{} }", NewNumber: 8},
		}}},
	}}

	if got := ix.Impacts(section); strings.Contains(got, "structural implementer") {
		t.Fatalf("interface body was untouched:\n%s", got)
	}
}

func TestResolveImportPath(t *testing.T) {
	ix := newTestIndex(t, map[string]string{
		"src/a.ts":         "export const a = 1;\n",
		"src/lib/index.ts": "export const b = 2;\n",
	})

	if got := ix.resolveImportPath("src/b.ts", "./a"); got != "src/a.ts" {
		t.Fatalf("extensionless import resolved to %q", got)
	}
	if got := ix.resolveImportPath("src/b.ts", "./lib"); got != "src/lib/index.ts" {
		t.Fatalf("directory import resolved to %q", got)
	}
	if got := ix.resolveImportPath("src/b.ts", "react"); got != "" {
		t.Fatalf("package import resolved to %q", got)
	}
	if got := ix.resolveImportPath("src/b.ts", "../outside/a"); got != "" {
		t.Fatalf("missing file resolved to %q", got)
	}
}
