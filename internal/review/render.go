package review

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

var severityBadge = map[Severity]string{
	Critical: "🔴 Critical",
	High:     "🟠 High",
	Medium:   "🟡 Medium",
	Low:      "🟢 Low",
}

var verdictHeader = map[string]string{
	"approve":     "✅ Approved",
	"suggestions": "🟡 Approved with suggestions",
	"changes":     "🔴 Changes requested",
}

// InlineBody renders the comment attached to one line of the diff. GitHub nests
// these under the changed line, so the severity leads, the title is the largest
// element, and the fix stays above the fold.
func InlineBody(f Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "> %s · %s\n\n", severityBadge[f.Severity], titleCase(f.Category))
	fmt.Fprintf(&b, "**%s**\n\n", f.Title)
	if f.Detail != "" {
		b.WriteString(f.Detail + "\n\n")
	}
	if f.Fix != "" {
		b.WriteString("**Suggested fix**\n\n")
		b.WriteString(f.Fix + "\n\n")
	}
	if f.Evidence != nil && len(f.Evidence.Sources) > 0 {
		b.WriteString("<details>\n<summary>🔗 Sources</summary>\n\n")
		if f.EvidenceNote != "" {
			b.WriteString(f.EvidenceNote + "\n\n")
		}
		for _, s := range f.Evidence.Sources {
			fmt.Fprintf(&b, "- [%s](%s)\n", s.Title, s.URL)
		}
		b.WriteString("\n</details>\n\n")
	}
	b.WriteString("<sub>🔎 Ekravo Review</sub>")
	return b.String()
}

// SummaryInput is everything the review body needs to describe one run.
type SummaryInput struct {
	Meta        Meta
	Findings    []Finding
	Verdict     Verdict
	Dropped     int
	Model       string
	Handle      string
	Truncated   bool
	Failed      int
	Sections    int
	Incremental bool
	Commit      string
	Duration    time.Duration
	// RepoURL is the project link shown in the footer. Empty falls back to the
	// canonical repository, so self-hosted forks can point at their own copy.
	RepoURL string
}

// Summary renders the review body shown alongside the inline comments. It is
// plain GitHub-flavored markdown: an alert callout for the verdict, a stat
// strip, then a table of findings.
func Summary(in SummaryInput) string {
	var b strings.Builder

	headline := verdictHeader[in.Verdict.Decision]
	if headline == "" {
		headline = "Review posted"
	}
	if in.Incremental {
		headline += " · re-review"
	}
	fmt.Fprintf(&b, "## 🔎 Ekravo Review — %s\n\n", headline)

	repo := in.Meta.Repo
	if repo == "" {
		repo = in.Meta.Base
	}
	prURL := in.Meta.URL
	if prURL == "" {
		prURL = fmt.Sprintf("https://github.com/%s/pull/%d", repo, in.Meta.Number)
	}
	fmt.Fprintf(&b, "**[#%d · %s](%s)**", in.Meta.Number, in.Meta.Title, prURL)
	if in.Meta.Author != "" {
		fmt.Fprintf(&b, " · @%s", in.Meta.Author)
	}
	b.WriteString("\n\n")

	b.WriteString(verdictCallout(in.Verdict))
	b.WriteString(statsTable(in))
	b.WriteString("\n")

	if len(in.Findings) > 0 {
		b.WriteString("### Findings\n\n")
		b.WriteString(findingsTable(in.Findings))
		b.WriteString("\n")

		b.WriteString("<details>\n<summary>🤖 Fix these with an AI agent</summary>\n\n")
		b.WriteString("````text\n")
		fmt.Fprintf(&b, "Fix these code review findings in PR #%d (%s). For each one, quote the file and line, show the corrected code, and explain the fix in one sentence.\n\n",
			in.Meta.Number, in.Meta.Title)
		for _, f := range in.Findings {
			fmt.Fprintf(&b, "- %s:%d [%s/%s] %s — %s\n", f.File, f.Line, f.Severity, f.Category, f.Title, f.Fix)
		}
		b.WriteString("````\n\n</details>\n\n")
	}

	if in.Truncated {
		b.WriteString("> [!IMPORTANT]\n> The diff exceeded the review budget, so only part of this PR was reviewed.\n\n")
	}
	if in.Failed > 0 && in.Sections > 0 {
		fmt.Fprintf(&b, "> [!WARNING]\n> %d of %d review sections failed and were skipped; these findings come from the sections that succeeded.\n\n",
			in.Failed, in.Sections)
	}
	if in.Dropped > 0 {
		fmt.Fprintf(&b, "<sub>%d model finding(s) were discarded because they did not point at a changed line in this PR.</sub>\n\n", in.Dropped)
	}

	model := in.Model
	if model == "" {
		model = "unknown"
	}
	handle := in.Handle
	if handle == "" {
		handle = "ekravo-review"
	}
	meta := []string{fmt.Sprintf("Reviewed by `%s`", model)}
	if in.Commit != "" {
		sha := in.Commit
		if len(sha) > 7 {
			sha = sha[:7]
		}
		meta = append(meta, fmt.Sprintf("commit `%s`", sha))
	}
	meta = append(meta, time.Now().UTC().Format("2006-01-02 15:04 UTC"))
	fmt.Fprintf(&b, "<sub>🔎 %s</sub>\n", strings.Join(meta, " · "))
	repoURL := strings.TrimSpace(in.RepoURL)
	if repoURL == "" {
		repoURL = "https://github.com/ekravo/Ekravo-review"
	}
	fmt.Fprintf(&b, "<sub>`@%s review` · `@%s explain` · `@%s summarize` · [Ekravo Review](%s)</sub>\n", handle, handle, handle, repoURL)
	return b.String()
}

