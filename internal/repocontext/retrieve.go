package repocontext

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/ekravo/Ekravo-review/v2/internal/diff"
)

var symbolFromLine = regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?(?:func|def|function|class|interface|struct|type|trait|object|module|const|let|var|enum)\s+\(?([A-Za-z_$][\w$]*)`)

var exportList = regexp.MustCompile(`^\s*export\s+(?:type\s+)?\{([^}]*)\}`)

var funcSignature = regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?(?:func|def|function)\s+(?:\([^)]*\)\s*)?([A-Za-z_$][\w$]*)\s*\(([^)]*)\)`)

var refIdent = regexp.MustCompile(`[A-Za-z_$][\w$]{2,}`)

type candidate struct {
	file  int
	sym   *Symbol
	ref   *Ref
	kind  string // defines | calls | similar | test
	score float64
}

// Retrieve ranks unchanged repository code for one diff section and renders it
// within budget. Changed files are excluded: the diff already contains them.
func (ix *Index) Retrieve(section []*diff.File, opts Options) (string, Stats) {
	stats := Stats{Mode: "on"}
	if ix == nil || len(section) == 0 {
		return "", stats
	}

	changed := map[string]bool{}
	addedNames := map[string]bool{}
	removedNames := map[string]bool{}
	signals := map[string]bool{}

	for _, f := range section {
		changed[f.Path] = true
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				switch l.Kind {
				case diff.Added:
					for _, m := range refIdent.FindAllString(l.Content, -1) {
						signals[m] = true
					}
				case diff.Removed:
					if m := symbolFromLine.FindStringSubmatch(l.Content); m != nil {
						removedNames[m[1]] = true
						signals[m[1]] = true
					}
				}
			}
		}
		if syms, ok := ix.bySymbolPath(f.Path); ok {
			for _, s := range syms {
				addedNames[s.Name] = true
				signals[s.Name] = true
			}
		}
	}
	for name := range signals {
		if len(name) < 3 || stopWords[strings.ToLower(name)] {
			delete(signals, name)
		}
	}
	if len(signals) > 120 {
		signals = topSignals(signals, 120)
	}

	cands := map[string]*candidate{}
	add := func(c *candidate) {
		if c == nil {
			return
		}
		f := ix.Files[c.file]
		if f == nil || changed[f.Path] {
			return
		}
		key := fmt.Sprintf("%s:%s:%s", f.Path, c.kind, c.name())
		if old, ok := cands[key]; ok {
			old.score += c.score
			return
		}
		cands[key] = c
	}

	for name := range signals {
		defs := ix.Definitions(name)
		for i, d := range defs {
			if i >= 3 {
				break
			}
			add(&candidate{file: d.File, sym: d.Sym, kind: "defines", score: 50})
		}
		for i, r := range ix.References(name) {
			if i >= 6 {
				break
			}
			if changed[ix.Files[r.File].Path] {
				continue
			}
			score := 0.0
			switch {
			case removedNames[name]:
				score = 60
			case addedNames[name]:
				score = 55
			default:
				score = 20
			}
			add(&candidate{file: r.File, ref: r.Ref, kind: "calls", score: score})
		}
	}

	// Tests that cover a changed file.
	for p := range changed {
		for _, ti := range ix.TestsFor(p) {
			if ix.Files[ti] == nil || changed[ix.Files[ti].Path] {
				continue
			}
			line := 1
			if len(ix.Files[ti].Symbols.Defs) > 0 {
				line = ix.Files[ti].Symbols.Defs[0].Line
			}
			add(&candidate{file: ti, ref: &Ref{Name: ix.Files[ti].Path, Line: line}, kind: "test", score: 35})
		}
	}

	// Same-directory neighbours, only when they are small and structurally close.
	for p := range changed {
		dir := path.Dir(p)
		count := 0
		for i, f := range ix.Files {
			if count >= 2 {
				break
			}
			if f == nil || changed[f.Path] || path.Dir(f.Path) != dir || f.Test {
				continue
			}
			if len(f.Symbols.Defs) == 0 {
				continue
			}
			add(&candidate{file: i, sym: &f.Symbols.Defs[0], kind: "neighbour", score: 20})
			count++
		}
	}

	// Imports added by the changed files often name the exact contract.
	for _, f := range section {
		fi := ix.File(f.Path)
		if fi == nil {
			continue
		}
		for _, i := range ix.ImportedFiles(fi.Symbols.Imports) {
			if len(ix.Files[i].Symbols.Defs) == 0 {
				continue
			}
			add(&candidate{file: i, sym: &ix.Files[i].Symbols.Defs[0], kind: "import", score: 40})
		}
	}

	if opts.SimHash {
		ix.addSimilar(cands, changed, section, add)
	}

	// Personalized PageRank biases the ranking toward the changed files.
	personal := map[int]float64{}
	for p := range changed {
		if i, ok := ix.byPath[p]; ok {
			personal[i] = 100
		}
	}
	rank := ix.pageRank(personal, 20)
	maxRank := 0.0
	for _, r := range rank {
		if r > maxRank {
			maxRank = r
		}
	}
	for _, c := range cands {
		if c.file < len(rank) && maxRank > 0 {
			c.score += 25 * rank[c.file] / maxRank
		}
	}

	// Prefer entry points; PageRank under-ranks them by construction.
	for _, c := range cands {
		if isEntryPoint(ix.Files[c.file].Path) {
			c.score *= 1.1
		}
	}

	ordered := make([]*candidate, 0, len(cands))
	for _, c := range cands {
		ordered = append(ordered, c)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].score == ordered[j].score {
			return ordered[i].file < ordered[j].file
		}
		return ordered[i].score > ordered[j].score
	})

	budget := opts.Budget(sectionDiff(section))
	if budget == 0 {
		return "", stats
	}

	var b strings.Builder
	if impact := ix.Impacts(section); impact != "" {
		b.WriteString(impact)
		b.WriteByte('\n')
	}

	maxScore := 0.0
	if len(ordered) > 0 {
		maxScore = ordered[0].score
	}
	emitted := 0
	seen := map[string]bool{}
	for _, c := range ordered {
		if emitted >= opts.MaxFiles {
			break
		}
		f := ix.Files[c.file]
		if f == nil {
			continue
		}
		full := c.score >= 0.66*maxScore
		refLine := 0
		if c.ref != nil {
			refLine = c.ref.Line
		}
		text, ok := renderSnippet(f, c.sym, refLine, full, 60)
		if !ok {
			stats.Unresolved++
			continue
		}
		key := fmt.Sprintf("%s:%d", f.Path, c.symLine())
		if seen[key] {
			continue
		}
		if b.Len()+len(text) > budget {
			// A lower tier is still useful when the full window does not fit.
			if short, ok := renderSnippet(f, c.sym, refLine, false, 12); ok && b.Len()+len(short) <= budget {
				text = short
			} else {
				continue
			}
		}
		seen[key] = true
		b.WriteString(text)
		emitted++
	}

	stats.Files = emitted
	stats.Snippets = emitted
	stats.Chars = b.Len()
	return b.String(), stats
}

