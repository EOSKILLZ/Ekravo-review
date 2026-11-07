package review

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ekravo/Ekravo-review/v2/internal/config"
	"github.com/ekravo/Ekravo-review/v2/internal/diff"
)

// Severity is the impact of a finding.
type Severity string

const (
	Critical Severity = "critical"
	High     Severity = "high"
	Medium   Severity = "medium"
	Low      Severity = "low"
)

func severityRank(s Severity) int {
	switch s {
	case Critical:
		return 4
	case High:
		return 3
	case Medium:
		return 2
	case Low:
		return 1
	default:
		return 0
	}
}

// ValidCategory reports whether c is one of the supported categories.
func ValidCategory(c string) bool {
	switch c {
	case "bug", "security", "performance", "quality", "test", "docs":
		return true
	}
	return false
}

// Finding is one validated, line-anchored issue.
type Finding struct {
	File       string   `json:"file"`
	Line       int      `json:"line"`
	Severity   Severity `json:"severity"`
	Category   string   `json:"category"`
	Confidence float64  `json:"confidence"`
	Title      string   `json:"title"`
	Detail     string   `json:"detail"`
	Fix        string   `json:"fix"`

	// Evidence is set only when a finding was checked against web sources.
	Evidence     *Evidence `json:"evidence,omitempty"`
	EvidenceNote string    `json:"evidence_note,omitempty"`
}

// Dropped records a finding removed by validation, for logs and metrics.
type Dropped struct {
	Finding Finding
	Reason  string
}

// ParseFindings accepts the shapes small models actually return:
// {"findings":[...]}, {"issues":[...]}, a bare array, or a nested object.
// Numeric fields are coerced from strings and common aliases are mapped.
func ParseFindings(raw []byte) ([]Finding, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("empty model output")
	}

	var items []json.RawMessage
	switch trimmed[0] {
	case '[':
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("decode findings array: %w", err)
		}
	default:
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, fmt.Errorf("decode findings object: %w", err)
		}
		for _, key := range []string{"findings", "issues", "results", "comments"} {
			if v, ok := obj[key]; ok {
				if err := json.Unmarshal(v, &items); err == nil {
					break
				}
			}
		}
		if items == nil {
			// one level of nesting: {"review":{"findings":[...]}}
			for _, v := range obj {
				var nested map[string]json.RawMessage
				if json.Unmarshal(v, &nested) != nil {
					continue
				}
				if f, ok := nested["findings"]; ok && json.Unmarshal(f, &items) == nil {
					break
				}
			}
		}
		if items == nil {
			return nil, fmt.Errorf("no findings array in model output")
		}
	}

	findings := make([]Finding, 0, len(items))
	for _, item := range items {
		var m map[string]any
		if err := json.Unmarshal(item, &m); err != nil {
			continue
		}
		f := Finding{
			File:       firstString(m, "file", "path", "filename"),
			Line:       firstInt(m, "line", "line_number", "start_line", "new_line"),
			Severity:   Severity(strings.ToLower(firstString(m, "severity", "priority", "level"))),
			Category:   strings.ToLower(firstString(m, "category", "type", "kind")),
			Confidence: firstFloat(m, "confidence", "score"),
			Title:      firstString(m, "title", "summary", "message", "issue"),
			Detail:     firstString(m, "detail", "details", "description", "body", "explanation"),
			Fix:        firstString(m, "fix", "suggestion", "recommendation", "remediation"),
		}
		findings = append(findings, f)
	}
	return findings, nil
}

// Validate enforces the invariant that matters most: every finding points at a
// real added line in this PR. Unanchored findings are dropped, not guessed.
func Validate(findings []Finding, files []*diff.File, max int) ([]Finding, []Dropped) {
	byPath := make(map[string]*diff.File, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}

	seen := map[string]bool{}
	perFile := map[string]int{}
	kept := make([]Finding, 0, len(findings))
	var dropped []Dropped

	for _, f := range findings {
		f.File = normalizePath(f.File, byPath)
		target := byPath[f.File]
		if target == nil {
			dropped = append(dropped, Dropped{f, "file not in diff"})
			continue
		}

		if f.Severity != Critical && f.Severity != High && f.Severity != Medium && f.Severity != Low {
			f.Severity = Medium
		}
		if !ValidCategory(f.Category) {
			f.Category = "quality"
		}
		f.Title = strings.TrimSpace(f.Title)
		f.Detail = strings.TrimSpace(f.Detail)
		f.Fix = strings.TrimSpace(f.Fix)
		if f.Title == "" {
			f.Title = firstSentence(f.Detail)
		}
		if f.Title == "" {
			dropped = append(dropped, Dropped{f, "empty title"})
			continue
		}
		if f.Confidence <= 0 {
			f.Confidence = 0.6
		}
		if f.Confidence > 1 {
			f.Confidence = 1
		}

		line, ok := anchor(target, f.Line)
		if !ok {
			dropped = append(dropped, Dropped{f, fmt.Sprintf("line %d has no added line nearby", f.Line)})
			continue
		}
		f.Line = line

		if len(f.Detail) > 1500 {
			f.Detail = f.Detail[:1500] + "…"
		}
		if len(f.Fix) > 1000 {
			f.Fix = f.Fix[:1000] + "…"
		}

		if selfContradictoryClaim(f) {
			dropped = append(dropped, Dropped{f, "claim compares identical names"})
			continue
		}

		key := fmt.Sprintf("%s|%d|%s|%s", f.File, f.Line, f.Category, strings.ToLower(f.Title))
		if seen[key] {
			continue
		}
		if perFile[f.File] >= 5 {
			dropped = append(dropped, Dropped{f, "per-file finding cap"})
			continue
		}
		if max > 0 && len(kept) >= max {
			dropped = append(dropped, Dropped{f, "global finding cap"})
			continue
		}

		seen[key] = true
		perFile[f.File]++
		kept = append(kept, f)
	}

	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].File != kept[j].File {
			return kept[i].File < kept[j].File
		}
		if kept[i].Line != kept[j].Line {
			return kept[i].Line < kept[j].Line
		}
		return severityRank(kept[i].Severity) > severityRank(kept[j].Severity)
	})
	return kept, dropped
}