func verdictCallout(v Verdict) string {
	switch v.Decision {
	case "changes":
		reason := strings.TrimSuffix(strings.TrimSpace(v.Reason), ".")
		if reason == "" {
			reason = "blocking findings must be resolved before merging"
		}
		return fmt.Sprintf("> [!WARNING]\n> **Changes requested** — %s.\n\n", reason)
	case "suggestions":
		return "> [!NOTE]\n> **Approved with suggestions** — no blocking issues, so this is safe to merge; the inline notes are optional improvements.\n\n"
	default:
		return "> [!TIP]\n> **Approved** — no issues found. Safe to merge.\n\n"
	}
}

func statsTable(in SummaryInput) string {
	files, lines := "—", "—"
	if in.Meta.ChangedFiles > 0 {
		files = fmt.Sprintf("%d", in.Meta.ChangedFiles)
		lines = fmt.Sprintf("+%d −%d", in.Meta.Additions, in.Meta.Deletions)
	}
	took := "—"
	if in.Duration > 0 {
		took = in.Duration.Round(100 * time.Millisecond).String()
	}
	var b strings.Builder
	b.WriteString("| Files | Lines | Findings | Reviewed in |\n")
	b.WriteString("|:--:|:--:|:--:|:--:|\n")
	fmt.Fprintf(&b, "| %s | %s | %d | %s |\n", files, lines, len(in.Findings), took)
	return b.String()
}

func findingsTable(findings []Finding) string {
	sorted := append([]Finding(nil), findings...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if a, b := severityRank(sorted[i].Severity), severityRank(sorted[j].Severity); a != b {
			return a > b
		}
		if sorted[i].File != sorted[j].File {
			return sorted[i].File < sorted[j].File
		}
		return sorted[i].Line < sorted[j].Line
	})

	var b strings.Builder
	b.WriteString("| Severity | Location | Finding |\n")
	b.WriteString("|:--|:--|:--|\n")
	for _, f := range sorted {
		finding := "**" + mdCell(f.Title) + "**"
		if category := titleCase(f.Category); category != "" {
			finding += " · " + mdCell(category)
		}
		fmt.Fprintf(&b, "| %s | `%s:%d` | %s |\n", severityBadge[f.Severity], f.File, f.Line, finding)
	}
	return b.String()
}

// mdCell keeps model-written text from breaking out of a table cell.
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// CommandsFooter is appended to command responses.
func CommandsFooter(handle string) string {
	return fmt.Sprintf("<sub>Commands: `@%s review` · `@%s explain` · `@%s summarize`</sub>", handle, handle, handle)
}

func titleCase(s string) string {
	switch s {
	case "bug":
		return "Bug"
	case "security":
		return "Security"
	case "performance":
		return "Performance"
	case "quality":
		return "Code Quality"
	case "test":
		return "Tests"
	case "docs":
		return "Docs"
	default:
		return s
	}
}
