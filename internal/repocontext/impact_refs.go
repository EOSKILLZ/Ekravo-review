package repocontext

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/diff"
)

// importBreakNotes reports exports removed from a changed file that another
// file still imports by name. The ES-module import record is exact, so these
// notes are stronger evidence than a bare identifier reference.
//
// The second return value names the symbols already explained here, so the
// caller does not emit a weaker duplicate reference note for them.
func (ix *Index) importBreakNotes(section []*diff.File, removed map[string]string, changed map[string]bool) ([]string, map[string]bool) {
	covered := map[string]bool{}
	if ix == nil || len(removed) == 0 {
		return nil, covered
	}
	names := make([]string, 0, len(removed))
	for name := range removed {
		names = append(names, name)
	}
	sort.Strings(names)

	var notes []string
	for _, name := range names {
		from := removed[name]
		if from == "" || !isESModulePath(from) {
			continue
		}
		importer, line := ix.firstImporter(from, name, changed)
		if importer == "" {
			continue
		}
		notes = append(notes, fmt.Sprintf(
			"- %s was removed from %s but %s:%d imports it", name, from, importer, line))
		covered[name] = true
	}
	return notes, covered
}

func isESModulePath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return true
	}
	return false
}

// firstImporter returns the first unchanged file that imports name from target.
func (ix *Index) firstImporter(target, name string, changed map[string]bool) (string, int) {
	for _, f := range ix.Files {
		if f == nil || f.Path == target || changed[f.Path] {
			continue
		}
		for _, imp := range f.Symbols.NamedImports {
			if imp.Name != name {
				continue
			}
			if ix.resolveImportPath(f.Path, imp.Source) == target {
				return f.Path, imp.Line
			}
		}
	}
	return "", 0
}

// resolveImportPath maps a relative module specifier to an indexed file,
// trying the extensions and index files bundlers resolve by default.
func (ix *Index) resolveImportPath(fromFile, spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" || !strings.HasPrefix(spec, ".") {
		return ""
	}
	base := path.Clean(path.Join(path.Dir(fromFile), spec))
	candidates := []string{base}
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".d.ts"} {
		candidates = append(candidates, base+ext)
	}
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx"} {
		candidates = append(candidates, path.Join(base, "index"+ext))
	}
	for _, c := range candidates {
		if _, ok := ix.byPath[c]; ok {
			return c
		}
	}
	return ""
}

// ifaceChangeNotes lists types outside the diff whose method set covers an
// interface the diff touches. It is a structural hint, not a type check: it
// names the declarations worth inspecting, never that the build will fail.
func (ix *Index) ifaceChangeNotes(section []*diff.File, changed map[string]bool) []string {
	if ix == nil || len(ix.methodSets) == 0 {
		return nil
	}
	var notes []string
	seen := map[string]bool{}
	for _, f := range section {
		if seen[f.Path] {
			continue
		}
		seen[f.Path] = true
		file := ix.File(f.Path)
		if file == nil {
			continue
		}
		for i := range file.Symbols.Defs {
			sym := &file.Symbols.Defs[i]
			if sym.Kind != "interface" || len(sym.Methods) == 0 {
				continue
			}
			if !sectionTouches(f, sym.Line, sym.EndLine) {
				continue
			}
			impl := ix.implementers(sym.Methods, changed)
			if len(impl) == 0 {
				continue
			}
			shown := impl
			if len(shown) > 3 {
				shown = shown[:3]
			}
			notes = append(notes, fmt.Sprintf(
				"- interface %s (%s:%d) has %d structural implementer(s) outside this diff: %s",
				sym.Name, f.Path, sym.Line, len(impl), strings.Join(shown, ", ")))
		}
	}
	return notes
}

// implementers returns types that declare every method the interface names,
// skipping types defined in files the diff already changes.
func (ix *Index) implementers(methods []string, changed map[string]bool) []string {
	need := make(map[string]bool, len(methods))
	for _, m := range methods {
		need[m] = true
	}
	var out []string
	for typeName, set := range ix.methodSets {
		def, ok := ix.typeDefs[typeName]
		if !ok || changed[def.Path] {
			continue
		}
		complete := true
		for m := range need {
			if !set[m] {
				complete = false
				break
			}
		}
		if complete {
			out = append(out, fmt.Sprintf("%s (%s:%d)", typeName, def.Path, def.Line))
		}
	}
	sort.Strings(out)
	if len(out) > 25 {
		out = out[:25]
	}
	return out
}

// sectionTouches reports whether a changed (non-context) line falls inside the
// interface's declaration, using the diff's own line numbering.
func sectionTouches(f *diff.File, start, end int) bool {
	if f == nil || start <= 0 || end < start {
		return false
	}
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == diff.Context {
				continue
			}
			line := l.NewNumber
			if l.Kind == diff.Removed {
				line = l.OldNumber
			}
			if line >= start && line <= end {
				return true
			}
		}
	}
	return false
}

// splitExportNames parses the body of an `export { a, b as c }` statement.
func splitExportNames(body string) []string {
	var out []string
	for _, part := range strings.Split(body, ",") {
		part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "type "))
		if part == "" {
			continue
		}
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		if name := fields[0]; name != "" && name != "*" && name != "type" {
			out = append(out, name)
		}
	}
	return out
}
