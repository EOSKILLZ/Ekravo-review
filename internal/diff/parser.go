// Package diff parses unified diffs into files, hunks, and line maps.
//
// GitHub returns one patch per changed file, starting at the first hunk
// ("@@ ... @@"). The parser also accepts full git-style diffs so the same code
// can be used from tests and the CLI.
package diff

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// LineKind is the first byte of a unified-diff line.
type LineKind byte

const (
	Context LineKind = ' '
	Added   LineKind = '+'
	Removed LineKind = '-'
)

// Line is a single diff line with its position in the old and new file.
// NewNumber is only meaningful for Added and Context lines; OldNumber only for
// Removed and Context lines.
type Line struct {
	Kind      LineKind
	Content   string
	OldNumber int
	NewNumber int
}

// Hunk is one "@@ -a,b +c,d @@" block.
type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Section  string
	Lines    []Line
}

// File is one changed file in a pull request.
type File struct {
	Path      string
	OldPath   string
	Status    string // added | modified | removed | renamed
	Hunks     []Hunk
	Additions int
	Deletions int
	Binary    bool
	Meta      Meta
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@ ?(.*)$`)

// ParseFilePatch parses a single file's patch. path is the new-file path and
// status the GitHub file status (added, modified, removed, renamed).
func ParseFilePatch(path, status, patch string) (*File, error) {
	f := &File{Path: path, Status: status}
	if f.Status == "" {
		f.Status = "modified"
	}

	lines := strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n")
	cur := -1
	oldNo, newNo := 0, 0

	for _, raw := range lines {
		if m := hunkHeader.FindStringSubmatch(raw); m != nil {
			oldStart, _ := strconv.Atoi(m[1])
			oldCount := 1
			if m[2] != "" {
				oldCount, _ = strconv.Atoi(m[2])
			}
			newStart, _ := strconv.Atoi(m[3])
			newCount := 1
			if m[4] != "" {
				newCount, _ = strconv.Atoi(m[4])
			}
			f.Hunks = append(f.Hunks, Hunk{
				OldStart: oldStart, OldCount: oldCount,
				NewStart: newStart, NewCount: newCount,
				Section: m[5],
			})
			cur = len(f.Hunks) - 1
			oldNo, newNo = oldStart, newStart
			continue
		}

		switch {
		case strings.HasPrefix(raw, "diff --git "):
			cur = -1
			if fields := strings.Fields(raw); len(fields) == 4 {
				if f.OldPath == "" {
					f.OldPath = cleanPath(fields[2])
				}
				if f.Path == "" {
					f.Path = cleanPath(fields[3])
				}
			}
			continue
		case strings.HasPrefix(raw, "--- "):
			cur = -1
			if p := cleanPath(strings.TrimPrefix(raw, "--- ")); p != "" && f.OldPath == "" {
				f.OldPath = p
			}
			continue
		case strings.HasPrefix(raw, "+++ "):
			cur = -1
			if p := cleanPath(strings.TrimPrefix(raw, "+++ ")); p != "" && f.Path == "" {
				f.Path = p
			}
			continue
		case strings.HasPrefix(raw, "new file mode"):
			f.Status = "added"
			continue
		case strings.HasPrefix(raw, "deleted file mode"):
			f.Status = "removed"
			continue
		case strings.HasPrefix(raw, "rename from "):
			f.OldPath = strings.TrimSpace(strings.TrimPrefix(raw, "rename from "))
			f.Status = "renamed"
			continue
		case strings.HasPrefix(raw, "rename to "):
			f.Path = strings.TrimSpace(strings.TrimPrefix(raw, "rename to "))
			f.Status = "renamed"
			continue
		case strings.HasPrefix(raw, "Binary files"), strings.HasPrefix(raw, "GIT binary patch"):
			f.Binary = true
			continue
		}

		if cur < 0 || raw == "" {
			continue
		}

		kind := LineKind(raw[0])
		if kind != Context && kind != Added && kind != Removed {
			if raw[0] == '\\' { // "\ No newline at end of file"
				continue
			}
			cur = -1
			continue
		}

		ln := Line{Kind: kind, Content: raw[1:]}
		switch kind {
		case Added:
			ln.NewNumber = newNo
			newNo++
			f.Additions++
		case Removed:
			ln.OldNumber = oldNo
			oldNo++
			f.Deletions++
		default:
			ln.OldNumber = oldNo
			ln.NewNumber = newNo
			oldNo++
			newNo++
		}
		f.Hunks[cur].Lines = append(f.Hunks[cur].Lines, ln)
	}

	if f.Path == "" {
		return nil, fmt.Errorf("diff: %q has no file path", path)
	}
	f.Meta = Classify(f.Path)
	return f, nil
}

func cleanPath(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	if s == "/dev/null" {
		return ""
	}
	s = strings.Trim(s, `"`)
	s = strings.TrimPrefix(s, "a/")
	s = strings.TrimPrefix(s, "b/")
	return s
}

// AddedLineNumbers returns the new-file line numbers introduced by this file.
func (f *File) AddedLineNumbers() map[int]struct{} {
	out := make(map[int]struct{}, f.Additions)
	if f == nil {
		return out
	}
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == Added {
				out[l.NewNumber] = struct{}{}
			}
		}
	}
	return out
}

