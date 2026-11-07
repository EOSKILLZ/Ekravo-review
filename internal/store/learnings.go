package store

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"
)

// Learning is one repo-scoped instruction learned from a review thread.
type Learning struct {
	ID        int64     `json:"id"`
	Repo      string    `json:"repo"`
	PathGlob  string    `json:"path_glob"`
	Text      string    `json:"text"`
	Source    string    `json:"source"`
	Enabled   bool      `json:"enabled"`
	Hits      int       `json:"hits"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) ensureLearnings(ctx context.Context) error {
	s.learnOnce.Do(func() { s.learnErr = s.initLearnings(ctx) })
	return s.learnErr
}

func (s *Store) initLearnings(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS learnings (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  repo       TEXT NOT NULL,
  path_glob  TEXT NOT NULL DEFAULT '',
  text       TEXT NOT NULL,
  source     TEXT NOT NULL DEFAULT 'chat',
  enabled    INTEGER NOT NULL DEFAULT 1,
  hits       INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_learnings_repo ON learnings(repo, enabled);
`)
	return err
}

// AddLearning stores one instruction. Text is bounded and single-line so it
// cannot smuggle a large prompt fragment into every future review.
func (s *Store) AddLearning(ctx context.Context, repo, pathGlob, text, source string) (int64, error) {
	if err := s.ensureLearnings(ctx); err != nil {
		return 0, err
	}
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return 0, fmt.Errorf("learning text is empty")
	}
	if len(text) > 500 {
		text = text[:500]
	}
	if len(pathGlob) > 200 {
		pathGlob = pathGlob[:200]
	}
	if source == "" {
		source = "chat"
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO learnings (repo, path_glob, text, source, enabled, created_at)
VALUES (?, ?, ?, ?, 1, ?)`,
		repo, pathGlob, text, source, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("add learning: %w", err)
	}
	return res.LastInsertId()
}

// ListLearnings returns a repository's learnings, newest first.
func (s *Store) ListLearnings(ctx context.Context, repo string, includeDisabled bool) ([]Learning, error) {
	if err := s.ensureLearnings(ctx); err != nil {
		return nil, err
	}
	q := `SELECT id, repo, path_glob, text, source, enabled, hits, created_at
FROM learnings WHERE repo = ?`
	if !includeDisabled {
		q += ` AND enabled = 1`
	}
	q += ` ORDER BY id DESC LIMIT 100`
	rows, err := s.db.QueryContext(ctx, q, repo)
	if err != nil {
		return nil, fmt.Errorf("list learnings: %w", err)
	}
	defer rows.Close()

	out := []Learning{}
	for rows.Next() {
		var l Learning
		var enabled int
		var created string
		if err := rows.Scan(&l.ID, &l.Repo, &l.PathGlob, &l.Text, &l.Source, &enabled, &l.Hits, &created); err != nil {
			return nil, err
		}
		l.Enabled = enabled == 1
		l.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeleteLearning removes one learning scoped to its repository.
func (s *Store) DeleteLearning(ctx context.Context, repo string, id int64) (bool, error) {
	if err := s.ensureLearnings(ctx); err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM learnings WHERE repo = ? AND id = ?`, repo, id)
	if err != nil {
		return false, fmt.Errorf("delete learning: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UpdateLearning edits one learning in place. Nil fields are left untouched, so
// the dashboard can toggle, retarget, or rewrite a single rule.
func (s *Store) UpdateLearning(ctx context.Context, repo string, id int64, text, pathGlob *string, enabled *bool) (bool, error) {
	if err := s.ensureLearnings(ctx); err != nil {
		return false, err
	}
	sets := make([]string, 0, 3)
	args := make([]any, 0, 5)
	if text != nil {
		clean := strings.Join(strings.Fields(*text), " ")
		if clean == "" {
			return false, fmt.Errorf("learning text is empty")
		}
		if len(clean) > 500 {
			clean = clean[:500]
		}
		sets = append(sets, "text = ?")
		args = append(args, clean)
	}
	if pathGlob != nil {
		glob := strings.TrimSpace(*pathGlob)
		if len(glob) > 200 {
			glob = glob[:200]
		}
		sets = append(sets, "path_glob = ?")
		args = append(args, glob)
	}
	if enabled != nil {
		v := 0
		if *enabled {
			v = 1
		}
		sets = append(sets, "enabled = ?")
		args = append(args, v)
	}
	if len(sets) == 0 {
		return false, fmt.Errorf("nothing to update")
	}
	args = append(args, repo, id)
	res, err := s.db.ExecContext(ctx,
		`UPDATE learnings SET `+strings.Join(sets, ", ")+` WHERE repo = ? AND id = ?`, args...)
	if err != nil {
		return false, fmt.Errorf("update learning: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SetLearningEnabled toggles one learning.
func (s *Store) SetLearningEnabled(ctx context.Context, repo string, id int64, enabled bool) error {
	if err := s.ensureLearnings(ctx); err != nil {
		return err
	}
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE learnings SET enabled = ? WHERE repo = ? AND id = ?`, v, repo, id)
	return err
}

// TouchLearnings increments usage counters for the injected set.
func (s *Store) TouchLearnings(ctx context.Context, ids []int64) {
	if len(ids) == 0 {
		return
	}
	for _, id := range ids {
		_, _ = s.db.ExecContext(ctx, `UPDATE learnings SET hits = hits + 1 WHERE id = ?`, id)
	}
}

// LearningTexts returns enabled instruction strings that apply to at least one
// changed path. An empty glob applies repo-wide.
func (s *Store) LearningTexts(ctx context.Context, repo string, paths []string) ([]string, error) {
	list, err := s.ListLearnings(ctx, repo, false)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list))
	for _, l := range list {
		if l.Text == "" {
			continue
		}
		if l.PathGlob != "" && !matchesAnyGlob(l.PathGlob, paths) {
			continue
		}
		out = append(out, l.Text)
		if len(out) >= 8 {
			break
		}
	}
	return out, nil
}

func matchesAnyGlob(glob string, paths []string) bool {
	glob = strings.ReplaceAll(strings.TrimSpace(glob), "\\", "/")
	if glob == "" {
		return true
	}
	for _, p := range paths {
		p = strings.ReplaceAll(p, "\\", "/")
		if ok, _ := path.Match(glob, p); ok {
			return true
		}
		if ok, _ := path.Match(glob, path.Base(p)); ok {
			return true
		}
		if strings.HasSuffix(glob, "/**") {
			prefix := strings.TrimSuffix(glob, "/**")
			if p == prefix || strings.HasPrefix(p, prefix+"/") {
				return true
			}
		}
	}
	return false
}
