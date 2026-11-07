package app

import (
	"context"
	"strings"
	"time"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/llm"
	"github.com/EOSKILLZ/ekravo-review/v2/internal/research"
	"github.com/EOSKILLZ/ekravo-review/v2/internal/review"
)

// verifyFindings runs the tool-use step: the model asks for web lookups on
// externally checkable claims, the client performs them, and a second pass keeps
// or drops each claim based on the returned sources. Local logic findings are
// never sent.
func (a *App) verifyFindings(ctx context.Context, meta review.Meta, findings []review.Finding) ([]review.Finding, []review.Dropped) {
	if !a.cfg.Research.Enabled || a.research == nil || len(findings) == 0 {
		return findings, nil
	}

	candidates := make([]review.Finding, 0, len(findings))
	positions := make([]int, 0, len(findings))
	for i, f := range findings {
		if review.NeedsEvidence(f) {
			candidates = append(candidates, f)
			positions = append(positions, i)
		}
	}
	if len(candidates) == 0 {
		return findings, nil
	}

	planCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	system, user := review.ResearchPlanPrompt(meta, candidates)
	plan, err := a.lm.JSON(planCtx, system, user, llm.Options{MaxTokens: 800})
	if err != nil {
		a.log.Warn("evidence check skipped: could not plan lookups", "err", err)
		return findings, nil
	}
	queries, err := review.ParseResearchPlan(plan.Raw)
	if err != nil {
		a.log.Warn("evidence check skipped: unparsable plan", "err", err)
		return findings, nil
	}
	if len(queries) == 0 {
		return findings, nil
	}
	max := a.cfg.Research.MaxQueries
	if max <= 0 {
		max = 3
	}
	if len(queries) > max {
		queries = queries[:max]
	}

	refuted := map[int]string{}
	checked := 0
	for _, q := range queries {
		findingIdx, ok := lookupFinding(q, candidates, positions)
		if !ok {
			continue
		}

		searchCtx, cancel := context.WithTimeout(ctx, a.cfg.Research.Timeout)
		result, err := a.research.Search(searchCtx, q.Query)
		cancel()
		if err != nil {
			a.log.Warn("evidence lookup failed", "query", q.Query, "err", err)
			continue
		}

		ev := convertEvidence(result)
		if len(ev.Sources) == 0 {
			continue
		}

		vSystem, vUser := review.EvidencePrompt(findings[findingIdx], ev)
		verdictRes, err := a.lm.JSON(ctx, vSystem, vUser, llm.Options{MaxTokens: 300})
		if err != nil {
			a.log.Warn("evidence judgement failed", "query", q.Query, "err", err)
			continue
		}
		verdict, err := review.ParseEvidenceVerdict(verdictRes.Raw)
		if err != nil {
			a.log.Warn("evidence judgement unparsable", "query", q.Query, "err", err)
			continue
		}
		checked++

		switch verdict.Verdict {
		case "refute":
			refuted[findingIdx] = verdict.Note
			a.log.Info("finding refuted by web evidence",
				"repo", meta.Repo, "pr", meta.Number,
				"file", findings[findingIdx].File, "line", findings[findingIdx].Line,
				"note", verdict.Note)
		case "confirm":
			review.MarkVerified(&findings[findingIdx], ev, verdict.Note)
			a.log.Info("finding confirmed by web evidence",
				"repo", meta.Repo, "pr", meta.Number,
				"file", findings[findingIdx].File, "line", findings[findingIdx].Line,
				"sources", len(ev.Sources))
		}
	}

	if checked == 0 {
		return findings, nil
	}
	if len(refuted) == 0 {
		return findings, nil
	}

	kept := make([]review.Finding, 0, len(findings))
	var dropped []review.Dropped
	for i, f := range findings {
		if note, ok := refuted[i]; ok {
			dropped = append(dropped, review.Dropped{
				Finding: f,
				Reason:  "refuted by web evidence: " + note,
			})
			continue
		}
		kept = append(kept, f)
	}
	return kept, dropped
}

func lookupFinding(q review.ResearchQuery, candidates []review.Finding, positions []int) (int, bool) {
	if q.Index >= 1 && q.Index <= len(candidates) {
		return positions[q.Index-1], true
	}
	for i, f := range candidates {
		if q.File != "" && f.File == q.File && (q.Line == 0 || f.Line == q.Line) {
			return positions[i], true
		}
	}
	if len(candidates) == 1 && (q.File == "" || strings.EqualFold(q.File, candidates[0].File)) {
		return positions[0], true
	}
	return 0, false
}

func convertEvidence(r *research.Result) review.Evidence {
	ev := review.Evidence{Query: r.Query, Answer: truncate(r.Answer, 1200)}
	for _, s := range r.Sources {
		if s.URL == "" {
			continue
		}
		ev.Sources = append(ev.Sources, review.Source{
			Title: s.Title, URL: s.URL, Score: s.Score,
			Snippet: truncate(s.Content, 500),
		})
		if len(ev.Sources) == 3 {
			break
		}
	}
	return ev
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
