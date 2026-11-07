package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Watch is the persisted set of repositories the bot reviews. All=true means
// every repository is reviewed and Repos is ignored.
type Watch struct {
	All   bool     `json:"all"`
	Repos []string `json:"repos"`
}

const watchKey = "watch"

// Watch returns the stored watch list. Installations that never saved one watch
// everything, which matches the behaviour before the setting existed.
func (s *Store) Watch(ctx context.Context) (Watch, error) {
	def := Watch{All: true, Repos: []string{}}

	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, watchKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return def, nil
	}
	if err != nil {
		return def, fmt.Errorf("read watch: %w", err)
	}

	var w Watch
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		return def, fmt.Errorf("decode watch: %w", err)
	}
	return normaliseWatch(w), nil
}

// SetWatch replaces the stored watch list.
func (s *Store) SetWatch(ctx context.Context, w Watch) error {
	payload, err := json.Marshal(normaliseWatch(w))
	if err != nil {
		return fmt.Errorf("encode watch: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, watchKey, string(payload))
	if err != nil {
		return fmt.Errorf("save watch: %w", err)
	}
	return nil
}

// normaliseWatch trims, drops blanks, and removes duplicates case-insensitively
// while keeping the owner/name casing GitHub uses.
func normaliseWatch(w Watch) Watch {
	seen := make(map[string]struct{}, len(w.Repos))
	repos := make([]string, 0, len(w.Repos))
	for _, repo := range w.Repos {
		repo = strings.TrimSpace(repo)
		if repo == "" {
			continue
		}
		key := strings.ToLower(repo)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		repos = append(repos, repo)
	}
	return Watch{All: w.All, Repos: repos}
}
