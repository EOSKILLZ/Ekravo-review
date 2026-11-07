// Package poller reviews open PRs on a timer. It also scans PR comments for
// @handle commands, which the previous Python bot only supported via webhooks.
package poller

import (
	"context"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/config"
	"github.com/EOSKILLZ/ekravo-review/v2/internal/githubx"
)

// Poller runs the polling loop.
type Poller struct {
	cfg     *config.Config
	gh      *githubx.Client
	log     *slog.Logger
	onPR    func(ctx context.Context, repo string, number int, sha string)
	onCmd   func(ctx context.Context, repo string, number int, command string)
	command *regexp.Regexp

	lastSeen map[string]time.Time
}

// New builds a poller.
func New(cfg *config.Config, gh *githubx.Client, log *slog.Logger,
	onPR func(context.Context, string, int, string),
	onCmd func(context.Context, string, int, string)) *Poller {
	if log == nil {
		log = slog.Default()
	}
	return &Poller{
		cfg:      cfg,
		gh:       gh,
		log:      log,
		onPR:     onPR,
		onCmd:    onCmd,
		command:  regexp.MustCompile(`(?i)@` + regexp.QuoteMeta(cfg.Bot.Handle) + `\s+(review|explain|summarize|recheck|learn|forget|learnings)\b([^\n]*)`),
		lastSeen: map[string]time.Time{},
	}
}

// Run blocks until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	interval := p.cfg.Bot.PollInterval
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}
	p.log.Info("polling mode started", "interval", interval.String(), "repos", len(p.cfg.GitHub.Repos))

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	p.cycle(ctx)
	for {
		select {
		case <-ctx.Done():
			p.log.Info("poller stopped")
			return
		case <-ticker.C:
			p.cycle(ctx)
		}
	}
}

func (p *Poller) cycle(ctx context.Context) {
	for _, repo := range p.cfg.GitHub.Repos {
		if _, ignored := p.cfg.GitHub.IgnoreRepos[strings.ToLower(repo)]; ignored {
			continue
		}
		p.pollRepo(ctx, repo)
	}
}

func (p *Poller) pollRepo(ctx context.Context, repo string) {
	prs, err := p.gh.ListOpenPullRequests(ctx, repo)
	if err != nil {
		p.log.Error("list pull requests failed", "repo", repo, "err", err)
		return
	}
	for _, pr := range prs {
		if p.cfg.Bot.SkipDrafts && pr.Draft {
			continue
		}
		if p.onPR != nil {
			p.onPR(ctx, repo, pr.Number, pr.Head.SHA)
		}
	}
	p.scanComments(ctx, repo)
}

func (p *Poller) scanComments(ctx context.Context, repo string) {
	if p.onCmd == nil {
		return
	}
	since, ok := p.lastSeen[repo]
	if !ok {
		// First pass: remember the watermark so old comments are not replayed.
		p.lastSeen[repo] = time.Now().UTC().Add(-time.Minute)
		return
	}

	comments, err := p.gh.ListIssueComments(ctx, repo, since.Add(-time.Minute))
	if err != nil {
		p.log.Error("list comments failed", "repo", repo, "err", err)
		return
	}
	watermark := since
	for _, c := range comments {
		if c.CreatedAt.After(watermark) {
			watermark = c.CreatedAt
		}
		if c.User.Login == p.cfg.Bot.Handle {
			continue // never execute our own comments
		}
		number, ok := pullNumber(c.IssueURL)
		if !ok {
			continue // regular issue, not a PR
		}
		m := p.command.FindStringSubmatch(c.Body)
		if len(m) < 2 {
			continue
		}
		cmd := strings.ToLower(m[1])
		if args := strings.TrimSpace(m[2]); args != "" && (cmd == "learn" || cmd == "forget") {
			cmd += " " + args
		}
		p.log.Info("comment command", "repo", repo, "pr", number, "command", m[1])
		p.onCmd(ctx, repo, number, cmd)
	}
	p.lastSeen[repo] = watermark
}

func pullNumber(issueURL string) (int, bool) {
	i := strings.LastIndex(issueURL, "/pull/")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(issueURL[i+len("/pull/"):])
	if err != nil {
		return 0, false
	}
	return n, true
}
