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
	"suggestions": "⚠️ Approved with suggestions",
	"changes":     "❌ Changes requested",
}

// InlineBody renders the comment attached to one line of the diff.
func InlineBody(f Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s\n\n", severityBadge[f.Severity], titleCase(f.Category))
	fmt.Fprintf(&b, "**%s**\n\n", f.Title)
	if f.Detail != "" {
		b.WriteString(f.Detail + "\n\n")
	}
	if f.Fix != "" {
		fmt.Fprintf(&b, "**Suggested fix:** %s\n", f.Fix)
	}
	if f.Evidence != nil && len(f.Evidence.Sources) > 0 {
		b.WriteString("\n")
		if f.EvidenceNote != "" {
			fmt.Fprintf(&b, "**Checked against sources:** %s\n\n", f.EvidenceNote)
		} else {
			b.WriteString("**Checked against sources:**\n\n")
		}
		for _, s := range f.Evidence.Sources {
			fmt.Fprintf(&b, "- [%s](%s)\n", s.Title, s.URL)
		}
	}
	b.WriteString("\n<sub>🔎 Ekravo Review</sub>")
	return b.String()
}

// Summary renders the review body shown alongside the inline comments.
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
}

func Summary(in SummaryInput) string {
	var b strings.Builder

	label := "Review"
	if in.Incremental {
		label = "Re-review"
	}
	fmt.Fprintf(&b, "## 🔎 Ekravo Review %s &nbsp; %s\n\n", label, verdictHeader[in.Verdict.Decision])

	repo := in.Meta.Repo
	if repo == "" {
		repo = in.Meta.Base
	}
	prURL := fmt.Sprintf("https://github.com/%s/pull/%d", repo, in.Meta.Number)
	parts := []string{fmt.Sprintf("[#%d · %s](%s)", in.Meta.Number, in.Meta.Title, prURL)}
	if in.Meta.ChangedFiles > 0 {
		parts = append(parts, fmt.Sprintf("%d file(s) · +%d / -%d",
			in.Meta.ChangedFiles, in.Meta.Additions, in.Meta.Deletions))
	}
	if in.Meta.Author != "" {
		parts = append(parts, "@"+in.Meta.Author)
	}
	fmt.Fprintf(&b, "> %s\n\n---\n\n", strings.Join(parts, " &nbsp;·&nbsp; "))

	if len(in.Findings) == 0 {
		b.WriteString("No issues found. The changes look safe to merge.\n")
	} else {
		fmt.Fprintf(&b, "**%d finding(s):** %d critical · %d high · %d medium · %d low\n\n",
			len(in.Findings),
			in.Verdict.Counts[Critical], in.Verdict.Counts[High],
			in.Verdict.Counts[Medium], in.Verdict.Counts[Low])

		grouped := map[string][]Finding{}
		var files []string
		for _, f := range in.Findings {
			if _, ok := grouped[f.File]; !ok {
				files = append(files, f.File)
			}
			grouped[f.File] = append(grouped[f.File], f)
		}
		sort.Strings(files)

		for _, file := range files {
			fmt.Fprintf(&b, "### `%s`\n\n", file)
			for _, f := range grouped[file] {
				fmt.Fprintf(&b, "- **`%s:%d`** %s — **%s**\n", f.File, f.Line, severityBadge[f.Severity], f.Title)
				if f.Detail != "" {
					fmt.Fprintf(&b, "  %s\n", f.Detail)
				}
				if f.Fix != "" {
					fmt.Fprintf(&b, "  _Fix:_ %s\n", f.Fix)
				}
			}
			b.WriteString("\n")
		}
	}

	if in.Dropped > 0 {
		fmt.Fprintf(&b, "\n<sub>%d model finding(s) were discarded because they did not point at a changed line in this PR.</sub>\n", in.Dropped)
	}
	if in.Truncated {
		b.WriteString("\n> ⚠️ The diff exceeded the review budget; only the shown portion was reviewed.\n")
	}
	if in.Failed > 0 && in.Sections > 0 {
		fmt.Fprintf(&b, "\n> ⚠️ %d of %d review sections failed and were skipped; the findings below are from the sections that succeeded.\n", in.Failed, in.Sections)
	}

	if len(in.Findings) > 0 {
		b.WriteString("\n<details>\n<summary>💡 Using an AI agent? Paste this to fix the findings:</summary>\n\n")
		b.WriteString("````text\n")
		fmt.Fprintf(&b, "Fix these code review findings in PR #%d (%s). For each one, quote the file and line, show the corrected code, and explain the fix in one sentence.\n\n",
			in.Meta.Number, in.Meta.Title)
		for _, f := range in.Findings {
			fmt.Fprintf(&b, "- %s:%d [%s/%s] %s — %s\n", f.File, f.Line, f.Severity, f.Category, f.Title, f.Fix)
		}
		b.WriteString("````\n\n</details>\n")
	}

	events := map[string]string{
		"approve":     "✅ **Approved — safe to merge.**",
		"suggestions": "⚠️ **Approved with suggestions — safe to merge, consider the notes above.**",
		"changes":     "❌ **Changes requested — resolve the blocking findings before merging.**",
	}
	fmt.Fprintf(&b, "\n---\n\n> %s\n\n", events[in.Verdict.Decision])

	model := in.Model
	if model == "" {
		model = "unknown"
	}
	handle := in.Handle
	if handle == "" {
		handle = "ekravo-review"
	}
	fmt.Fprintf(&b, "<sub>Reviewed by `%s` · %s</sub>\n", model, time.Now().UTC().Format("2006-01-02 15:04 UTC"))
	fmt.Fprintf(&b, "<sub>Commands: `@%s review` · `@%s explain` · `@%s summarize` · [Ekravo Review](https://github.com/EOSKILLZ/ekravo-review)</sub>\n", handle, handle, handle)
	return b.String()
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
