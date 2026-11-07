package review

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Source is one web page returned by the research backend.
type Source struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Snippet string  `json:"snippet,omitempty"`
	Score   float64 `json:"score,omitempty"`
}

// Evidence is the result of checking an externally verifiable claim.
type Evidence struct {
	Query   string   `json:"query"`
	Answer  string   `json:"answer,omitempty"`
	Sources []Source `json:"sources,omitempty"`
}

// ResearchQuery is one lookup the model asked for. Index is the 1-based
// position of the finding in the plan request.
type ResearchQuery struct {
	Index int    `json:"index"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Query string `json:"query"`
}

// EvidenceVerdict is the outcome of checking one finding against sources.
type EvidenceVerdict struct {
	Verdict string `json:"verdict"`
	Note    string `json:"note"`
}

var evidenceTriggers = []string{
	"cve-", "security advisory", "vulnerab", "exploit",
	"does not exist", "doesn't exist", "not a function", "not a method",
	"not supported", "unsupported", "removed in", "deprecated", "no longer",
	"since v", "introduced in", "breaking change", "renamed in",
	"typo", "misspell", "wrong name", "should be called",
}

// NeedsEvidence reports whether a finding makes a claim about the outside world
// that a web lookup can actually settle. Local logic bugs are never sent.
func NeedsEvidence(f Finding) bool {
	if f.Severity != Critical && f.Severity != High {
		return false
	}
	text := strings.ToLower(f.Title + " " + f.Detail + " " + f.Fix)
	for _, trigger := range evidenceTriggers {
		if strings.Contains(text, trigger) {
			return true
		}
	}
	return false
}

// ResearchPlanPrompt asks the model which findings deserve a lookup. It is a
// tool-use step written as JSON because the gateway's tool calls are
// client-executed and JSON keeps the review pipeline deterministic.
func ResearchPlanPrompt(meta Meta, findings []Finding) (string, string) {
	system := `You decide which code-review findings need a web lookup before they are posted.
You cannot browse. Output ONLY {"queries":[{"index":1,"file":"path","line":12,"query":"search words"}]}.
index is the 1-based number of the finding in the list you are given.

Ask for a lookup only when the claim depends on an external fact that sources can settle:
- whether an API, method, class, option, or CLI flag exists in a named library or version
- whether a version introduced, removed, or deprecated behaviour
- whether a dependency or pattern has a known CVE or security advisory
Do NOT ask for:
- local logic, null handling, naming, style, or anything proven by the diff
- a broad "best practices" question
- anything you are not certain is externally checkable
Return {"queries":[]} if none qualify. At most 3 queries. Make each query specific:
library or product, version if known, and the exact symbol or behaviour.`
	user := fmt.Sprintf("Repository: %s\nPR #%d: %s\n\nFindings:\n%s",
		meta.Repo, meta.Number, meta.Title, findingsForResearch(findings))
	return system, user
}

func findingsForResearch(findings []Finding) string {
	var b strings.Builder
	for i, f := range findings {
		fmt.Fprintf(&b, "%d. %s:%d [%s/%s] %s — %s\n",
			i+1, f.File, f.Line, f.Severity, f.Category, f.Title, f.Detail)
	}
	return b.String()
}

// EvidencePrompt asks for a verdict that must be grounded in the returned
// sources. The model is explicitly forbidden from using its own memory.
func EvidencePrompt(f Finding, ev Evidence) (string, string) {
	system := `You are an adversarial evidence verifier. The review claim is already written; decide whether the
supplied web sources support it. Output ONLY {"verdict":"confirm|refute|uncertain","note":"one sentence"}.

Rules:
- confirm only when a source explicitly supports the claim, including the relevant version or symbol
- refute when a source explicitly contradicts the claim
- uncertain when the sources are about something else, are undated blog opinion, or do not address the exact claim
- never confirm from your own memory; the sources are the only evidence
- a source that merely mentions the library is not support
- if the claim is about a specific version and the sources do not cover that version, choose uncertain
- keep note under 200 characters and name the source title when it decides the verdict`
	user := fmt.Sprintf("Claim: %s\nFile: %s:%d\nSeverity: %s\nCategory: %s\nDetail: %s\n\nQuery: %s\n",
		f.Title, f.File, f.Line, f.Severity, f.Category, f.Detail, ev.Query)
	if ev.Answer != "" {
		user += "\nSynthesised answer (may be wrong, verify against sources):\n" + truncateText(ev.Answer, 1200) + "\n"
	}
	user += "\nSources:\n"
	for i, s := range ev.Sources {
		user += fmt.Sprintf("[%d] %s — %s\n", i+1, s.Title, s.URL)
		if s.Snippet != "" {
			user += "    " + s.Snippet + "\n"
		}
	}
	return system, user
}

// ParseResearchPlan accepts {"queries":[...]} or a bare array and drops empty
// or duplicate queries.
func ParseResearchPlan(raw []byte) ([]ResearchQuery, error) {
	var queries []ResearchQuery
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("empty research plan")
	}
	if trimmed[0] == '[' {
		if err := json.Unmarshal([]byte(trimmed), &queries); err != nil {
			return nil, fmt.Errorf("decode research queries: %w", err)
		}
	} else {
		var wrapper struct {
			Queries []ResearchQuery `json:"queries"`
		}
		if err := json.Unmarshal([]byte(trimmed), &wrapper); err != nil {
			return nil, fmt.Errorf("decode research plan: %w", err)
		}
		queries = wrapper.Queries
	}

	seen := map[string]bool{}
	out := make([]ResearchQuery, 0, len(queries))
	for _, q := range queries {
		q.Query = strings.TrimSpace(q.Query)
		q.File = strings.TrimSpace(q.File)
		if q.Index < 0 {
			q.Index = 0
		}
		if len(q.Query) < 8 || len(q.Query) > 400 {
			continue
		}
		key := strings.ToLower(q.Query)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, q)
	}
	return out, nil
}

// ParseEvidenceVerdict parses the single verdict object for one finding.
func ParseEvidenceVerdict(raw []byte) (EvidenceVerdict, error) {
	var v EvidenceVerdict
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("decode evidence verdict: %w", err)
	}
	v.Verdict = strings.ToLower(strings.TrimSpace(v.Verdict))
	switch v.Verdict {
	case "confirm", "refute", "uncertain":
	default:
		return v, fmt.Errorf("unknown evidence verdict %q", v.Verdict)
	}
	v.Note = strings.TrimSpace(v.Note)
	if len(v.Note) > 300 {
		v.Note = v.Note[:300] + "…"
	}
	return v, nil
}

// MarkVerified attaches sources to a confirmed finding.
func MarkVerified(f *Finding, ev Evidence, note string) {
	if f == nil || len(ev.Sources) == 0 {
		return
	}
	if len(ev.Sources) > 3 {
		ev.Sources = ev.Sources[:3]
	}
	f.Evidence = &ev
	f.EvidenceNote = note
}

func truncateText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