func (ix *Index) bySymbolPath(p string) ([]*Symbol, bool) {
	f := ix.File(p)
	if f == nil {
		return nil, false
	}
	out := make([]*Symbol, 0, len(f.Symbols.Defs))
	for i := range f.Symbols.Defs {
		out = append(out, &f.Symbols.Defs[i])
	}
	return out, true
}

func (c *candidate) name() string {
	if c.sym != nil {
		return c.sym.Name
	}
	if c.ref != nil {
		return c.ref.Name
	}
	return ""
}

func (c *candidate) symLine() int {
	if c.sym != nil {
		return c.sym.Line
	}
	if c.ref != nil {
		return c.ref.Line
	}
	return 0
}

func topSignals(in map[string]bool, n int) map[string]bool {
	names := make([]string, 0, len(in))
	for k := range in {
		names = append(names, k)
	}
	sort.Strings(names)
	out := map[string]bool{}
	for _, k := range names {
		if len(out) >= n {
			break
		}
		if len(k) >= 4 {
			out[k] = true
		}
	}
	return out
}

func sectionDiff(section []*diff.File) int {
	n := 0
	for _, f := range section {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				n += len(l.Content) + 2
			}
		}
	}
	return n
}

func isEntryPoint(p string) bool {
	lower := strings.ToLower(p)
	return strings.HasPrefix(lower, "cmd/") || strings.Contains(lower, "/handlers/") ||
		strings.Contains(lower, "/api/") || strings.HasSuffix(lower, "/main.go") ||
		strings.HasSuffix(lower, "index.js") || strings.HasSuffix(lower, "app.ts")
}

