// Package app wires the review pipeline together.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	pathpkg "path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ekravo/Ekravo-review/v2/internal/config"
	"github.com/ekravo/Ekravo-review/v2/internal/diff"
	"github.com/ekravo/Ekravo-review/v2/internal/githubx"
	"github.com/ekravo/Ekravo-review/v2/internal/llm"
	"github.com/ekravo/Ekravo-review/v2/internal/poller"
	"github.com/ekravo/Ekravo-review/v2/internal/queue"
	"github.com/ekravo/Ekravo-review/v2/internal/repocontext"
	"github.com/ekravo/Ekravo-review/v2/internal/research"
	"github.com/ekravo/Ekravo-review/v2/internal/review"
	"github.com/ekravo/Ekravo-review/v2/internal/server"
	"github.com/ekravo/Ekravo-review/v2/internal/store"
)

const (
	reviewTimeout = 10 * time.Minute
	maxInline     = 50
	maxBodyChars  = 60_000
	configTTL     = 5 * time.Minute
	// Small models truncate long JSON responses, so keep each section well
	// below the provider's character budget.
	maxSectionChars = 20_000
)

type cachedConfig struct {
	cfg     *config.RepoConfig
	expires time.Time
}

// App is the running bot.
type App struct {
	cfg      *config.Config
	log      *slog.Logger
	gh       *githubx.Client
	lm       *llm.Client
	research *research.Client
	st       *store.Store
	context  *repocontext.Engine
	pool     *queue.Pool
	metrics  *server.Metrics

	configs sync.Map
}

// New constructs the app and its clients.
func New(cfg *config.Config, log *slog.Logger) (*App, error) {
	if log == nil {
		log = slog.Default()
	}
	gh, err := githubx.New(cfg.GitHub)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(cfg.Store.Path)
	if err != nil {
		return nil, err
	}
	ctxEngine, err := newContextEngine(cfg, gh, log)
	if err != nil {
		st.Close()
		return nil, err
	}

	var webClient *research.Client
	if cfg.Research.Enabled {
		webClient = research.New(research.Options{
			BaseURL:    cfg.LLM.BaseURL,
			APIKey:     cfg.LLM.APIKey,
			Timeout:    cfg.Research.Timeout,
			MaxResults: cfg.Research.MaxResults,
		})
	}

	return &App{
		cfg:      cfg,
		log:      log,
		gh:       gh,
		lm:       llm.New(cfg.LLM),
		research: webClient,
		st:       st,
		context:  ctxEngine,
		pool:     queue.New(cfg.Bot.Workers, cfg.Bot.QueueSize, log),
		metrics:  &server.Metrics{},
	}, nil
}