func anchor(f *diff.File, line int) (int, bool) {
	if line <= 0 {
		return 0, false
	}
	if _, ok := f.AddedLineNumbers()[line]; ok {
		return line, true
	}
	if got, ok := f.AnchorLine(line); ok {
		return got, true
	}
	return 0, false
}

var quotedToken = regexp.MustCompile(`['"` + "`" + `]([^'"` + "`" + `]{1,80})['"` + "`" + `]`)

// selfContradictoryClaim catches the common weak-model failure of proposing a
// correction whose "correct" name is identical to the "wrong" one, e.g.
// "fs.readFileSync does not exist — should be fs.readFileSync".
func selfContradictoryClaim(f Finding) bool {
	text := f.Title + " " + f.Detail
	lower := strings.ToLower(text)
	triggers := []string{"instead of", "should be", "typo", "misspell", "does not exist", "not a method", "not a function", "not standard"}
	triggered := false
	for _, t := range triggers {
		if strings.Contains(lower, t) {
			triggered = true
			break
		}
	}
	if !triggered {
		return false
	}
	m := quotedToken.FindAllStringSubmatch(text, -1)
	seen := map[string]bool{}
	for _, match := range m {
		name := strings.ToLower(strings.TrimSpace(match[1]))
		if name == "" {
			continue
		}
		if seen[name] {
			return true
		}
		seen[name] = true
	}
	return false
}

func normalizePath(p string, known map[string]*diff.File) string {
	p = strings.TrimSpace(strings.Trim(p, "`'\""))
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "a/")
	p = strings.TrimPrefix(p, "b/")
	p = strings.ReplaceAll(p, `\`, "/")
	if _, ok := known[p]; ok {
		return p
	}
	for path := range known {
		if strings.HasSuffix(path, "/"+p) || strings.HasSuffix(p, "/"+path) {
			return path
		}
	}
	return p
}

// Verdict is the review outcome, computed from validated findings only.
type Verdict struct {
	Decision string
	Counts   map[Severity]int
	Blocking int
	Reason   string
}

// Decide applies the repo's block_on policy. A model cannot approve code on
// vibes: the decision is a pure function of validated findings.
func Decide(findings []Finding, blockOn string) Verdict {
	rules := map[string]int{"none": 99, "critical": 4, "high": 3, "medium": 2, "low": 1}
	threshold, ok := rules[blockOn]
	if !ok {
		threshold = 3
	}

	v := Verdict{Counts: map[Severity]int{}}
	for _, f := range findings {
		v.Counts[f.Severity]++
		if severityRank(f.Severity) >= threshold {
			v.Blocking++
		}
	}

	switch {
	case v.Blocking > 0:
		v.Decision = "changes"
		v.Reason = fmt.Sprintf("%d blocking issue(s) at or above %s severity", v.Blocking, blockOn)
	case len(findings) > 0:
		v.Decision = "suggestions"
		v.Reason = "no blocking issues; minor findings only"
	default:
		v.Decision = "approve"
		v.Reason = "no issues found"
	}
	return v
}

// ReviewEvent maps a verdict to the GitHub review event.
func ReviewEvent(decision string, autoApprove bool) string {
	if !autoApprove {
		return "COMMENT"
	}
	switch decision {
	case "approve":
		return "APPROVE"
	case "changes":
		return "REQUEST_CHANGES"
	default:
		return "COMMENT"
	}
}

// Meta is the pull-request context sent to the model and rendered in comments.
type Meta struct {
	Number       int
	Repo         string
	URL          string
	Title        string
	Body         string
	Author       string
	Base         string
	Head         string
	Commits      int
	ChangedFiles int
	Additions    int
	Deletions    int
	Labels       []string
}

func normalizeRepoConfig(rc *config.RepoConfig) *config.RepoConfig {
	if rc == nil {
		return config.DefaultRepoConfig()
	}
	return rc
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				return strings.TrimSpace(t)
			case json.Number:
				return t.String()
			}
		}
	}
	return ""
}

func firstInt(m map[string]any, keys ...string) int {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		case string:
			var n int
			if _, err := fmt.Sscanf(strings.TrimSpace(t), "%d", &n); err == nil {
				return n
			}
		}
	}
	return 0
}

func firstFloat(m map[string]any, keys ...string) float64 {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case float64:
			return t
		case string:
			var f float64
			if _, err := fmt.Sscanf(strings.TrimSpace(t), "%g", &f); err == nil {
				return f
			}
		}
	}
	return 0
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexAny(s, ".!?\n"); i > 0 {
		return s[:i]
	}
	if len(s) > 120 {
		return s[:120]
	}
	return s
}