// Impacts reports deterministic structural risks: a removed symbol that
// unchanged code still references, and a changed signature whose known call
// sites pass a different number of arguments.
func (ix *Index) Impacts(section []*diff.File) string {
	if ix == nil {
		return ""
	}
	removed := map[string]string{}
	addedDefs := map[string]bool{}
	oldParams := map[string]int{}
	newParams := map[string]int{}
	changed := map[string]bool{}
	for _, f := range section {
		changed[f.Path] = true
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if m := symbolFromLine.FindStringSubmatch(l.Content); m != nil {
					switch l.Kind {
					case diff.Added:
						addedDefs[m[1]] = true
					case diff.Removed:
						removed[m[1]] = f.Path
					}
				}
				if l.Kind == diff.Removed {
					if m := exportList.FindStringSubmatch(l.Content); m != nil {
						for _, name := range splitExportNames(m[1]) {
							removed[name] = f.Path
						}
					}
				}
				if m := funcSignature.FindStringSubmatch(l.Content); m != nil {
					switch l.Kind {
					case diff.Added:
						newParams[m[1]] = paramCount(m[2])
					case diff.Removed:
						oldParams[m[1]] = paramCount(m[2])
					}
				}
			}
		}
	}

	var lines []string

	// A removed export that another module imports is the strongest evidence
	// available without a type checker, so it is reported first and suppresses
	// the weaker "still referenced" note for the same symbol.
	importNotes, importedBreaks := ix.importBreakNotes(section, removed, changed)
	lines = append(lines, importNotes...)

	names := make([]string, 0, len(removed))
	for name := range removed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if addedDefs[name] || importedBreaks[name] {
			continue
		}
		from := removed[name]
		shown := 0
		for _, r := range ix.References(name) {
			f := ix.Files[r.File]
			if f == nil || f.Path == from {
				continue
			}
			lines = append(lines, fmt.Sprintf(
				"- %s was removed from %s but is still referenced at %s:%d",
				name, from, f.Path, r.Ref.Line))
			shown++
			if shown >= 3 {
				break
			}
		}
	}

	names = names[:0]
	for name := range oldParams {
		if _, ok := newParams[name]; ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if oldParams[name] == newParams[name] {
			continue
		}
		for _, r := range ix.References(name) {
			f := ix.Files[r.File]
			if f == nil || f.Path == removed[name] {
				continue
			}
			args, ok := callArgCount(f, r.Ref.Line, name)
			if !ok || args == newParams[name] {
				continue
			}
			lines = append(lines, fmt.Sprintf(
				"- %s signature changed from %d to %d parameters; %s:%d passes %d",
				name, oldParams[name], newParams[name], f.Path, r.Ref.Line, args))
			break
		}
	}

	lines = append(lines, ix.ifaceChangeNotes(section, changed)...)

	if len(lines) == 0 {
		return ""
	}
	if len(lines) > 8 {
		lines = lines[:8]
	}
	return "### PR impact (structural, not a finding)\n" + strings.Join(lines, "\n") + "\n"
}

func paramCount(params string) int {
	params = strings.TrimSpace(params)
	if params == "" {
		return 0
	}
	depth, count := 0, 1
	for _, r := range params {
		switch r {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				count++
			}
		}
	}
	return count
}

// callArgCount counts top-level arguments at a call site, tolerating multi-line
// calls, nested calls, and commas inside strings.
func callArgCount(f *IndexFile, line int, name string) (int, bool) {
	lines := strings.Split(string(f.Source), "\n")
	for i := line - 1; i < len(lines) && i < line+10; i++ {
		idx := strings.Index(lines[i], name+"(")
		if idx < 0 {
			continue
		}
		end := minInt(len(lines), i+12)
		text := strings.Join(lines[i:end], "\n")
		depth, count, nonEmpty := 0, 0, false
		inString, escaped := byte(0), false
		for j := idx + len(name); j < len(text); j++ {
			c := text[j]
			if inString != 0 {
				if escaped {
					escaped = false
					continue
				}
				if c == '\\' {
					escaped = true
				} else if c == inString {
					inString = 0
				}
				continue
			}
			switch c {
			case '"', '\'', '`':
				inString = c
				if depth == 1 {
					nonEmpty = true
				}
			case '(':
				depth++
				if depth == 1 {
					count = 1
				}
			case ')':
				depth--
				if depth == 0 {
					if !nonEmpty {
						return 0, true
					}
					return count, true
				}
			case ',':
				if depth == 1 {
					count++
				}
			default:
				if depth == 1 && c != ' ' && c != '\t' && c != '\n' && c != '\r' {
					nonEmpty = true
				}
			}
		}
	}
	return 0, false
}

func (ix *Index) addSimilar(cands map[string]*candidate, changed map[string]bool, section []*diff.File, add func(*candidate)) {
	for _, f := range section {
		fi := ix.File(f.Path)
		if fi == nil {
			continue
		}
		for i := range fi.Symbols.Defs {
			s := &fi.Symbols.Defs[i]
			if s.SimHash == 0 || len(s.Body) < 40 {
				continue
			}
			bestFile, bestSym, bestDist := -1, -1, 9
			for j, other := range ix.Files {
				if other == nil || changed[other.Path] || other.Test {
					continue
				}
				for k := range other.Symbols.Defs {
					o := &other.Symbols.Defs[k]
					if o.SimHash == 0 || o.Name == s.Name {
						continue
					}
					if d := Hamming(s.SimHash, o.SimHash); d < bestDist {
						bestDist = d
						bestFile, bestSym = j, k
					}
				}
			}
			if bestFile >= 0 && bestSym >= 0 && bestDist <= 6 {
				add(&candidate{file: bestFile, sym: &ix.Files[bestFile].Symbols.Defs[bestSym], kind: "similar", score: 15})
			}
		}
	}
}