// Close releases resources.
func (a *App) Close() error {
	if a.context != nil {
		_ = a.context.Close()
	}
	return a.st.Close()
} // Run serves webhooks or polls, depending on configuration, until ctx ends.
// The HTTP surface always runs so the dashboard, health checks, and metrics are
// available in polling mode too.
func (a *App) Run(ctx context.Context) error {
	a.pool.Start(ctx)

	srv := server.New(a.cfg, a.log, a.metrics, server.Handlers{
		OnPullRequest: func(ctx context.Context, repo string, number int, _ string, _ bool) error {
			if !a.watching(ctx, repo) {
				a.log.Debug("ignoring pull request from unwatched repository", "repo", repo, "pr", number)
				return nil
			}
			if err := a.enqueuePR(repo, number, false); err != nil {
				return server.ErrQueueFull
			}
			return nil
		},
		OnCommand: func(ctx context.Context, repo string, number int, command string) error {
			if !a.watching(ctx, repo) {
				a.log.Debug("ignoring command from unwatched repository", "repo", repo, "pr", number)
				return nil
			}
			if err := a.enqueueCommand(repo, number, command); err != nil {
				return server.ErrQueueFull
			}
			return nil
		},
		Ready:          func(ctx context.Context) error { return a.st.Ping(ctx) },
		AvailableRepos: a.gh.ListRepos,
		Store:          a.st,
	})

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	if a.cfg.WebhookMode() {
		select {
		case <-ctx.Done():
		case err := <-errCh:
			if err != nil {
				return err
			}
		}
	} else {
		p := poller.New(a.cfg, a.gh, a.log,
			func(_ context.Context, repo string, number int, _ string) { a.enqueuePR(repo, number, false) },
			func(_ context.Context, repo string, number int, command string) {
				a.enqueueCommand(repo, number, command)
			},
		)
		p.SetWatch(func(ctx context.Context) (bool, []string, error) {
			watch, err := a.st.Watch(ctx)
			if err != nil {
				return false, nil, err
			}
			return watch.All, watch.Repos, nil
		})
		done := make(chan struct{})
		go func() {
			p.Run(ctx)
			close(done)
		}()

		select {
		case <-done:
		case err := <-errCh:
			// A busy port must not stop reviews; log it and keep polling.
			if err != nil {
				a.log.Error("http surface stopped; dashboard and metrics are unavailable", "err", err)
			}
			<-done
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	a.pool.Shutdown(shutdownCtx)
	return nil
}

// Metrics exposes counters (used by tests and operators).
func (a *App) Metrics() *server.Metrics { return a.metrics }

// watching reports whether a repository is in the dashboard watch list. An
// unreadable setting fails open so a storage hiccup cannot stop reviews.
func (a *App) watching(ctx context.Context, repo string) bool {
	watch, err := a.st.Watch(ctx)
	if err != nil {
		a.log.Warn("could not read watch list; allowing review", "repo", repo, "err", err)
		return true
	}
	if watch.All {
		return true
	}
	for _, watched := range watch.Repos {
		if strings.EqualFold(watched, repo) {
			return true
		}
	}
	return false
}

func (a *App) enqueuePR(repo string, number int, force bool) error {
	key := fmt.Sprintf("%s#%d", repo, number)
	err := a.pool.SubmitOrJoin(key, func(ctx context.Context) {
		jobCtx, cancel := context.WithTimeout(ctx, reviewTimeout)
		defer cancel()
		if err := a.ReviewPR(jobCtx, repo, number, force); err != nil {
			a.metrics.Failures.Add(1)
			a.log.Error("review failed", "repo", repo, "pr", number, "err", err)
		}
	})
	if err != nil {
		a.log.Warn("review could not be queued", "key", key, "err", err)
	}
	return err
}

func (a *App) enqueueCommand(repo string, number int, command string) error {
	key := fmt.Sprintf("%s#%d#%s", repo, number, command)
	err := a.pool.SubmitOrJoin(key, func(ctx context.Context) {
		jobCtx, cancel := context.WithTimeout(ctx, reviewTimeout)
		defer cancel()
		if err := a.HandleCommand(jobCtx, repo, number, command); err != nil {
			a.metrics.Failures.Add(1)
			a.log.Error("command failed", "repo", repo, "pr", number, "command", command, "err", err)
		}
	})
	if err != nil {
		a.log.Warn("command could not be queued", "key", key, "err", err)
	}
	return err
}

// ReviewPR reviews one pull request. force skips the already-reviewed check,
// which is what the @bot review command needs.
func (a *App) ReviewPR(ctx context.Context, repo string, number int, force bool) error {
	started := time.Now()
	pr, err := a.gh.GetPullRequest(ctx, repo, number)
	if err != nil {
		return fmt.Errorf("fetch pull request: %w", err)
	}
	if pr.State != "" && pr.State != "open" {
		return nil
	}
	if a.cfg.Bot.SkipDrafts && pr.Draft {
		a.log.Debug("skipping draft PR", "repo", repo, "pr", number)
		return nil
	}

	already, err := a.st.IsReviewed(ctx, repo, number, pr.Head.SHA)
	if err != nil {
		return err
	}
	if already && !force {
		return nil
	}

	rc := a.repoConfig(ctx, repo)
	files, incremental, since, err := a.diffFor(ctx, repo, pr, force)
	if err != nil {
		return err
	}

	parsed := a.parseFiles(files, rc)
	if len(parsed) == 0 {
		a.log.Info("no reviewable files", "repo", repo, "pr", number)
		return a.st.SaveReview(ctx, &store.Review{
			Repo: repo, PR: number, HeadSHA: pr.Head.SHA, Verdict: "skipped",
		})
	}

	meta := metaOf(pr)
	truncated := len(files) > a.cfg.Bot.MaxFiles
	manifest := manifestOf(parsed)
	system := review.SystemPrompt(rc)
	if a.cfg.Context.Learnings {
		paths := make([]string, 0, len(parsed))
		for _, f := range parsed {
			paths = append(paths, f.Path)
		}
		if learned, lerr := a.st.LearningTexts(ctx, repo, paths); lerr == nil {
			system = review.SystemPromptWithLearnings(rc, learned)
		} else {
			a.log.Debug("could not load learnings", "repo", repo, "err", lerr)
		}
	}
	chunks := diff.ChunkFiles(parsed, min(a.cfg.LLM.ChunkChars, maxSectionChars))

	var findings []review.Finding
	var dropped []review.Dropped
	modelUsed := ""
	var promptTokens, completionTokens, contextFiles, contextChars int
	var contextMS int64

	// Small models are slow and error-prone per call, so pack multiple files
	// into each section. One bad section must not discard the whole review.
	sections := batchChunks(chunks, min(a.cfg.LLM.ChunkChars, maxSectionChars))
	failed := 0
	partial := truncated
	maxOut := a.cfg.LLM.MaxTokens
	if maxOut < 8192 {
		maxOut = 8192
	}

	for i, batch := range sections {
		var text strings.Builder
		truncatedChunk := false
		for _, ch := range batch {
			text.WriteString(diff.RenderNumberedHunks(ch.File, ch.Hunks))
			text.WriteByte('\n')
			truncatedChunk = truncatedChunk || ch.Truncated
		}
		partial = partial || truncatedChunk

		repoCtx, cstats := a.sectionContext(ctx, repo, pr.Base.SHA, batch)
		if repoctxMax := a.cfg.Context.MaxTotalChars; repoctxMax > 0 && repoCtx != "" {
			remaining := repoctxMax - contextChars
			if remaining <= 0 {
				repoCtx = ""
			} else if len(repoCtx) > remaining {
				repoCtx = truncateContext(repoCtx, remaining)
			}
		}
		contextChars += len(repoCtx)
		contextFiles += cstats.Files
		contextMS += cstats.IndexMS

		user := review.UserPromptWithContext(meta, manifest, repoCtx, text.String(),
			i, len(sections), truncatedChunk, incremental)

		a.metrics.LLMCalls.Add(1)
		res, err := a.lm.JSON(ctx, system, user, llm.Options{MaxTokens: maxOut})
		if err != nil {
			failed++
			partial = true
			a.log.Warn("review section failed; continuing with the rest",
				"repo", repo, "pr", number, "section", i+1, "of", len(sections), "err", err)
			continue
		}
		modelUsed = res.Model
		promptTokens += res.Usage.PromptTokens
		completionTokens += res.Usage.CompletionTokens

		parsedFindings, err := review.ParseFindings(res.Raw)
		if err != nil {
			failed++
			partial = true
			a.log.Warn("review section returned unparsable findings; continuing",
				"repo", repo, "pr", number, "section", i+1, "of", len(sections), "err", err)
			continue
		}
		kept, drops := review.Validate(parsedFindings, parsed, rc.MaxFindings)
		findings = append(findings, kept...)
		dropped = append(dropped, drops...)

		a.log.Info("section reviewed",
			"repo", repo, "pr", number, "section", i+1, "of", len(sections),
			"model", res.Model, "findings", len(kept), "dropped", len(drops))
	}

	if failed == len(sections) {
		reason := fmt.Sprintf("all %d review sections failed", len(sections))
		a.reportReviewFailure(ctx, repo, number, reason)
		return fmt.Errorf("%s", reason)
	}

	limit := rc.MaxFindings
	if a.cfg.LLM.MaxFindings > 0 && a.cfg.LLM.MaxFindings < limit {
		limit = a.cfg.LLM.MaxFindings
	}
	findings, more := review.Validate(findings, parsed, limit)
	dropped = append(dropped, more...)

	// Tool-use step: verify externally checkable claims against web sources
	// before the verdict is computed. Local logic findings are never sent.
	var evidenceDropped []review.Dropped
	findings, evidenceDropped = a.verifyFindings(ctx, meta, findings)
	dropped = append(dropped, evidenceDropped...)

	verdict := review.Decide(findings, rc.BlockOn)
	summary := review.Summary(review.SummaryInput{
		Meta:        meta,
		Findings:    findings,
		Verdict:     verdict,
		Dropped:     len(dropped),
		Model:       modelUsed,
		Handle:      a.cfg.Bot.Handle,
		Truncated:   truncated,
		Failed:      failed,
		Sections:    len(sections),
		Incremental: incremental,
		Commit:      pr.Head.SHA,
		Duration:    time.Since(started),
		RepoURL:     a.cfg.Bot.RepoURL,
	})
	if len(summary) > maxBodyChars {
		summary = summary[:maxBodyChars]
	}

	comments := make([]githubx.ReviewComment, 0, len(findings))
	for i, f := range findings {
		if i >= maxInline {
			break
		}
		comments = append(comments, githubx.ReviewComment{
			Path: f.File, Line: f.Line, Side: "RIGHT", Body: review.InlineBody(f),
		})
	}

	a.deletePrevious(ctx, repo, pr, force)

	event := review.ReviewEvent(verdict.Decision, a.cfg.Bot.AutoApprove)
	req := githubx.ReviewRequest{CommitID: pr.Head.SHA, Body: summary, Event: event, Comments: comments}
	reviewID, err := a.gh.CreateReview(ctx, repo, number, req)
	if err != nil && len(comments) > 0 {
		a.log.Warn("inline comments rejected; retrying with summary only", "repo", repo, "pr", number, "err", err)
		req.Comments = nil
		reviewID, err = a.gh.CreateReview(ctx, repo, number, req)
	}
	if err != nil && event != "COMMENT" {
		a.log.Warn("gated review rejected; retrying as COMMENT", "repo", repo, "pr", number, "err", err)
		req.Event = "COMMENT"
		reviewID, err = a.gh.CreateReview(ctx, repo, number, req)
	}
	if err != nil {
		return fmt.Errorf("create review: %w", err)
	}

	findingsJSON, _ := json.Marshal(findings)
	if err := a.st.SaveReview(ctx, &store.Review{
		Repo: repo, PR: number, Title: pr.Title, URL: pr.HTMLURL,
		HeadSHA: pr.Head.SHA, ReviewID: reviewID,
		Verdict: verdict.Decision, Findings: len(findings), Model: modelUsed,
		FindingsJSON: string(findingsJSON), DurationMS: time.Since(started).Milliseconds(),
		PromptTokens: promptTokens, CompletionTokens: completionTokens,
		ContextFiles: contextFiles, ContextChars: contextChars, ContextMS: contextMS,
		Files: len(parsed), Additions: additionsOf(parsed), Deletions: deletionsOf(parsed),
	}); err != nil {
		return err
	}

	if a.maybeAutoMerge(ctx, repo, pr, verdict, partial) {
		if err := a.st.MarkMerged(ctx, repo, number, pr.Head.SHA); err != nil {
			a.log.Warn("auto-merge: could not record merge", "repo", repo, "pr", number, "err", err)
		}
	}

	a.metrics.Reviews.Add(1)
	a.log.Info("review posted",
		"repo", repo, "pr", number, "verdict", verdict.Decision,
		"findings", len(findings), "model", modelUsed,
		"incremental", incremental, "since", since)
	return nil
}

// reportReviewFailure makes an incomplete review visible on the pull request
// instead of leaving the author with no signal. The message deliberately does
// not mention the bot handle so it cannot trigger a command loop.
func (a *App) reportReviewFailure(ctx context.Context, repo string, number int, reason string) {
	body := fmt.Sprintf("## 🔎 Ekravo Review — review failed\n\nI could not complete this review: %s. Nothing was approved or merged. Run the review command again to retry.\n", reason)
	if _, err := a.gh.CreateIssueComment(ctx, repo, number, body); err != nil {
		a.log.Warn("could not post review failure comment", "repo", repo, "pr", number, "err", err)
	}
}

// canAutoMerge is deliberately strict: an approving review of the exact same
// commit, an open non-draft PR, and GitHub reporting it mergeable and clean.
func canAutoMerge(pr *githubx.PullRequest, sha string) bool {
	return pr != nil &&
		pr.State == "open" &&
		!pr.Draft &&
		pr.Head.SHA == sha &&
		pr.Mergeable != nil && *pr.Mergeable &&
		pr.MergeableState == "clean"
}

func autoMergeEligible(verdict review.Verdict, partial bool) bool {
	return !partial && verdict.Decision == "approve"
}

func (a *App) maybeAutoMerge(ctx context.Context, repo string, pr *githubx.PullRequest, verdict review.Verdict, partial bool) bool {
	if !a.cfg.Bot.AutoMerge {
		return false
	}
	if !autoMergeEligible(verdict, partial) {
		a.log.Info("auto-merge skipped: review is not a complete approval",
			"repo", repo, "pr", pr.Number, "verdict", verdict.Decision, "partial", partial)
		return false
	}

	current := pr
	for attempt := 0; attempt < 3; attempt++ {
		if canAutoMerge(current, pr.Head.SHA) {
			break
		}
		// GitHub computes mergeability asynchronously; give it a moment.
		if current.Mergeable == nil || current.MergeableState == "" || current.MergeableState == "unknown" {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(2 * time.Second):
			}
			refreshed, err := a.gh.GetPullRequest(ctx, repo, pr.Number)
			if err != nil {
				a.log.Warn("auto-merge: refresh PR failed", "repo", repo, "pr", pr.Number, "err", err)
				return false
			}
			current = refreshed
			continue
		}
		a.log.Info("auto-merge skipped: PR is not clean",
			"repo", repo, "pr", pr.Number, "state", current.MergeableState)
		return false
	}

	if !canAutoMerge(current, pr.Head.SHA) {
		a.log.Info("auto-merge skipped: PR is not mergeable",
			"repo", repo, "pr", pr.Number, "state", current.MergeableState)
		return false
	}

	sha, err := a.gh.MergePullRequest(ctx, repo, pr.Number, githubx.MergeRequest{
		SHA:           pr.Head.SHA,
		MergeMethod:   a.cfg.Bot.AutoMergeMethod,
		CommitTitle:   pr.Title,
		CommitMessage: "Auto-merged by Ekravo Review after a clean review.",
	})
	if err != nil {
		a.log.Warn("auto-merge failed", "repo", repo, "pr", pr.Number, "err", err)
		return false
	}
	a.metrics.Merges.Add(1)
	a.log.Info("auto-merged", "repo", repo, "pr", pr.Number,
		"merge_sha", sha, "method", a.cfg.Bot.AutoMergeMethod)
	return true
}

func (a *App) deletePrevious(ctx context.Context, repo string, pr *githubx.PullRequest, force bool) {
	prev, ok, err := a.st.LatestReview(ctx, repo, pr.Number)
	if err != nil || !ok || prev.ReviewID == 0 || (!force && prev.HeadSHA == pr.Head.SHA) {
		return
	}
	if err := a.gh.DeleteReview(ctx, repo, pr.Number, prev.ReviewID); err != nil {
		a.log.Warn("could not delete previous review", "repo", repo, "pr", pr.Number, "err", err)
	}
}

// diffFor picks an incremental comparison when the PR moved since the last
// review, otherwise the full diff. Empty legacy SHAs always mean full diff.
func (a *App) diffFor(ctx context.Context, repo string, pr *githubx.PullRequest, force bool) ([]githubx.ChangedFile, bool, string, error) {
	prev, ok, err := a.st.LatestReview(ctx, repo, pr.Number)
	if err != nil {
		return nil, false, "", err
	}

	if !force && ok && prev.HeadSHA != "" && prev.HeadSHA != pr.Head.SHA && a.cfg.Bot.RecheckUpdated {
		files, err := a.gh.CompareFiles(ctx, repo, prev.HeadSHA, pr.Head.SHA)
		if err == nil && len(files) > 0 {
			return files, true, prev.HeadSHA, nil
		}
		if err != nil {
			a.log.Warn("incremental comparison failed; using full diff", "repo", repo, "pr", pr.Number, "err", err)
		}
	}

	files, err := a.gh.ListChangedFiles(ctx, repo, pr.Number)
	if err != nil {
		return nil, false, "", fmt.Errorf("list changed files: %w", err)
	}
	return files, false, "", nil
}

func (a *App) parseFiles(files []githubx.ChangedFile, rc *config.RepoConfig) []*diff.File {
	patterns := append(append([]string{}, a.cfg.Bot.IgnorePatterns...), rc.IgnorePatterns...)
	out := make([]*diff.File, 0, len(files))

	for _, cf := range files {
		if cf.Status == "removed" || strings.TrimSpace(cf.Patch) == "" {
			continue
		}
		if ignored(cf.Filename, patterns) {
			continue
		}
		if len(out) >= a.cfg.Bot.MaxFiles {
			a.log.Warn("file limit reached; remaining files not reviewed",
				"limit", a.cfg.Bot.MaxFiles, "skipped", len(files)-len(out))
			break
		}
		f, err := diff.ParseFilePatch(cf.Filename, cf.Status, cf.Patch)
		if err != nil {
			a.log.Warn("could not parse file patch", "file", cf.Filename, "err", err)
			continue
		}
		trimLines(f, a.cfg.Bot.MaxLinesPerFile)
		out = append(out, f)
	}
	return out
}

// batchChunks groups consecutive per-file chunks into sections that fit the
// character budget, so a 50-file PR takes a handful of model calls rather than
// one call per file. Findings still validate against the full parsed file set.
func batchChunks(chunks []diff.Chunk, maxChars int) [][]diff.Chunk {
	if maxChars <= 0 {
		maxChars = 120_000
	}
	var out [][]diff.Chunk
	var cur []diff.Chunk
	chars := 0
	for _, ch := range chunks {
		if len(cur) > 0 && chars+ch.Chars > maxChars {
			out = append(out, cur)
			cur = nil
			chars = 0
		}
		cur = append(cur, ch)
		chars += ch.Chars
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func trimLines(f *diff.File, max int) {
	if max <= 0 {
		return
	}
	count := 0
	kept := f.Hunks[:0]
	for _, h := range f.Hunks {
		if count >= max {
			break
		}
		remaining := max - count
		if len(h.Lines) > remaining {
			h.Lines = h.Lines[:remaining]
			h.OldCount, h.NewCount = 0, 0
			for _, l := range h.Lines {
				if l.Kind != diff.Added {
					h.OldCount++
				}
				if l.Kind != diff.Removed {
					h.NewCount++
				}
			}
		}
		count += len(h.Lines)
		kept = append(kept, h)
	}
	f.Hunks = kept
}

func ignored(name string, patterns []string) bool {
	base := pathpkg.Base(name)
	for _, p := range patterns {
		if ok, _ := pathpkg.Match(p, name); ok {
			return true
		}
		if ok, _ := pathpkg.Match(p, base); ok {
			return true
		}
	}
	return false
}

func (a *App) repoConfig(ctx context.Context, repo string) *config.RepoConfig {
	if v, ok := a.configs.Load(repo); ok {
		if entry, ok := v.(cachedConfig); ok && time.Now().Before(entry.expires) {
			return entry.cfg
		}
	}

	rc := config.DefaultRepoConfig()
	data, err := a.gh.GetFile(ctx, repo, ".ekravo.yml")
	switch {
	case err == nil:
		parsed, perr := config.ParseRepoConfig(data)
		if perr != nil {
			a.log.Warn("invalid .ekravo.yml; using defaults", "repo", repo, "err", perr)
		} else {
			rc = parsed
		}
	case errors.Is(err, githubx.ErrNotFound):
		// no per-repo config
	default:
		a.log.Warn("could not read .ekravo.yml", "repo", repo, "err", err)
	}

	a.configs.Store(repo, cachedConfig{cfg: rc, expires: time.Now().Add(configTTL)})
	return rc
}

// HandleCommand executes @bot review|explain|summarize|recheck|learn|forget|learnings.
func (a *App) HandleCommand(ctx context.Context, repo string, number int, command string) error {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return fmt.Errorf("empty command")
	}
	cmd := strings.ToLower(fields[0])
	args := strings.TrimSpace(strings.TrimPrefix(command, fields[0]))

	switch cmd {
	case "review", "recheck":
		return a.ReviewPR(ctx, repo, number, true)
	case "explain", "summarize":
		return a.commandResponse(ctx, repo, number, cmd)
	case "learn":
		if args == "" {
			return a.commandReply(ctx, repo, number, "Usage: `@"+a.cfg.Bot.Handle+" learn <rule>`. The rule is stored for this repository only.")
		}
		if _, err := a.st.AddLearning(ctx, repo, "", args, "chat"); err != nil {
			return err
		}
		return a.commandReply(ctx, repo, number, "Learned. Future reviews of this repository will apply it when relevant.")
	case "forget":
		id, err := strconv.ParseInt(strings.TrimSpace(args), 10, 64)
		if err != nil || id <= 0 {
			return a.commandReply(ctx, repo, number, "Usage: `@"+a.cfg.Bot.Handle+" forget <id>` (see `"+a.cfg.Bot.Handle+" learnings`).")
		}
		ok, err := a.st.DeleteLearning(ctx, repo, id)
		if err != nil {
			return err
		}
		if !ok {
			return a.commandReply(ctx, repo, number, fmt.Sprintf("No learning `%d` in this repository.", id))
		}
		return a.commandReply(ctx, repo, number, fmt.Sprintf("Forgot learning `%d`.", id))
	case "learnings":
		list, err := a.st.ListLearnings(ctx, repo, true)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return a.commandReply(ctx, repo, number, "No learnings stored for this repository.")
		}
		var b strings.Builder
		b.WriteString("## 🧠 Repository learnings\n\n")
		for _, l := range list {
			state := "on"
			if !l.Enabled {
				state = "off"
			}
			fmt.Fprintf(&b, "- `%d` [%s] %s\n", l.ID, state, l.Text)
		}
		return a.commandReply(ctx, repo, number, b.String())
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func (a *App) commandReply(ctx context.Context, repo string, number int, body string) error {
	_, err := a.gh.CreateIssueComment(ctx, repo, number, body)
	return err
}

func (a *App) commandResponse(ctx context.Context, repo string, number int, command string) error {
	pr, err := a.gh.GetPullRequest(ctx, repo, number)
	if err != nil {
		return err
	}
	rc := a.repoConfig(ctx, repo)
	files, err := a.gh.ListChangedFiles(ctx, repo, number)
	if err != nil {
		return err
	}
	parsed := a.parseFiles(files, rc)

	var b strings.Builder
	for _, f := range parsed {
		b.WriteString(diff.RenderHunks(f, f.Hunks))
	}
	text := b.String()
	if len(text) > a.cfg.LLM.ChunkChars {
		text = text[:a.cfg.LLM.ChunkChars] + "\n… (truncated)"
	}

	meta := metaOf(pr)
	var system, user, title string
	if command == "explain" {
		system, user = review.ExplainPrompt(meta, text)
		title = "Explanation"
	} else {
		system, user = review.SummarizePrompt(meta, text)
		title = "Summary"
	}

	a.metrics.LLMCalls.Add(1)
	res, err := a.lm.Chat(ctx, system, user, llm.Options{MaxTokens: 900})
	if err != nil {
		return err
	}

	body := fmt.Sprintf("## 🔎 Ekravo Review %s\n\n%s\n\n---\n\n%s",
		title, res.Content, review.CommandsFooter(a.cfg.Bot.Handle))
	_, err = a.gh.CreateIssueComment(ctx, repo, number, body)
	return err
}

func metaOf(pr *githubx.PullRequest) review.Meta {
	labels := make([]string, 0, len(pr.Labels))
	for _, l := range pr.Labels {
		labels = append(labels, l.Name)
	}
	return review.Meta{
		Number:       pr.Number,
		Repo:         pr.Base.Repo.FullName,
		URL:          pr.HTMLURL,
		Title:        pr.Title,
		Body:         pr.Body,
		Author:       pr.User.Login,
		Base:         pr.Base.Ref,
		Head:         pr.Head.Ref,
		Commits:      pr.Commits,
		ChangedFiles: pr.ChangedFiles,
		Additions:    pr.Additions,
		Deletions:    pr.Deletions,
		Labels:       labels,
	}
}

func manifestOf(files []*diff.File) string {
	summaries := make([]review.FileSummary, 0, len(files))
	for _, f := range files {
		summaries = append(summaries, review.FileSummary{
			Path: f.Path, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions,
			Language: f.Meta.Language, Role: f.Meta.Role, Generated: f.Meta.Generated,
		})
	}
	return review.Manifest(summaries)
}

// truncateContext cuts at a line boundary so a reference header is never
// followed by a half line or a split UTF-8 sequence.
func truncateContext(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i+1]
	}
	return cut
}

// sectionContext retrieves validated reference code for one section. Shadow
// mode builds and measures it without sending it, so rollout can be judged on
// real repositories before a prompt changes.
func (a *App) sectionContext(ctx context.Context, repo, sha string, chunks []diff.Chunk) (string, repocontext.Stats) {
	if a.context == nil || a.cfg.Context.Mode == "off" || a.cfg.Context.Mode == "" {
		return "", repocontext.Stats{}
	}
	seen := map[string]bool{}
	files := make([]*diff.File, 0, len(chunks))
	for _, ch := range chunks {
		if ch.File == nil || seen[ch.File.Path] {
			continue
		}
		seen[ch.File.Path] = true
		files = append(files, ch.File)
	}

	text, stats := a.context.Context(ctx, repo, sha, files)
	if a.cfg.Context.Mode == "shadow" {
		if text != "" {
			a.log.Info("context shadow", "repo", repo, "files", stats.Files,
				"chars", stats.Chars, "symbols", stats.Symbols,
				"cached", stats.Cached, "partial", stats.Partial)
		}
		return "", stats
	}
	if stats.Partial {
		a.log.Info("context is partial", "repo", repo, "source", stats.IndexSource)
	}
	return text, stats
}

func additionsOf(files []*diff.File) int {
	total := 0
	for _, f := range files {
		total += f.Additions
	}
	return total
}

func deletionsOf(files []*diff.File) int {
	total := 0
	for _, f := range files {
		total += f.Deletions
	}
	return total
}
