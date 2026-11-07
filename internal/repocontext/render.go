package repocontext

import (
	"fmt"
	"strings"
)

const maxSnippetLines = 80

// renderSnippet renders one validated reference. It returns false when the
// requested range does not resolve, which keeps stale or hallucinated paths out
// of the prompt.
func renderSnippet(f *IndexFile, sym *Symbol, refLine int, full bool, maxLines int) (string, bool) {
	if f == nil || len(f.Source) == 0 {
		return "", false
	}
	if maxLines <= 0 || maxLines > maxSnippetLines {
		maxLines = maxSnippetLines
	}
	lines := strings.Split(string(f.Source), "\n")

	start, end := 0, 0
	label := "reference"
	if sym != nil && sym.EndLine < sym.Line {
		sym.EndLine = sym.Line
	}
	switch {
	case sym != nil && refLine > 0:
		start, end = sym.Line, sym.EndLine
		label = "defines " + sym.Name
		if full {
			start = maxInt(1, minInt(refLine-10, end-1))
			end = minInt(len(lines), start+maxLines-1)
		}
	case sym != nil:
		start, end = sym.Line, sym.EndLine
		label = "defines " + sym.Name
	case refLine > 0:
		start = maxInt(1, refLine-8)
		end = minInt(len(lines), refLine+12)
	}
	if start <= 0 || start > len(lines) {
		return "", false
	}
	if end < start {
		end = start
	}
	if end-start+1 > maxLines {
		end = start + maxLines - 1
	}
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "### reference: %s:%d-%d · %s\n", f.Path, start, end, label)
	for i := start - 1; i < end; i++ {
		fmt.Fprintf(&b, "%5d | %s\n", i+1, lines[i])
	}
	return b.String(), true
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