// ChangedLine returns the diff line at new-file line n.
func (f *File) ChangedLine(n int) (Line, bool) {
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind != Removed && l.NewNumber == n {
				return l, true
			}
		}
	}
	return Line{}, false
}

// AnchorLine maps an arbitrary new-file line to the nearest added line inside
// the same hunk. It reports false when the line is not part of any hunk.
func (f *File) AnchorLine(n int) (int, bool) {
	for _, h := range f.Hunks {
		var candidates []int
		for _, l := range h.Lines {
			if l.Kind == Added {
				candidates = append(candidates, l.NewNumber)
			}
		}
		if len(candidates) == 0 {
			continue
		}
		lo, hi := h.NewStart, h.NewStart+h.NewCount
		for _, l := range h.Lines {
			if l.Kind != Removed {
				if l.NewNumber < lo {
					lo = l.NewNumber
				}
				if l.NewNumber > hi {
					hi = l.NewNumber
				}
			}
		}
		if n < lo || n > hi {
			continue
		}
		sort.Ints(candidates)
		best := candidates[0]
		for _, c := range candidates {
			if abs(c-n) < abs(best-n) {
				best = c
			}
		}
		return best, true
	}
	return 0, false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Chunk is a reviewable slice of one file that fits a character budget.
type Chunk struct {
	File      *File
	Hunks     []Hunk
	Chars     int
	Truncated bool
}

// ChunkFiles splits files into chunks of at most maxChars characters, never
// mixing files (so findings always map back to one path). A single line larger
// than the budget still gets its own chunk rather than being dropped.
func ChunkFiles(files []*File, maxChars int) []Chunk {
	if maxChars <= 0 {
		maxChars = 120_000
	}
	var chunks []Chunk
	for _, f := range files {
		chunks = append(chunks, ChunkFile(f, maxChars)...)
	}
	return chunks
}

// ChunkFile splits one file's hunks into budget-sized chunks.
func ChunkFile(f *File, maxChars int) []Chunk {
	if f == nil || len(f.Hunks) == 0 {
		return nil
	}
	if maxChars <= 0 {
		maxChars = 120_000
	}
	var chunks []Chunk
	cur := Chunk{File: f}
	for _, h := range f.Hunks {
		for _, part := range splitHunk(h, maxChars) {
			size := hunkSize(part)
			if cur.Chars > 0 && cur.Chars+size > maxChars {
				chunks = append(chunks, cur)
				cur = Chunk{File: f}
			}
			cur.Hunks = append(cur.Hunks, part)
			cur.Chars += size
		}
	}
	if len(cur.Hunks) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

func splitHunk(h Hunk, maxChars int) []Hunk {
	if hunkSize(h) <= maxChars {
		return []Hunk{h}
	}
	var out []Hunk
	oldNo, newNo := h.OldStart, h.NewStart
	cur := Hunk{Section: h.Section}
	size := 0

	flush := func() {
		if len(cur.Lines) == 0 {
			return
		}
		cur.OldCount, cur.NewCount = 0, 0
		for _, l := range cur.Lines {
			if l.Kind != Added {
				cur.OldCount++
			}
			if l.Kind != Removed {
				cur.NewCount++
			}
		}
		out = append(out, cur)
	}

	for _, l := range h.Lines {
		lineSize := len(l.Content) + 2
		if size+lineSize > maxChars && len(cur.Lines) > 0 {
			flush()
			cur = Hunk{Section: h.Section}
			size = 0
		}
		if len(cur.Lines) == 0 {
			cur.OldStart, cur.NewStart = oldNo, newNo
		}
		cur.Lines = append(cur.Lines, l)
		size += lineSize
		if l.Kind != Added {
			oldNo++
		}
		if l.Kind != Removed {
			newNo++
		}
	}
	flush()
	return out
}

func hunkSize(h Hunk) int {
	n := len(h.Section) + 32
	for _, l := range h.Lines {
		n += len(l.Content) + 2
	}
	return n
}

// RenderNumberedHunks renders hunks with the new-file line number in front of
// every context and added line. Removed lines have no new-file number. The
// model is instructed to anchor findings to the printed "+" numbers, which
// removes the need for it to count lines from hunk headers.
func RenderNumberedHunks(f *File, hunks []Hunk) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s (+%d/-%d) · %s\n", f.Path, f.Additions, f.Deletions, f.Meta.Label())
	for _, h := range hunks {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@ %s\n",
			h.OldStart, h.OldCount, h.NewStart, h.NewCount, h.Section)
		for _, l := range h.Lines {
			if l.Kind == Removed {
				fmt.Fprintf(&b, "-     | %s\n", l.Content)
				continue
			}
			fmt.Fprintf(&b, "%c%4d | %s\n", byte(l.Kind), l.NewNumber, l.Content)
		}
	}
	return b.String()
}

// RenderHunks renders hunks back to unified-diff text for a prompt.
func RenderHunks(f *File, hunks []Hunk) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s (+%d/-%d) · %s\n", f.Path, f.Additions, f.Deletions, f.Meta.Label())
	for _, h := range hunks {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@ %s\n",
			h.OldStart, h.OldCount, h.NewStart, h.NewCount, h.Section)
		for _, l := range h.Lines {
			b.WriteByte(byte(l.Kind))
			b.WriteString(l.Content)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
