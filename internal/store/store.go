// Package store persists review state so restarts never re-review a commit or
// stack duplicate comments on a PR. It also backs the embedded dashboard.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Review is one review attempt for a (repo, PR, head SHA).
type Review struct {
	ID               int64     `json:"id"`
	Repo             string    `json:"repo"`
	PR               int       `json:"pr"`
	Title            string    `json:"title"`
	URL              string    `json:"url"`
	HeadSHA          string    `json:"head_sha"`
	ReviewID         int64     `json:"review_id"`
	Verdict          string    `json:"verdict"`
	Findings         int       `json:"findings"`
	FindingsJSON     string    `json:"-"`
	Model            string    `json:"model"`
	DurationMS       int64     `json:"duration_ms"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	ContextFiles     int       `json:"context_files"`
	ContextChars     int       `json:"context_chars"`
	ContextMS        int64     `json:"context_ms"`
	Files            int       `json:"files"`
	Additions        int       `json:"additions"`
	Deletions        int       `json:"deletions"`
	Merged           bool      `json:"merged"`
	State            string    `json:"state"`
	CreatedAt        time.Time `json:"created_at"`
}

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB

	learnOnce sync.Once
	learnErr  error
}

// Open creates the database and applies the schema.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping %s: %w", path, err)
	}

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS reviews (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	repo          TEXT    NOT NULL,
	pr            INTEGER NOT NULL,
	title         TEXT    NOT NULL DEFAULT '',
	url           TEXT    NOT NULL DEFAULT '',
	head_sha      TEXT    NOT NULL,
	review_id     INTEGER NOT NULL DEFAULT 0,
	verdict       TEXT    NOT NULL DEFAULT '',
	findings      INTEGER NOT NULL DEFAULT 0,
	findings_json TEXT    NOT NULL DEFAULT '',
	model         TEXT    NOT NULL DEFAULT '',
	duration_ms   INTEGER NOT NULL DEFAULT 0,
	files         INTEGER NOT NULL DEFAULT 0,
	additions     INTEGER NOT NULL DEFAULT 0,
	deletions     INTEGER NOT NULL DEFAULT 0,
	merged        INTEGER NOT NULL DEFAULT 0,
	state         TEXT    NOT NULL DEFAULT '',
	prompt_tokens     INTEGER NOT NULL DEFAULT 0,
	completion_tokens INTEGER NOT NULL DEFAULT 0,
	context_files     INTEGER NOT NULL DEFAULT 0,
	context_chars     INTEGER NOT NULL DEFAULT 0,
	context_ms        INTEGER NOT NULL DEFAULT 0,
	created_at    TEXT    NOT NULL,
	UNIQUE(repo, pr, head_sha)
);
CREATE INDEX IF NOT EXISTS idx_reviews_pr ON reviews(repo, pr, id DESC);
CREATE INDEX IF NOT EXISTS idx_reviews_created ON reviews(created_at DESC);
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// Older databases predate the dashboard columns. Add anything missing
	// instead of dropping the table, so upgrades keep their history.
	existing := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(reviews)`)
	if err != nil {
		return fmt.Errorf("inspect schema: %w", err)
	}
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("inspect schema: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	columns := []struct{ name, ddl string }{
		{"title", "TEXT NOT NULL DEFAULT ''"},
		{"url", "TEXT NOT NULL DEFAULT ''"},
		{"findings_json", "TEXT NOT NULL DEFAULT ''"},
		{"duration_ms", "INTEGER NOT NULL DEFAULT 0"},
		{"files", "INTEGER NOT NULL DEFAULT 0"},
		{"additions", "INTEGER NOT NULL DEFAULT 0"},
		{"deletions", "INTEGER NOT NULL DEFAULT 0"},
		{"merged", "INTEGER NOT NULL DEFAULT 0"},
		{"state", "TEXT NOT NULL DEFAULT ''"},
		{"prompt_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"completion_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"context_files", "INTEGER NOT NULL DEFAULT 0"},
		{"context_chars", "INTEGER NOT NULL DEFAULT 0"},
		{"context_ms", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, c := range columns {
		if existing[c.name] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE reviews ADD COLUMN %s %s", c.name, c.ddl)); err != nil {
			return fmt.Errorf("add column %s: %w", c.name, err)
		}
	}
	return nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// Ping verifies the database is still usable (used by /readyz).
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// SaveReview upserts the record for a commit.
func (s *Store) SaveReview(ctx context.Context, r *Review) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	merged := 0
	if r.Merged {
		merged = 1
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO reviews (repo, pr, title, url, head_sha, review_id, verdict, findings,
	findings_json, model, duration_ms, files, additions, deletions, merged, state,
	prompt_tokens, completion_tokens, context_files, context_chars, context_ms, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(repo, pr, head_sha) DO UPDATE SET
	title         = excluded.title,
	url           = excluded.url,
	review_id     = excluded.review_id,
	verdict       = excluded.verdict,
	findings      = excluded.findings,
	findings_json = excluded.findings_json,
	model         = excluded.model,
	duration_ms   = excluded.duration_ms,
	files         = excluded.files,
	additions     = excluded.additions,
	deletions     = excluded.deletions,
	merged        = CASE WHEN excluded.merged = 1 THEN 1 ELSE reviews.merged END,
	state         = CASE WHEN reviews.state IN ('merged', 'closed') THEN reviews.state ELSE excluded.state END,
	prompt_tokens     = excluded.prompt_tokens,
	completion_tokens = excluded.completion_tokens,
	context_files     = excluded.context_files,
	context_chars     = excluded.context_chars,
	context_ms        = excluded.context_ms,
	created_at    = excluded.created_at`,
		r.Repo, r.PR, r.Title, r.URL, r.HeadSHA, r.ReviewID, r.Verdict, r.Findings,
		r.FindingsJSON, r.Model, r.DurationMS, r.Files, r.Additions, r.Deletions,
		merged, r.State, r.PromptTokens, r.CompletionTokens, r.ContextFiles, r.ContextChars,
		r.ContextMS, r.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("save review: %w", err)
	}
	return nil
}

// MarkMerged records that the reviewed commit was merged.
func (s *Store) MarkMerged(ctx context.Context, repo string, pr int, sha string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE reviews SET merged = 1, state = 'merged' WHERE repo = ? AND pr = ? AND head_sha = ?`,
		repo, pr, sha)
	if err != nil {
		return fmt.Errorf("mark merged: %w", err)
	}
	return nil
}

// SetPRState records the GitHub state of a pull request on every review row for
// it. "merged" and "closed" move the review out of the active review queue.
func (s *Store) SetPRState(ctx context.Context, repo string, pr int, state string) error {
	switch state {
	case "open", "merged", "closed":
	default:
		return fmt.Errorf("invalid PR state %q", state)
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE reviews SET state = ? WHERE repo = ? AND pr = ?`, state, repo, pr)
	if err != nil {
		return fmt.Errorf("set PR state: %w", err)
	}
	return nil
}

// PRRef identifies a pull request that has been reviewed at least once.
type PRRef struct {
	Repo string `json:"repo"`
	PR   int    `json:"pr"`
}

// UnknownPRStates lists reviewed PRs whose state predates state tracking,
// newest first. The app reconciles them against GitHub after an upgrade.
func (s *Store) UnknownPRStates(ctx context.Context, limit int) ([]PRRef, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT repo, pr FROM reviews WHERE state = '' GROUP BY repo, pr ORDER BY MAX(id) DESC LIMIT ?`,
		limit)
	if err != nil {
		return nil, fmt.Errorf("unknown PR states: %w", err)
	}
	defer rows.Close()

	var out []PRRef
	for rows.Next() {
		var ref PRRef
		if err := rows.Scan(&ref.Repo, &ref.PR); err != nil {
			return nil, fmt.Errorf("unknown PR states: %w", err)
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// IsReviewed reports whether this exact commit was already handled.
func (s *Store) IsReviewed(ctx context.Context, repo string, pr int, sha string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM reviews WHERE repo = ? AND pr = ? AND head_sha = ? LIMIT 1`,
		repo, pr, sha).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("is reviewed: %w", err)
	}
	return true, nil
}

const reviewColumns = `id, repo, pr, title, url, head_sha, review_id, verdict, findings,
	findings_json, model, duration_ms, files, additions, deletions, merged, state,
	prompt_tokens, completion_tokens, context_files, context_chars, context_ms, created_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanReview(row scanner) (*Review, error) {
	var r Review
	var createdAt string
	var merged int
	if err := row.Scan(&r.ID, &r.Repo, &r.PR, &r.Title, &r.URL, &r.HeadSHA, &r.ReviewID,
		&r.Verdict, &r.Findings, &r.FindingsJSON, &r.Model, &r.DurationMS,
		&r.Files, &r.Additions, &r.Deletions, &merged, &r.State,
		&r.PromptTokens, &r.CompletionTokens, &r.ContextFiles, &r.ContextChars,
		&r.ContextMS, &createdAt); err != nil {
		return nil, err
	}
	r.Merged = merged == 1
	r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &r, nil
}

// LatestReview returns the most recent record for a PR.
func (s *Store) LatestReview(ctx context.Context, repo string, pr int) (*Review, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+reviewColumns+` FROM reviews WHERE repo = ? AND pr = ? ORDER BY id DESC LIMIT 1`,
		repo, pr)
	r, err := scanReview(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("latest review: %w", err)
	}
	return r, true, nil
}

// ReviewByID loads one review, including its stored findings.
func (s *Store) ReviewByID(ctx context.Context, id int64) (*Review, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+reviewColumns+` FROM reviews WHERE id = ?`, id)
	r, err := scanReview(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("review %d: %w", id, err)
	}
	return r, true, nil
}

// ReviewFilter narrows a review listing.
type ReviewFilter struct {
	Verdict string
	Query   string
	Sort    string
	Limit   int
	Offset  int
}

// ListReviews returns newest-first reviews and the total matching the filter.
func (s *Store) ListReviews(ctx context.Context, f ReviewFilter) ([]Review, int, error) {
	where := []string{"1=1"}
	args := []any{}

	if v := strings.ToLower(strings.TrimSpace(f.Verdict)); v != "" && v != "all" {
		where = append(where, "verdict = ?")
		args = append(args, v)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + q + "%"
		where = append(where, "(repo LIKE ? OR title LIKE ? OR CAST(pr AS TEXT) LIKE ?)")
		args = append(args, like, like, like)
	}

	order := "id DESC"
	if strings.EqualFold(strings.TrimSpace(f.Sort), "repo") {
		order = "repo COLLATE NOCASE ASC, id DESC"
	}

	clause := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM reviews WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count reviews: %w", err)
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+reviewColumns+` FROM reviews WHERE `+clause+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list reviews: %w", err)
	}
	defer rows.Close()

	out := make([]Review, 0, limit)
	for rows.Next() {
		r, err := scanReview(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("list reviews: %w", err)
		}
		out = append(out, *r)
	}
	return out, total, rows.Err()
}

// ReviewCounts groups matching reviews by verdict. The dashboard uses it for
// the Now/Next column totals without paging through the whole history.
func (s *Store) ReviewCounts(ctx context.Context, query string) (map[string]int, error) {
	where := "1=1"
	args := []any{}
	if q := strings.TrimSpace(query); q != "" {
		like := "%" + q + "%"
		where = "(repo LIKE ? OR title LIKE ? OR CAST(pr AS TEXT) LIKE ?)"
		args = append(args, like, like, like)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT verdict, COUNT(*) FROM reviews WHERE `+where+` GROUP BY verdict`, args...)
	if err != nil {
		return nil, fmt.Errorf("count reviews by verdict: %w", err)
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var verdict string
		var count int
		if err := rows.Scan(&verdict, &count); err != nil {
			return nil, fmt.Errorf("count reviews by verdict: %w", err)
		}
		out[verdict] = count
	}
	return out, rows.Err()
}

// ActiveReviewCounts groups reviews that are still live by verdict. Merged and
// closed pull requests are excluded so Now/Next totals stay actionable.
func (s *Store) ActiveReviewCounts(ctx context.Context, query string) (map[string]int, error) {
	where := "state NOT IN ('merged', 'closed')"
	args := []any{}
	if q := strings.TrimSpace(query); q != "" {
		like := "%" + q + "%"
		where += " AND (repo LIKE ? OR title LIKE ? OR CAST(pr AS TEXT) LIKE ?)"
		args = append(args, like, like, like)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT verdict, COUNT(*) FROM reviews WHERE `+where+` GROUP BY verdict`, args...)
	if err != nil {
		return nil, fmt.Errorf("count active reviews by verdict: %w", err)
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var verdict string
		var count int
		if err := rows.Scan(&verdict, &count); err != nil {
			return nil, fmt.Errorf("count active reviews by verdict: %w", err)
		}
		out[verdict] = count
	}
	return out, rows.Err()
}

// ReviewStateCounts groups every matching review by PR state. The dashboard
// uses it to total the Done column independently of pagination.
func (s *Store) ReviewStateCounts(ctx context.Context, query string) (map[string]int, error) {
	where := "1=1"
	args := []any{}
	if q := strings.TrimSpace(query); q != "" {
		like := "%" + q + "%"
		where = "(repo LIKE ? OR title LIKE ? OR CAST(pr AS TEXT) LIKE ?)"
		args = append(args, like, like, like)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT state, COUNT(*) FROM reviews WHERE `+where+` GROUP BY state`, args...)
	if err != nil {
		return nil, fmt.Errorf("count reviews by state: %w", err)
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, fmt.Errorf("count reviews by state: %w", err)
		}
		out[state] = count
	}
	return out, rows.Err()
}

// Totals are the headline counters on the dashboard.
type Totals struct {
	Reviews     int `json:"reviews"`
	Approve     int `json:"approve"`
	Changes     int `json:"changes"`
	Suggestions int `json:"suggestions"`
	Findings    int `json:"findings"`
	Repos       int `json:"repos"`
	Merged      int `json:"merged"`
	Files       int `json:"files"`
}

// Day is one point in the activity series.
type Day struct {
	Date     string `json:"date"`
	Reviews  int    `json:"reviews"`
	Findings int    `json:"findings"`
}

// Overview is everything the overview panel needs in one request.
type Overview struct {
	Totals Totals   `json:"totals"`
	Series []Day    `json:"series"`
	Recent []Review `json:"recent"`
}

// Overview aggregates totals, a daily series, and recent reviews.
func (s *Store) Overview(ctx context.Context, days int) (*Overview, error) {
	if days < 1 {
		days = 14
	}
	if days > 90 {
		days = 90
	}

	out := &Overview{Totals: Totals{}, Series: make([]Day, 0, days), Recent: []Review{}}

	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*),
	COALESCE(SUM(CASE WHEN verdict = 'approve' THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN verdict = 'changes' THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN verdict = 'suggestions' THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(findings), 0),
	COUNT(DISTINCT repo),
	COALESCE(SUM(merged), 0),
	COALESCE(SUM(files), 0)
FROM reviews`).Scan(&out.Totals.Reviews, &out.Totals.Approve, &out.Totals.Changes,
		&out.Totals.Suggestions, &out.Totals.Findings, &out.Totals.Repos,
		&out.Totals.Merged, &out.Totals.Files)
	if err != nil {
		return nil, fmt.Errorf("overview totals: %w", err)
	}

	start := time.Now().UTC().AddDate(0, 0, -(days - 1))
	startDay := start.Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx, `
SELECT substr(created_at, 1, 10) AS day, COUNT(*), COALESCE(SUM(findings), 0)
FROM reviews WHERE substr(created_at, 1, 10) >= ?
GROUP BY day ORDER BY day`, startDay)
	if err != nil {
		return nil, fmt.Errorf("overview series: %w", err)
	}
	defer rows.Close()

	byDay := map[string]Day{}
	for rows.Next() {
		var d Day
		if err := rows.Scan(&d.Date, &d.Reviews, &d.Findings); err != nil {
			return nil, fmt.Errorf("overview series: %w", err)
		}
		byDay[d.Date] = d
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := 0; i < days; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		if d, ok := byDay[date]; ok {
			out.Series = append(out.Series, d)
			continue
		}
		out.Series = append(out.Series, Day{Date: date})
	}

	recent, _, err := s.ListReviews(ctx, ReviewFilter{Limit: 6})
	if err != nil {
		return nil, err
	}
	if recent != nil {
		out.Recent = recent
	}
	return out, nil
}

// RepoStat is the per-repository rollup.
type RepoStat struct {
	Repo         string    `json:"repo"`
	Reviews      int       `json:"reviews"`
	Findings     int       `json:"findings"`
	LastVerdict  string    `json:"last_verdict"`
	LastReviewAt time.Time `json:"last_review_at"`
}

// RepoStats returns repositories ordered by review volume.
func (s *Store) RepoStats(ctx context.Context) ([]RepoStat, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT repo, COUNT(*), COALESCE(SUM(findings), 0), MAX(created_at),
	(SELECT verdict FROM reviews r2 WHERE r2.repo = reviews.repo ORDER BY r2.id DESC LIMIT 1)
FROM reviews
GROUP BY repo
ORDER BY COUNT(*) DESC, repo ASC`)
	if err != nil {
		return nil, fmt.Errorf("repo stats: %w", err)
	}
	defer rows.Close()

	out := []RepoStat{}
	for rows.Next() {
		var r RepoStat
		var last string
		if err := rows.Scan(&r.Repo, &r.Reviews, &r.Findings, &last, &r.LastVerdict); err != nil {
			return nil, fmt.Errorf("repo stats: %w", err)
		}
		r.LastReviewAt, _ = time.Parse(time.RFC3339, last)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ParseFindings decodes the stored findings payload.
func (r *Review) ParseFindings() []json.RawMessage {
	if strings.TrimSpace(r.FindingsJSON) == "" {
		return []json.RawMessage{}
	}
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(r.FindingsJSON), &items); err != nil {
		return []json.RawMessage{}
	}
	return items
}

// ImportLegacy migrates the old .processed_prs.json format. Both shapes are
// accepted: {"repo":{"12":"sha"}} and {"repo":[12,13]}.
func (s *Store) ImportLegacy(ctx context.Context, path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read legacy state: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return 0, fmt.Errorf("parse legacy state: %w", err)
	}

	count := 0
	for repo, value := range raw {
		var byPR map[string]string
		if err := json.Unmarshal(value, &byPR); err == nil {
			for prStr, sha := range byPR {
				var pr int
				if _, err := fmt.Sscanf(prStr, "%d", &pr); err != nil {
					continue
				}
				if err := s.SaveReview(ctx, &Review{Repo: repo, PR: pr, HeadSHA: sha, Verdict: "legacy"}); err != nil {
					return count, err
				}
				count++
			}
			continue
		}

		var list []int
		if err := json.Unmarshal(value, &list); err == nil {
			for _, pr := range list {
				if err := s.SaveReview(ctx, &Review{Repo: repo, PR: pr, Verdict: "legacy"}); err != nil {
					return count, err
				}
				count++
			}
		}
	}
	return count, nil
}
